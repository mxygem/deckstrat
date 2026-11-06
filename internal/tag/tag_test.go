package tag

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mxygem/deckstrat/internal/catalog"
)

// loadCards reads real TCGdex cards recorded in testdata/cards.json.
func loadCards(t *testing.T) map[string]*catalog.Card {
	t.Helper()
	b, err := os.ReadFile("testdata/cards.json")
	if err != nil {
		t.Fatal(err)
	}
	var cards []*catalog.Card
	if err := json.Unmarshal(b, &cards); err != nil {
		t.Fatal(err)
	}
	m := map[string]*catalog.Card{}
	for _, c := range cards {
		m[c.ID] = c
	}
	return m
}

func keys(tags []Tag) []string {
	out := make([]string, len(tags))
	for i, t := range tags {
		out[i] = t.Key()
	}
	return out
}

func TestVocabulary(t *testing.T) {
	v := Vocab()
	seen := map[string]bool{}
	roles := map[string]bool{"": true, "attacker": true, "pokemon-search": true, "trainer-search": true, "draw": true,
		"energy": true, "energy-accel": true, "switching": true, "gust": true, "disruption": true, "recovery": true,
		"stadium": true, "tools": true}
	for _, term := range v.Terms {
		if seen[term.ID] {
			t.Errorf("duplicate term %s", term.ID)
		}
		seen[term.ID] = true
		if !roles[term.Role] {
			t.Errorf("%s: unknown role %q", term.ID, term.Role)
		}
		if term.Description == "" || term.Category == "" {
			t.Errorf("%s: missing description or category", term.ID)
		}
	}
	for _, term := range v.Terms {
		for _, by := range term.SatisfiedBy {
			if !seen[by] {
				t.Errorf("%s: satisfied_by names unknown term %s", term.ID, by)
			}
		}
	}
}

func TestRules(t *testing.T) {
	cards := loadCards(t)
	want := map[string][]string{
		"me01-131":    {"provides:discard-from-hand", "provides:search-pokemon"},
		"me01-125":    {"provides:evolution-accel"},
		"me01-114":    {"provides:gust"},
		"sv01-166":    {"provides:search-trainer"},
		"me01-173":    {"provides:recover-energy", "provides:recover-pokemon"},
		"me01-115":    {"provides:energy-move"},
		"sv04.5-080":  {"provides:draw", "provides:hand-disruption"},
		"me01-077":    {"needs:energy-attached:Fighting", "needs:energy-in-discard", "needs:search-pokemon", "provides:energy-accel-discard:Fighting"},
		"sv06-130":    {"needs:energy-attached:Fire", "needs:energy-attached:Psychic", "needs:evolution-accel", "needs:search-pokemon", "provides:spread-damage"},
		"sv01-086":    {"needs:energy-attached:Psychic", "needs:energy-in-discard", "needs:evolution-accel", "needs:search-pokemon", "provides:energy-accel-discard:Psychic"},
		"sv07-131":    {"provides:bench-expand"},
		"sv10.5b-085": {"provides:discard-from-hand", "provides:draw"},
		"mee-005":     nil,
	}
	for id, w := range want {
		c := cards[id]
		if c == nil {
			t.Fatalf("missing fixture %s", id)
		}
		tags, err := Rules{}.Tag(context.Background(), c)
		if err != nil {
			t.Fatal(err)
		}
		if got := keys(tags); !slices.Equal(got, w) {
			t.Errorf("%s (%s)\n got  %v\n want %v", c.Name, id, got, w)
		}
		for _, tg := range tags {
			if err := Vocab().Check(tg); err != nil {
				t.Errorf("%s: %v", id, err)
			}
			if tg.Reason == "" || tg.Source != Rule {
				t.Errorf("%s: %s needs a reason and the rule source", id, tg.Key())
			}
		}
	}
}

func TestSatisfies(t *testing.T) {
	v := Vocab()
	need := func(term, q string) Tag { return Tag{Kind: Needs, Term: term, Qualifier: q} }
	prov := func(term, q string) Tag { return Tag{Kind: Provides, Term: term, Qualifier: q} }
	tests := []struct {
		p, n Tag
		want bool
	}{
		{prov("energy-accel-discard", "Psychic"), need("energy-attached", "Psychic"), true},
		{prov("energy-accel-discard", "Fire"), need("energy-attached", "Psychic"), false},
		{prov("energy-move", ""), need("energy-attached", "Psychic"), true},
		{prov("discard-from-hand", ""), need("energy-in-discard", ""), true},
		{prov("search-pokemon", ""), need("search-pokemon", ""), true},
		{prov("bench-expand", ""), need("bench-size", ""), true},
		{prov("draw", ""), need("search-pokemon", ""), false},
		{need("draw", ""), need("cards-in-hand", ""), false}, // a need never satisfies
	}
	for _, tt := range tests {
		if got := v.Satisfies(tt.p, tt.n); got != tt.want {
			t.Errorf("Satisfies(%s, %s) = %v, want %v", tt.p.Key(), tt.n.Key(), got, tt.want)
		}
	}
}

func TestCheck(t *testing.T) {
	v := Vocab()
	bad := []Tag{
		{Kind: Provides, Term: "teleport", Confidence: 1},
		{Kind: "wants", Term: "draw", Confidence: 1},
		{Kind: Provides, Term: "draw", Qualifier: "Fire", Confidence: 1},
		{Kind: Provides, Term: "draw", Confidence: 1.5},
	}
	for _, tg := range bad {
		if v.Check(tg) == nil {
			t.Errorf("Check(%+v) should fail", tg)
		}
	}
	if err := v.Check(Tag{Kind: Needs, Term: "energy-attached", Qualifier: "Fire", Confidence: 0.5}); err != nil {
		t.Error(err)
	}
}

func TestMerge(t *testing.T) {
	got := Merge(
		[]Tag{{Kind: Provides, Term: "draw", Source: Rule, Confidence: 0.9}, {Kind: Provides, Term: "gust", Source: Rule, Confidence: 0.5}},
		[]Tag{{Kind: Provides, Term: "draw", Source: LLM, Confidence: 0.6}, {Kind: Provides, Term: "gust", Source: Rule, Confidence: 0.8}},
		[]Tag{{Kind: Provides, Term: "draw", Source: Human, Confidence: 0.1}},
	)
	if len(got) != 2 || got[0].Term != "draw" || got[0].Source != Human || got[1].Confidence != 0.8 {
		t.Errorf("Merge = %+v", got)
	}
}

func TestStore(t *testing.T) {
	cards := loadCards(t)
	ball, research := cards["me01-131"], cards["sv10.5b-085"]
	path := filepath.Join(t.TempDir(), "tags.json")

	s, err := LoadStore(path)
	if err != nil || len(s.Cards) != 0 {
		t.Fatalf("missing file should give an empty store: %v %v", s, err)
	}
	human := Tag{Kind: Provides, Term: "search-pokemon", Source: Human, Confidence: 1}
	s.Put(ball, []Tag{human})
	s.Put(ball, []Tag{{Kind: Provides, Term: "discard-from-hand", Source: Rule, Confidence: 0.7}})
	if got := keys(s.Get(ball).Tags); !slices.Equal(got, []string{"provides:discard-from-hand", "provides:search-pokemon"}) {
		t.Errorf("retagging should keep human tags, got %v", got)
	}

	s.Put(research, []Tag{{Kind: Provides, Term: "draw", Source: Rule, Confidence: 0.8}})
	s.Cards[KeyOf(research)].Reviewed = true
	s.Put(research, nil)
	if len(s.Get(research).Tags) != 1 {
		t.Error("reviewed entries should not change")
	}

	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	s2, err := LoadStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if e := s2.Get(ball); e == nil || e.Name != "Ultra Ball" || len(e.Tags) != 2 {
		t.Errorf("round trip lost data: %+v", e)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), `"vocab_version"`) {
		t.Error("saved store should record the vocabulary version")
	}

	// Reprints share an entry.
	reprint := *ball
	reprint.ID, reprint.SetCode = "sv01-196", "SVI"
	if s2.Get(&reprint) == nil {
		t.Error("a reprint should find the original's tags")
	}
}
