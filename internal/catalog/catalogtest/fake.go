// Package catalogtest provides an in-memory catalog.Catalog for tests.
package catalogtest

import (
	"context"
	"strings"

	"github.com/mxygem/deckstrat/internal/catalog"
)

// Fake serves a fixed card list. Like TCGdex, its exact-name search
// ignores case and punctuation.
type Fake struct {
	Cards []catalog.Card
}

var _ catalog.Catalog = (*Fake)(nil)

func (f *Fake) Get(_ context.Context, id string) (*catalog.Card, error) {
	for i := range f.Cards {
		if f.Cards[i].ID == id {
			c := f.Cards[i]
			return &c, nil
		}
	}
	return nil, catalog.ErrNotFound
}

// GetByCode matches SetCode and number like TCGdex: name is preferred,
// and a unique name in the set wins when the number does not agree.
func (f *Fake) GetByCode(_ context.Context, setCode, number, name string) (*catalog.Card, error) {
	var atNumber, byName []catalog.Card
	for _, c := range f.Cards {
		if !strings.EqualFold(c.SetCode, setCode) {
			continue
		}
		if strings.TrimLeft(c.Number, "0") == strings.TrimLeft(number, "0") {
			if name == "" || catalog.NamesAgree(name, c.Name) {
				return &c, nil
			}
			atNumber = append(atNumber, c)
		}
		if name != "" && catalog.NamesAgree(name, c.Name) {
			byName = append(byName, c)
		}
	}
	if len(byName) == 1 {
		return &byName[0], nil
	}
	if len(atNumber) > 0 {
		return &atNumber[0], nil
	}
	return nil, catalog.ErrNotFound
}

func (f *Fake) Search(_ context.Context, q catalog.Query) ([]catalog.Ref, error) {
	var refs []catalog.Ref
	for _, c := range f.Cards {
		match := catalog.SameName(c.Name, q.Name)
		if !q.Exact {
			match = strings.Contains(strings.ToLower(c.Name), strings.ToLower(q.Name))
		}
		if match && (q.SetID == "" || q.SetID == c.SetID) {
			refs = append(refs, catalog.Ref{ID: c.ID, Name: c.Name, Series: c.Series})
		}
		if q.Limit > 0 && len(refs) == q.Limit {
			break
		}
	}
	return refs, nil
}
