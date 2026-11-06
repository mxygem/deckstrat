// Package strat turns a starter card into a deck. It collects the
// starter's needs, finds cards whose provides meet them, and fills a role
// skeleton, giving a reason for every pick.
//
// This is v1 of PLAN.md's strat engine: tags only. Tournament
// co-occurrence and learned role counts come later; until then role
// counts and staples are hand-set in data/strat/<format>.json.
package strat

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"slices"
	"strings"

	"github.com/mxygem/deckstrat/internal/catalog"
	"github.com/mxygem/deckstrat/internal/tag"
)

// Config is a format's role skeleton.
type Config struct {
	Format string `json:"format"`
	Note   string `json:"note"`
	// Lines are copy counts for the starter's evolution line by the
	// starter's stage, Basic first: Stage2 [4, 2, 3] is 4-2-3.
	Lines map[string][]int `json:"lines"`
	// EngineLines are the same for the engine Pokémon.
	EngineLines map[string][]int `json:"engine_lines"`
	// Engines are support Pokémon names in order of preference; the first
	// one in the pool is added.
	Engines []string   `json:"engines"`
	Roles   []RoleSpec `json:"roles"`
	Energy  struct {
		Min int `json:"min"`
		Max int `json:"max"`
	} `json:"energy"`
}

// RoleSpec is one skeleton role. When gates it: "" always, "stage2" when
// the starter's line has a Stage 2, "needs:<term>" when the line needs
// that term.
type RoleSpec struct {
	Role    string   `json:"role"`
	Count   int      `json:"count"`
	When    string   `json:"when,omitempty"`
	Staples []string `json:"staples"`
}

// LoadConfig reads a role skeleton file.
func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	return &c, nil
}

// Tags gives a card's tags.
type Tags interface {
	For(c *catalog.Card) []tag.Tag
}

// StoreTags reads tags from a store and falls back to the rules tagger
// for cards the store has not seen.
type StoreTags struct{ Store *tag.Store }

func (s StoreTags) For(c *catalog.Card) []tag.Tag {
	if s.Store != nil {
		if e := s.Store.Get(c); e != nil {
			return e.Tags
		}
	}
	tags, _ := tag.Rules{}.Tag(context.Background(), c)
	return tags
}

// Pick is one deck line with the reason it is there.
type Pick struct {
	Card   *catalog.Card
	Qty    int
	Role   string
	Reason string
}

// Need is one of the starter line's needs, and which card has it.
type Need struct {
	Tag  tag.Tag
	From string // card name
}

// Lead is a Pokémon that meets the line's needs but was not added: a
// candidate partner worth testing.
type Lead struct {
	Card    *catalog.Card
	Score   float64
	Reasons []string
}

// Result is a built deck.
type Result struct {
	Starter *catalog.Card
	Needs   []Need
	Picks   []Pick
	Leads   []Lead
	Notes   []string
}

// Total counts the deck's cards.
func (r *Result) Total() int {
	n := 0
	for _, p := range r.Picks {
		n += p.Qty
	}
	return n
}

// MinConfidence drops tags too uncertain to build around.
const MinConfidence = 0.5

const deckSize = 60

type builder struct {
	cfg    *Config
	tags   Tags
	vocab  *tag.Vocabulary
	byName map[string][]*catalog.Card
	res    *Result
	copies map[string]int // by rule name
	inLine map[string]bool
}

// Build makes a deck around a Pokémon starter from a legal pool.
func Build(starter *catalog.Card, pool []*catalog.Card, tags Tags, cfg *Config) (*Result, error) {
	if starter.Category != catalog.Pokemon {
		return nil, fmt.Errorf("%s is a %s; v1 builds around Pokémon starters only", starter.Name, starter.Category)
	}
	b := &builder{
		cfg: cfg, tags: tags, vocab: tag.Vocab(),
		byName: map[string][]*catalog.Card{},
		res:    &Result{Starter: starter},
		copies: map[string]int{},
		inLine: map[string]bool{},
	}
	for _, c := range pool {
		n := catalog.RuleName(c.Name)
		b.byName[n] = append(b.byName[n], c)
	}

	line := b.line(starter)
	b.addLine(line, cfg.Lines[starter.Stage], "starter", fmt.Sprintf("evolves into %s", starter.Name))
	b.collectNeeds(line)
	b.addEngine()
	for _, spec := range cfg.Roles {
		if b.applies(spec, line) {
			b.fillRole(spec)
		}
	}
	b.addEnergy(line)
	b.findLeads(pool)
	return b.res, nil
}

// line walks a Pokémon's evolves-from chain through the pool, Basic first.
func (b *builder) line(top *catalog.Card) []*catalog.Card {
	chain := []*catalog.Card{top}
	for cur := top; cur.EvolvesFrom != ""; {
		prev := b.best(cur.EvolvesFrom, catalog.Pokemon)
		if prev == nil {
			b.note("no legal %s to evolve %s from", cur.EvolvesFrom, cur.Name)
			break
		}
		chain = append([]*catalog.Card{prev}, chain...)
		cur = prev
	}
	return chain
}

// best picks the pool card with a name, preferring more tags, then HP.
func (b *builder) best(name string, cat catalog.Category) *catalog.Card {
	var out *catalog.Card
	for _, c := range b.byName[catalog.RuleName(name)] {
		if c.Category != cat {
			continue
		}
		if out == nil || len(b.tags.For(c)) > len(b.tags.For(out)) ||
			(len(b.tags.For(c)) == len(b.tags.For(out)) && c.HP > out.HP) {
			out = c
		}
	}
	return out
}

func (b *builder) addLine(line []*catalog.Card, counts []int, role, evolveReason string) {
	// A short chain (a missing Basic) uses the counts for its top stages.
	if len(counts) > len(line) {
		counts = counts[len(counts)-len(line):]
	}
	for i, c := range line {
		qty := 1
		if i < len(counts) {
			qty = counts[i]
		}
		reason := "starter card"
		if i < len(line)-1 {
			reason = fmt.Sprintf("evolves into %s", line[i+1].Name)
		}
		if role != "starter" && i == len(line)-1 {
			reason = evolveReason
		}
		b.inLine[c.ID] = true
		b.add(c, qty, role, reason)
	}
}

// collectNeeds gathers the line's needs, top stage first, so the
// starter's own attack costs win ties with its pre-evolutions'.
func (b *builder) collectNeeds(line []*catalog.Card) {
	seen := map[string]int{}
	for i := len(line) - 1; i >= 0; i-- {
		c := line[i]
		for _, t := range b.tags.For(c) {
			if t.Kind != tag.Needs || t.Confidence < MinConfidence {
				continue
			}
			if i, ok := seen[t.Key()]; ok {
				if t.Confidence > b.res.Needs[i].Tag.Confidence {
					b.res.Needs[i] = Need{Tag: t, From: c.Name}
				}
				continue
			}
			seen[t.Key()] = len(b.res.Needs)
			b.res.Needs = append(b.res.Needs, Need{Tag: t, From: c.Name})
		}
	}
}

func (b *builder) addEngine() {
	for _, name := range b.cfg.Engines {
		top := b.best(name, catalog.Pokemon)
		if top == nil {
			b.note("engine %s is not in the pool; skipped", name)
			continue
		}
		if b.inLine[top.ID] {
			return
		}
		var provides []string
		for _, t := range b.tags.For(top) {
			if t.Kind == tag.Provides && t.Confidence >= MinConfidence {
				provides = append(provides, t.Term)
			}
		}
		reason := "engine Pokémon"
		if len(provides) > 0 {
			reason += ": provides " + strings.Join(provides, ", ")
		}
		line := b.line(top)
		b.addLine(line, b.cfg.EngineLines[top.Stage], "engine", reason)
		return
	}
}

func (b *builder) applies(spec RoleSpec, line []*catalog.Card) bool {
	switch {
	case spec.When == "":
		return true
	case spec.When == "stage2":
		return slices.ContainsFunc(line, func(c *catalog.Card) bool { return c.Stage == "Stage2" })
	case strings.HasPrefix(spec.When, "needs:"):
		term := strings.TrimPrefix(spec.When, "needs:")
		return slices.ContainsFunc(b.res.Needs, func(n Need) bool { return n.Tag.Term == term })
	}
	b.note("role %s has unknown condition %q; skipped", spec.Role, spec.When)
	return false
}

// fillRole adds cards for a role: staples first, in order, then
// Trainers whose provides meet a need, best score first. Cards already
// in the deck that fill the role count toward it.
func (b *builder) fillRole(spec RoleSpec) {
	want := spec.Count - b.roleCount(spec.Role)
	if want <= 0 {
		return
	}
	for _, name := range spec.Staples {
		if want <= 0 {
			return
		}
		c := b.bestTrainer(name)
		if c == nil {
			b.note("staple %s (%s) is not in the pool; skipped", name, spec.Role)
			continue
		}
		if spec.Role == "ace-spec" && !c.IsAceSpec() {
			continue
		}
		want -= b.add(c, min(want, 4), spec.Role, "staple "+roleLabel(spec.Role))
	}
	if spec.Role == "ace-spec" {
		return
	}
	for _, cand := range b.satisfiers(spec.Role) {
		if want <= 0 {
			return
		}
		want -= b.add(cand.Card, min(want, 2), spec.Role, strings.Join(cand.Reasons, "; "))
	}
}

func (b *builder) bestTrainer(name string) *catalog.Card {
	if c := b.best(name, catalog.Trainer); c != nil {
		return c
	}
	return b.best(name, catalog.Energy)
}

// roleCount counts deck cards that provide a term filling role.
func (b *builder) roleCount(role string) int {
	n := 0
	for _, p := range b.res.Picks {
		if p.Role == role || (p.Card.Category != catalog.Pokemon && b.fills(p.Card, role)) {
			n += p.Qty
		}
	}
	return n
}

func (b *builder) fills(c *catalog.Card, role string) bool {
	if role == "ace-spec" {
		return c.IsAceSpec()
	}
	for _, t := range b.tags.For(c) {
		if t.Kind != tag.Provides || t.Confidence < MinConfidence {
			continue
		}
		if term, ok := b.vocab.Term(t.Term); ok && term.Role == role {
			return true
		}
	}
	return false
}

type candidate struct {
	Card    *catalog.Card
	Score   float64
	Reasons []string
}

// score sums need confidence × provide confidence over the needs a card
// meets, with one reason line: "provides A, B → meets X's need for C".
func (b *builder) score(c *catalog.Card) candidate {
	cand := candidate{Card: c}
	var provides, needs []string
	for _, p := range b.tags.For(c) {
		if p.Kind != tag.Provides || p.Confidence < MinConfidence {
			continue
		}
		for _, n := range b.res.Needs {
			if !b.vocab.Satisfies(p, n.Tag) {
				continue
			}
			cand.Score += n.Tag.Confidence * p.Confidence
			if d := describe(p); !slices.Contains(provides, d) {
				provides = append(provides, d)
			}
			if d := fmt.Sprintf("%s's need for %s", n.From, describe(n.Tag)); !slices.Contains(needs, d) {
				needs = append(needs, d)
			}
		}
	}
	if len(provides) > 0 {
		cand.Reasons = []string{fmt.Sprintf("provides %s → meets %s", strings.Join(provides, ", "), strings.Join(needs, ", "))}
	}
	return cand
}

// satisfiers lists non-Pokémon cards that fill role and meet a need.
func (b *builder) satisfiers(role string) []candidate {
	var out []candidate
	for _, cards := range b.byName {
		for _, c := range cards {
			if c.Category == catalog.Pokemon || c.IsBasicEnergy() || !b.fills(c, role) {
				continue
			}
			if cand := b.score(c); cand.Score > 0 {
				out = append(out, cand)
			}
		}
	}
	slices.SortFunc(out, func(x, y candidate) int {
		return cmp.Or(cmp.Compare(y.Score, x.Score), cmp.Compare(x.Card.Name, y.Card.Name), cmp.Compare(x.Card.ID, y.Card.ID))
	})
	return out
}

// addEnergy fills the rest of the deck with Basic Energy split by the
// line's typed Energy needs, then tops up or trims to 60.
func (b *builder) addEnergy(line []*catalog.Card) {
	weights := map[string]float64{}
	for _, n := range b.res.Needs {
		if n.Tag.Term == "energy-attached" && n.Tag.Qualifier != "" {
			weights[n.Tag.Qualifier] += n.Tag.Confidence
		}
	}
	if len(weights) == 0 {
		for _, t := range b.res.Starter.Types {
			if t != "Colorless" {
				weights[t] = 1
			}
		}
	}
	if len(weights) == 0 {
		// Colorless attacks take any Energy, so the right type depends on
		// the partners. Pick the first Basic Energy type the pool has.
		for _, t := range tag.EnergyTypes() {
			if e := b.best(t+" Energy", catalog.Energy); e != nil && e.IsBasicEnergy() {
				weights[t] = 1
				b.note("%s's attacks cost only Colorless Energy; using %s Energy, so change it to suit your partners", b.res.Starter.Name, t)
				break
			}
		}
	}
	types := make([]string, 0, len(weights))
	var sum float64
	for t, w := range weights {
		types = append(types, t)
		sum += w
	}
	slices.SortFunc(types, func(x, y string) int { return cmp.Or(cmp.Compare(weights[y], weights[x]), cmp.Compare(x, y)) })

	room := deckSize - b.res.Total()
	n := min(max(room, b.cfg.Energy.Min), b.cfg.Energy.Max)
	if room < n {
		b.trim(n - room)
	}
	given := 0
	var mainEnergy *catalog.Card
	for i, t := range types {
		e := b.best(t+" Energy", catalog.Energy)
		if e == nil || !e.IsBasicEnergy() {
			b.note("no Basic %s Energy in the pool", t)
			continue
		}
		qty := int(math.Round(float64(n) * weights[t] / sum))
		if i == len(types)-1 {
			qty = n - given
		}
		given += b.add(e, qty, "energy", fmt.Sprintf("Basic Energy for %s attacks", t))
		if mainEnergy == nil {
			mainEnergy = e
		}
	}
	if left := deckSize - b.res.Total(); left > 0 {
		left = b.topUp(left)
		if left > 0 && mainEnergy != nil {
			b.add(mainEnergy, left, "energy", "")
			b.note("added %d extra %s to reach %d cards; the skeleton has no other cards to add copies of", left, mainEnergy.Name, deckSize)
		} else if left > 0 {
			b.note("deck is %d cards short of %d", left, deckSize)
		}
	}
}

// trim removes copies from the last-added Trainer picks to make room.
func (b *builder) trim(n int) {
	for i := len(b.res.Picks) - 1; i >= 0 && n > 0; i-- {
		p := &b.res.Picks[i]
		if p.Card.Category == catalog.Pokemon || p.Role == "ace-spec" {
			continue
		}
		take := min(n, p.Qty-1)
		if take <= 0 {
			continue
		}
		p.Qty -= take
		b.copies[catalog.RuleName(p.Card.Name)] -= take
		n -= take
	}
}

// topUp adds copies of draw and search picks, then of the other Trainer
// picks, until the deck is full, and returns how many cards are still
// missing.
func (b *builder) topUp(n int) int {
	for _, role := range []string{"draw", "pokemon-search", "recovery", "switching", "gust", "energy-accel", "trainer-search", "stadium"} {
		for i := range b.res.Picks {
			if n <= 0 {
				return 0
			}
			p := &b.res.Picks[i]
			if p.Role != role {
				continue
			}
			key := catalog.RuleName(p.Card.Name)
			add := min(n, 4-b.copies[key])
			if add > 0 {
				p.Qty += add
				b.copies[key] += add
				n -= add
			}
		}
	}
	return n
}

// findLeads lists Pokémon that meet the line's needs but were not added.
func (b *builder) findLeads(pool []*catalog.Card) {
	for _, c := range pool {
		if c.Category != catalog.Pokemon || b.copies[catalog.RuleName(c.Name)] > 0 {
			continue
		}
		if cand := b.score(c); cand.Score > 0 {
			b.res.Leads = append(b.res.Leads, Lead{Card: c, Score: cand.Score, Reasons: cand.Reasons})
		}
	}
	slices.SortFunc(b.res.Leads, func(x, y Lead) int {
		return cmp.Or(cmp.Compare(y.Score, x.Score), cmp.Compare(x.Card.Name, y.Card.Name))
	})
	if len(b.res.Leads) > 5 {
		b.res.Leads = b.res.Leads[:5]
	}
}

// add records copies of a card within the copy limits and returns how
// many were added.
func (b *builder) add(c *catalog.Card, qty int, role, reason string) int {
	key := catalog.RuleName(c.Name)
	if !c.IsBasicEnergy() {
		qty = min(qty, 4-b.copies[key])
	}
	if c.IsAceSpec() {
		for _, p := range b.res.Picks {
			if p.Card.IsAceSpec() {
				return 0
			}
		}
		qty = min(qty, 1)
	}
	if qty <= 0 {
		return 0
	}
	for i := range b.res.Picks {
		if b.res.Picks[i].Card.ID == c.ID {
			b.res.Picks[i].Qty += qty
			b.copies[key] += qty
			return qty
		}
	}
	b.res.Picks = append(b.res.Picks, Pick{Card: c, Qty: qty, Role: role, Reason: reason})
	b.copies[key] += qty
	return qty
}

func (b *builder) note(format string, args ...any) {
	b.res.Notes = append(b.res.Notes, fmt.Sprintf(format, args...))
}

func describe(t tag.Tag) string {
	if t.Qualifier != "" {
		return fmt.Sprintf("%s (%s)", t.Term, t.Qualifier)
	}
	return t.Term
}

func roleLabel(role string) string { return strings.ReplaceAll(role, "-", " ") }
