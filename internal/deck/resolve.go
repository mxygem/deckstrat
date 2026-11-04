package deck

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/mxygem/deckstrat/internal/catalog"
)

// Entry is a decklist line matched to a catalog printing.
type Entry struct {
	Line Line
	Card *catalog.Card
}

// Unmatched is a line that could not be matched to exactly one printing.
type Unmatched struct {
	Line   Line
	Reason string
}

// Resolve matches each line to a printing. Lines with a set code and
// number are looked up directly; the printing's name must agree with the
// line. Name-only lines take the newest printing by regulation mark.
// Catalog errors other than not-found abort resolution.
func Resolve(ctx context.Context, cat catalog.Catalog, d Deck) ([]Entry, []Unmatched, error) {
	var (
		entries []Entry
		missing []Unmatched
	)
	for _, l := range d.Lines {
		card, reason, err := resolveLine(ctx, cat, l)
		if err != nil {
			return nil, nil, fmt.Errorf("line %d (%s): %w", l.LineNo, l, err)
		}
		if card == nil {
			missing = append(missing, Unmatched{Line: l, Reason: reason})
			continue
		}
		entries = append(entries, Entry{Line: l, Card: card})
	}
	return entries, missing, nil
}

func resolveLine(ctx context.Context, cat catalog.Catalog, l Line) (*catalog.Card, string, error) {
	name := CatalogName(l.Name)
	if l.SetCode != "" {
		card, err := cat.GetByCode(ctx, l.SetCode, l.Number)
		switch {
		case errors.Is(err, catalog.ErrNotFound):
			return nil, fmt.Sprintf("no card %s %s in the catalog", l.SetCode, l.Number), nil
		case err != nil:
			return nil, "", err
		case !namesAgree(name, card.Name):
			return nil, fmt.Sprintf("%s %s is %q, not %q; check the set code and number", l.SetCode, l.Number, card.Name, l.Name), nil
		}
		return card, "", nil
	}

	refs, err := cat.Search(ctx, catalog.Query{Name: name, Exact: true, Limit: 20})
	if err != nil {
		return nil, "", err
	}
	if len(refs) == 0 {
		return nil, fmt.Sprintf("no card named %q in the catalog", name), nil
	}
	var cards []*catalog.Card
	for _, r := range refs {
		c, err := cat.Get(ctx, r.ID)
		if errors.Is(err, catalog.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, "", err
		}
		cards = append(cards, c)
	}
	if len(cards) == 0 {
		return nil, fmt.Sprintf("no card named %q in the catalog", name), nil
	}
	sort.SliceStable(cards, func(i, j int) bool { return cards[i].RegulationMark > cards[j].RegulationMark })
	return cards[0], "", nil
}

// namesAgree tolerates suffixes the catalog adds, e.g. a line "Professor's
// Research" matching "Professor's Research (Professor Turo)".
func namesAgree(lineName, cardName string) bool {
	if catalog.SameName(lineName, cardName) {
		return true
	}
	a, b := strings.ToLower(lineName), strings.ToLower(cardName)
	return strings.HasPrefix(b, a+" (") || strings.HasPrefix(a, b+" (")
}
