package strat

import (
	"strings"
	"testing"

	"github.com/mxygem/deckstrat/internal/catalog"
	"github.com/mxygem/deckstrat/internal/tag"
)

// fakeTags gives tags by card ID.
type fakeTags map[string][]tag.Tag

func (f fakeTags) For(c *catalog.Card) []tag.Tag { return f[c.ID] }

func prov(term, q string) tag.Tag {
	return tag.Tag{Kind: tag.Provides, Term: term, Qualifier: q, Confidence: 0.9, Reason: "test"}
}
func need(term, q string) tag.Tag {
	return tag.Tag{Kind: tag.Needs, Term: term, Qualifier: q, Confidence: 0.9, Reason: "test"}
}

func testPool() ([]*catalog.Card, fakeTags) {
	pk := func(id, name, stage, from string) *catalog.Card {
		return &catalog.Card{ID: id, Name: name, Category: catalog.Pokemon, Stage: stage, EvolvesFrom: from, HP: 100, Types: []string{"Fire"}}
	}
	tr := func(id, name string) *catalog.Card {
		return &catalog.Card{ID: id, Name: name, Category: catalog.Trainer, TrainerType: "Item"}
	}
	cards := []*catalog.Card{
		pk("p1", "Charmander", "Basic", ""),
		pk("p1b", "Charmander", "Basic", ""), // a weaker printing
		pk("p2", "Charmeleon", "Stage1", "Charmander"),
		pk("p3", "Charizard ex", "Stage2", "Charmeleon"),
		pk("e1", "Helper", "Basic", ""),
		pk("lead", "Fire Friend", "Basic", ""),
		tr("t1", "Ultra Ball"),
		tr("t2", "Rare Candy"),
		tr("t3", "Research"),
		tr("t4", "Fire Pump"),
		tr("t5", "Water Pump"),
		{ID: "ace", Name: "Prime Catcher", Category: catalog.Trainer, Rarity: "ACE SPEC Rare"},
		{ID: "ace2", Name: "Other Ace", Category: catalog.Trainer, Rarity: "ACE SPEC Rare"},
		{ID: "en", Name: "Fire Energy", Category: catalog.Energy, EnergyType: "Normal"},
	}
	cards[1].HP = 60
	tags := fakeTags{
		"p1":   {prov("draw", "")}, // more tags than p1b, so it is preferred
		"p3":   {need("energy-attached", "Fire"), need("evolution-accel", ""), need("search-pokemon", "")},
		"e1":   {prov("draw", "")},
		"lead": {prov("energy-accel-hand", "Fire")},
		"t1":   {prov("search-pokemon", "")},
		"t2":   {prov("evolution-accel", "")},
		"t3":   {prov("draw", "")},
		"t4":   {prov("energy-accel-deck", "Fire")},
		"t5":   {prov("energy-accel-deck", "Water")},
		"ace":  {prov("gust", "")},
	}
	return cards, tags
}

func testConfig() *Config {
	cfg := &Config{
		Lines:       map[string][]int{"Basic": {4}, "Stage1": {4, 3}, "Stage2": {4, 2, 3}},
		EngineLines: map[string][]int{"Basic": {1}},
		Engines:     []string{"Missing Engine", "Helper"},
		Roles: []RoleSpec{
			{Role: "pokemon-search", Count: 4, Staples: []string{"Ultra Ball"}},
			{Role: "evolution-accel", Count: 4, When: "stage2", Staples: []string{"Rare Candy"}},
			{Role: "draw", Count: 8, Staples: []string{"Research", "Gone Supporter"}},
			{Role: "energy-accel", Count: 4, When: "needs:energy-attached"},
			{Role: "ace-spec", Count: 1, Staples: []string{"Prime Catcher", "Other Ace"}},
			{Role: "stadium", Count: 2, When: "needs:bench-size"},
		},
	}
	cfg.Energy.Min, cfg.Energy.Max = 7, 10
	return cfg
}

func TestBuild(t *testing.T) {
	pool, tags := testPool()
	res, err := Build(pool[3], pool, tags, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	qty := map[string]int{}
	for _, p := range res.Picks {
		qty[p.Card.ID] += p.Qty
		if p.Reason == "" {
			t.Errorf("%s has no reason", p.Card.Name)
		}
	}
	want := map[string]int{
		"p1": 4, "p2": 2, "p3": 3, // 4-2-3 line, the better Charmander
		"e1":  1, // engine, after skipping the missing one
		"t1":  4, // staple
		"t2":  4, // Stage 2, so Rare Candy
		"t4":  4, // Fire acceleration meets the Fire need (topped up); Water does not
		"ace": 1, // one ACE SPEC only
	}
	for id, n := range want {
		if qty[id] != n {
			t.Errorf("%s: %d copies, want %d", id, qty[id], n)
		}
	}
	for _, id := range []string{"p1b", "t5", "ace2", "lead"} {
		if qty[id] != 0 {
			t.Errorf("%s should not be in the deck", id)
		}
	}
	if qty["t3"] != 4 {
		t.Errorf("Research: %d copies, want 4 (staple, then top-ups)", qty["t3"])
	}
	if res.Total() != 60 {
		t.Errorf("deck has %d cards, want 60", res.Total())
	}
	// The tiny skeleton can't reach 60, so extra Energy fills the gap.
	if e := qty["en"]; e <= 10 {
		t.Errorf("Fire Energy = %d, want more than the max of 10", e)
	}

	// The starter's needs, and the Pokémon lead that meets one.
	if len(res.Needs) != 3 || res.Needs[0].From != "Charizard ex" {
		t.Errorf("needs = %+v", res.Needs)
	}
	if len(res.Leads) != 1 || res.Leads[0].Card.ID != "lead" || !strings.Contains(res.Leads[0].Reasons[0], "energy-accel-hand (Fire)") {
		t.Errorf("leads = %+v", res.Leads)
	}
	notes := strings.Join(res.Notes, "\n")
	for _, w := range []string{"engine Missing Engine is not in the pool", "staple Gone Supporter (draw) is not in the pool", "extra Fire Energy"} {
		if !strings.Contains(notes, w) {
			t.Errorf("notes should mention %q:\n%s", w, notes)
		}
	}
}

func TestBuildRejectsTrainerStarter(t *testing.T) {
	pool, tags := testPool()
	if _, err := Build(pool[6], pool, tags, testConfig()); err == nil {
		t.Error("a Trainer starter should be rejected in v1")
	}
}

func TestBasicStarterSkipsStage2Roles(t *testing.T) {
	pool, tags := testPool()
	res, err := Build(pool[5], pool, tags, testConfig())
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range res.Picks {
		if p.Card.ID == "t2" {
			t.Error("Rare Candy is only for Stage 2 lines")
		}
	}
	if res.Total() != 60 {
		t.Errorf("deck has %d cards, want 60", res.Total())
	}
}
