// Package tag gives cards needs and provides tags from one fixed
// vocabulary. The strat engine matches a starter card's needs against
// other cards' provides.
//
// Tags attach to the functional card (catalog.Card.FunctionalKey), so
// every reprint shares them.
package tag

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/mxygem/deckstrat/internal/catalog"
)

type Kind string

const (
	Needs    Kind = "needs"
	Provides Kind = "provides"
)

// Source records who made a tag. Human tags win over the others.
type Source string

const (
	Rule  Source = "rule"
	LLM   Source = "llm"
	Human Source = "human"
)

// Tag is one need or provide. Qualifier narrows a qualified term, such as
// the Energy type for energy-attached.
type Tag struct {
	Kind       Kind    `json:"kind"`
	Term       string  `json:"term"`
	Qualifier  string  `json:"qualifier,omitempty"`
	Source     Source  `json:"source"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason,omitempty"`
}

// Key identifies a tag regardless of source: "needs:energy-attached:Grass".
func (t Tag) Key() string {
	k := string(t.Kind) + ":" + t.Term
	if t.Qualifier != "" {
		k += ":" + t.Qualifier
	}
	return k
}

// Tagger proposes tags for a card.
type Tagger interface {
	Tag(ctx context.Context, c *catalog.Card) ([]Tag, error)
}

// Term is one vocabulary entry. Role is the deck skeleton role a card
// providing the term fills, or "" when it fills none by itself.
type Term struct {
	ID          string `json:"id"`
	Category    string `json:"category"`
	Role        string `json:"role"`
	Description string `json:"description"`
	// Qualified terms take a Qualifier: an Energy type, or a card name
	// for stadium-in-play.
	Qualified bool `json:"qualified,omitempty"`
	// SatisfiedBy lists the provides terms that meet this term as a need.
	// Empty means only providing the same term does.
	SatisfiedBy []string `json:"satisfied_by,omitempty"`
}

type Vocabulary struct {
	Version string `json:"version"`
	Terms   []Term `json:"terms"`
}

//go:embed vocab.json
var vocabJSON []byte

var vocab = mustVocab()

func mustVocab() *Vocabulary {
	var v Vocabulary
	dec := json.NewDecoder(strings.NewReader(string(vocabJSON)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		panic(fmt.Sprintf("tag vocabulary: %v", err))
	}
	return &v
}

// Vocab returns the tag vocabulary.
func Vocab() *Vocabulary { return vocab }

// Term looks up a vocabulary term by ID.
func (v *Vocabulary) Term(id string) (Term, bool) {
	i := slices.IndexFunc(v.Terms, func(t Term) bool { return t.ID == id })
	if i < 0 {
		return Term{}, false
	}
	return v.Terms[i], true
}

// Satisfies reports whether a provided tag meets a needed one. A typed
// provide meets only a need of the same type: Fire acceleration does not
// meet a Psychic or an untyped Energy need. An untyped provide, such as
// Energy Switch, meets any.
func (v *Vocabulary) Satisfies(provided, needed Tag) bool {
	if provided.Kind != Provides || needed.Kind != Needs {
		return false
	}
	term, ok := v.Term(needed.Term)
	if !ok {
		return false
	}
	by := term.SatisfiedBy
	if len(by) == 0 {
		by = []string{term.ID}
	}
	if !slices.Contains(by, provided.Term) {
		return false
	}
	return provided.Qualifier == "" || provided.Qualifier == needed.Qualifier
}

// Check reports why a tag does not fit the vocabulary, or nil.
func (v *Vocabulary) Check(t Tag) error {
	term, ok := v.Term(t.Term)
	switch {
	case !ok:
		return fmt.Errorf("unknown term %q", t.Term)
	case t.Kind != Needs && t.Kind != Provides:
		return fmt.Errorf("%s: kind must be needs or provides, got %q", t.Term, t.Kind)
	case t.Qualifier != "" && !term.Qualified:
		return fmt.Errorf("%s takes no qualifier, got %q", t.Term, t.Qualifier)
	case t.Confidence < 0 || t.Confidence > 1:
		return fmt.Errorf("%s: confidence %v is outside 0..1", t.Term, t.Confidence)
	}
	return nil
}

// Merge combines tag sets. For each Key the most trusted source wins
// (human over llm over rule), then the higher confidence. The result is
// sorted by Key.
func Merge(sets ...[]Tag) []Tag {
	rank := map[Source]int{Rule: 1, LLM: 2, Human: 3}
	best := map[string]Tag{}
	for _, set := range sets {
		for _, t := range set {
			cur, ok := best[t.Key()]
			if !ok || rank[t.Source] > rank[cur.Source] ||
				(rank[t.Source] == rank[cur.Source] && t.Confidence > cur.Confidence) {
				best[t.Key()] = t
			}
		}
	}
	out := make([]Tag, 0, len(best))
	for _, t := range best {
		out = append(out, t)
	}
	slices.SortFunc(out, func(a, b Tag) int { return strings.Compare(a.Key(), b.Key()) })
	return out
}
