package format

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mxygem/deckstrat/internal/catalog"
	"github.com/mxygem/deckstrat/internal/deck"
)

// Violation is one reason a deck is not legal.
type Violation struct {
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

// Note is information that does not make the deck illegal, such as a
// printing that is legal only through a reprint.
type Note struct {
	Message string `json:"message"`
}

// Result is the outcome of validating a deck.
type Result struct {
	Violations []Violation `json:"violations"`
	Notes      []Note      `json:"notes"`
}

func (r Result) Legal() bool { return len(r.Violations) == 0 }

// PrintingFinder lists every printing of a card name.
type PrintingFinder interface {
	Printings(ctx context.Context, name string) ([]*catalog.Card, error)
}

// Validator checks decks against a format.
type Validator struct {
	Format *Format
	// Reprints finds legal reprints. Nil disables the reprint rule.
	Reprints PrintingFinder
	// AsOf decides which bans are in effect. Zero means now.
	AsOf time.Time
}

// Validate returns every rule the deck breaks, with reasons.
func (v *Validator) Validate(ctx context.Context, entries []deck.Entry) (Result, error) {
	res := Result{Violations: []Violation{}, Notes: []Note{}}
	f := v.Format
	add := func(rule, format string, args ...any) {
		res.Violations = append(res.Violations, Violation{Rule: rule, Message: fmt.Sprintf(format, args...)})
	}

	total := 0
	for _, e := range entries {
		total += e.Line.Qty
	}
	if total != f.Rules.Size {
		add("deck-size", "deck has %d cards; %s requires exactly %d", total, f.Name, f.Rules.Size)
	}

	// Copy limits count by name across printings.
	type nameCount struct {
		name     string
		total    int
		printing []string
	}
	byName := map[string]*nameCount{}
	var order []string
	for _, e := range entries {
		if e.Card.IsBasicEnergy() {
			continue
		}
		key := catalog.RuleName(e.Card.Name)
		nc, ok := byName[key]
		if !ok {
			nc = &nameCount{name: key}
			byName[key] = nc
			order = append(order, key)
		}
		nc.total += e.Line.Qty
		nc.printing = append(nc.printing, fmt.Sprintf("%d × %s", e.Line.Qty, printing(e.Card)))
	}
	for _, key := range order {
		nc := byName[key]
		if nc.total <= f.Rules.CopyLimit {
			continue
		}
		detail := ""
		if len(nc.printing) > 1 {
			detail = " (" + strings.Join(nc.printing, ", ") + ")"
		}
		if f.Rules.CopyLimit == 1 {
			add("copy-limit", "%d copies of %s%s; %s is singleton except Basic Energy", nc.total, nc.name, detail, f.Name)
		} else {
			add("copy-limit", "%d copies of %s%s; the limit is %d per name", nc.total, nc.name, detail, f.Rules.CopyLimit)
		}
	}

	var aceSpecs []string
	aceCount, basics := 0, 0
	for _, e := range entries {
		if e.Card.IsAceSpec() {
			aceCount += e.Line.Qty
			aceSpecs = append(aceSpecs, fmt.Sprintf("%d × %s", e.Line.Qty, e.Card.Name))
		}
		if e.Card.IsBasicPokemon() {
			basics += e.Line.Qty
		}
	}
	switch {
	case aceCount > 0 && f.Rules.MaxAceSpec == 0:
		add("ace-spec", "%s allows no ACE SPEC cards; deck has %s", f.Name, strings.Join(aceSpecs, ", "))
	case aceCount > f.Rules.MaxAceSpec:
		add("ace-spec", "deck has %d ACE SPEC cards (%s); %s allows %d", aceCount, strings.Join(aceSpecs, ", "), f.Name, f.Rules.MaxAceSpec)
	}
	if basics == 0 {
		add("basic-pokemon", "deck has no Basic Pokémon")
	}

	if f.Rules.NoRuleBox {
		for _, e := range entries {
			if e.Card.IsRuleBox() {
				add("rule-box", "%s is a Rule Box Pokémon; %s bans them", printing(e.Card), f.Name)
			}
		}
	}
	if f.Rules.SingleType {
		if msg := singleTypeViolation(entries); msg != "" {
			add("single-type", "%s; %s requires every Pokémon to share one type", msg, f.Name)
		}
	}

	asOf := v.AsOf
	if asOf.IsZero() {
		asOf = time.Now()
	}
	for _, b := range f.BanList {
		if b.Effective != "" && b.Effective > asOf.Format(time.DateOnly) {
			continue
		}
		for _, e := range entries {
			if catalog.SameName(catalog.RuleName(e.Card.Name), b.Name) {
				add("banned", "%s is banned in %s (since %s)", e.Card.Name, f.Name, b.Effective)
			}
		}
	}

	for _, e := range entries {
		reason := v.Format.PoolReason(e.Card)
		if reason == "" {
			continue
		}
		if f.Pool.ReprintsLegal && v.Reprints != nil {
			legal, err := v.legalReprint(ctx, e.Card)
			if err != nil {
				return Result{}, err
			}
			if legal != nil {
				res.Notes = append(res.Notes, Note{Message: fmt.Sprintf("%s %s, but is legal through its reprint %s", printing(e.Card), reason, printing(legal))})
				continue
			}
			reason += " and no legal reprint was found"
		}
		add("pool", "%s %s", printing(e.Card), reason)
	}
	return res, nil
}

func (v *Validator) legalReprint(ctx context.Context, c *catalog.Card) (*catalog.Card, error) {
	printings, err := v.Reprints.Printings(ctx, c.Name)
	if err != nil {
		return nil, fmt.Errorf("finding reprints of %s: %w", c.Name, err)
	}
	key := c.FunctionalKey()
	for _, p := range printings {
		if p.ID != c.ID && p.FunctionalKey() == key && v.Format.PoolReason(p) == "" {
			return p, nil
		}
	}
	return nil, nil
}

func singleTypeViolation(entries []deck.Entry) string {
	var common map[string]bool
	seen := map[string]bool{}
	for _, e := range entries {
		if e.Card.Category != catalog.Pokemon {
			continue
		}
		types := map[string]bool{}
		for _, t := range e.Card.Types {
			types[t] = true
			seen[t] = true
		}
		if common == nil {
			common = types
			continue
		}
		for t := range common {
			if !types[t] {
				delete(common, t)
			}
		}
	}
	if common == nil || len(common) > 0 {
		return ""
	}
	all := make([]string, 0, len(seen))
	for t := range seen {
		all = append(all, t)
	}
	sort.Strings(all)
	return "Pokémon types " + strings.Join(all, ", ") + " have no type in common"
}

func printing(c *catalog.Card) string {
	if c.SetCode == "" {
		return fmt.Sprintf("%s (%s)", c.Name, c.ID)
	}
	return fmt.Sprintf("%s (%s %s)", c.Name, c.SetCode, strings.TrimLeft(c.Number, "0"))
}

// CatalogPrintings finds printings by rule name through a catalog, so
// "Boss's Orders" also finds "Boss's Orders (Giovanni)".
func CatalogPrintings(cat catalog.Catalog) PrintingFinder { return catalogPrintings{cat} }

type catalogPrintings struct{ cat catalog.Catalog }

func (p catalogPrintings) Printings(ctx context.Context, name string) ([]*catalog.Card, error) {
	name = catalog.RuleName(name)
	refs, err := p.cat.Search(ctx, catalog.Query{Name: name})
	if err != nil {
		return nil, err
	}
	var out []*catalog.Card
	for _, r := range refs {
		if catalog.RuleName(r.Name) != name {
			continue
		}
		c, err := p.cat.Get(ctx, r.ID)
		if errors.Is(err, catalog.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}
