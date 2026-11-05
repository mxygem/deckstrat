package catalog

import "testing"

func TestCardClassification(t *testing.T) {
	tests := []struct {
		name                                 string
		card                                 Card
		basicEnergy, aceSpec, ruleBox, basic bool
	}{
		{name: "basic energy", card: Card{Category: Energy, EnergyType: "Normal"}, basicEnergy: true},
		{name: "special energy", card: Card{Category: Energy, EnergyType: "Special"}},
		{name: "ace spec", card: Card{Category: Trainer, Rarity: "ACE SPEC Rare"}, aceSpec: true},
		{name: "pokemon ex", card: Card{Category: Pokemon, Stage: "Stage2", Suffix: "ex"}, ruleBox: true},
		{name: "mega ex", card: Card{Name: "Mega Lucario ex", Category: Pokemon, Stage: "Stage1", Suffix: "ex"}, ruleBox: true},
		{name: "vstar", card: Card{Category: Pokemon, Stage: "VSTAR"}, ruleBox: true},
		{name: "radiant", card: Card{Name: "Radiant Charizard", Category: Pokemon, Stage: "Basic"}, ruleBox: true, basic: true},
		{name: "plain basic", card: Card{Name: "Riolu", Category: Pokemon, Stage: "Basic"}, basic: true},
		{name: "bw ace spec flag", card: Card{Category: Trainer, Rarity: "Rare", AceSpec: true}, aceSpec: true},
		{name: "prism star", card: Card{Name: "Volcanion ◇", Category: Pokemon, Stage: "Basic"}, ruleBox: true, basic: true},
		{name: "trainer named ex-ish", card: Card{Name: "Exp. Share", Category: Trainer, Suffix: "ex"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := tt.card
			if got := c.IsBasicEnergy(); got != tt.basicEnergy {
				t.Errorf("IsBasicEnergy = %v", got)
			}
			if got := c.IsAceSpec(); got != tt.aceSpec {
				t.Errorf("IsAceSpec = %v", got)
			}
			if got := c.IsRuleBox(); got != tt.ruleBox {
				t.Errorf("IsRuleBox = %v", got)
			}
			if got := c.IsBasicPokemon(); got != tt.basic {
				t.Errorf("IsBasicPokemon = %v", got)
			}
		})
	}
}

func TestFunctionalKey(t *testing.T) {
	a := Card{ID: "sv05-157", Name: "Prime Catcher", Category: Trainer, TrainerType: "Item", Rarity: "ACE SPEC Rare", Text: "Switch in 1 of your opponent's Benched Pokémon."}
	b := a
	b.ID, b.Rarity, b.RegulationMark = "sv08.5-119", "ACE SPEC Rare", "H"
	b.Text = "Switch in 1 of your opponent’s Benched Pokemon." // curly quote, no accent
	if a.FunctionalKey() != b.FunctionalKey() {
		t.Errorf("reprint keys differ:\n%s\n%s", a.FunctionalKey(), b.FunctionalKey())
	}
	c := a
	c.Text = "Switch in 1 of your opponent's Benched Pokemon. (Reworded.)"
	if a.FunctionalKey() != c.FunctionalKey() {
		t.Error("reworded reprints should share a key")
	}
	d := a
	d.Name = "Computer Search"
	if a.FunctionalKey() == d.FunctionalKey() {
		t.Error("different names should give different keys")
	}

	// Base Set Computer Search is not the BW ACE SPEC.
	oldCS := Card{Name: "Computer Search", Category: Trainer}
	aceCS := Card{Name: "Computer Search", Category: Trainer, TrainerType: "Item", AceSpec: true}
	if oldCS.FunctionalKey() == aceCS.FunctionalKey() {
		t.Error("ACE SPEC and non-ACE SPEC versions should differ")
	}

	p1 := Card{Name: "Pikachu", Category: Pokemon, HP: 60, Types: []string{"Lightning"}, Attacks: []Attack{{Name: "Thunder Shock", Cost: []string{"Lightning"}, Damage: "20"}}}
	p2 := p1
	p2.Attacks = []Attack{{Name: "Gnaw", Cost: []string{"Colorless"}, Damage: "10"}}
	if p1.FunctionalKey() == p2.FunctionalKey() {
		t.Error("Pokémon with different attacks should differ")
	}
}

func TestSameName(t *testing.T) {
	if !SameName("Pokégear 3.0", "Pokegear 3.0") {
		t.Error("accent should not matter")
	}
	if SameName("Arven", "Iono") {
		t.Error("different names matched")
	}
}

func TestRuleName(t *testing.T) {
	for in, want := range map[string]string{
		"Boss's Orders (Giovanni)":              "Boss's Orders",
		"Professor's Research (Professor Turo)": "Professor's Research",
		"Charizard ex":                          "Charizard ex",
		"Charizard EX":                          "Charizard EX",
		"(odd)":                                 "(odd)",
	} {
		if got := RuleName(in); got != want {
			t.Errorf("RuleName(%q) = %q, want %q", in, got, want)
		}
	}
	a := Card{Name: "Boss's Orders (Giovanni)", Category: Trainer, Text: "Switch in 1 of your opponent's Benched Pokémon."}
	b := a
	b.Name = "Boss's Orders (Ghetsis)"
	if a.FunctionalKey() != b.FunctionalKey() {
		t.Error("subtitled versions should share a functional key")
	}
}

func TestRadiantPrismStar(t *testing.T) {
	r := Card{Name: "Radiant Greninja", Category: Pokemon}
	p := Card{Name: "Lysandre ◇", Category: Trainer}
	if !r.IsRadiant() || r.IsPrismStar() || p.IsRadiant() || !p.IsPrismStar() {
		t.Error("misclassified")
	}
	if SeriesRank("me") <= SeriesRank("sv") || SeriesRank("bw") <= SeriesRank("base") || SeriesRank("tcgp") != 0 {
		t.Error("series rank order")
	}
}

func TestNamesAgree(t *testing.T) {
	if !NamesAgree("Professor's Research", "Professor's Research (Professor Turo)") {
		t.Error("catalog suffix should be tolerated")
	}
	if !NamesAgree("Pikachu & Zekrom-GX", "Pikachu & Zekrom GX") {
		t.Error("punctuation should not matter")
	}
	if NamesAgree("Iono", "Arven") {
		t.Error("different names agreed")
	}
}
