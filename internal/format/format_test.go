package format

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mxygem/deckstrat/internal/catalog"
	"github.com/mxygem/deckstrat/internal/catalog/catalogtest"
	"github.com/mxygem/deckstrat/internal/deck"
)

func TestRegistry(t *testing.T) {
	for _, id := range []string{"standard", "expanded", "glc"} {
		f, err := Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if f.Rules.Size != 60 || f.Rules.CopyLimit == 0 || f.Name == "" || len(f.Sources) == 0 {
			t.Errorf("%s looks incomplete: %+v", id, f)
		}
	}
	if _, err := Get("STANDARD"); err != nil {
		t.Error("IDs should be case-insensitive")
	}
	if _, err := Get("nope"); err == nil || !strings.Contains(err.Error(), "standard") {
		t.Errorf("unknown format error should list formats: %v", err)
	}
}

// Test cards. Marks and series follow the real printings.
var (
	riolu      = catalog.Card{ID: "me01-076", Name: "Riolu", Category: catalog.Pokemon, Stage: "Basic", Types: []string{"Fighting"}, SetCode: "MEG", Number: "076", RegulationMark: "I", Series: "me"}
	megaLuc    = catalog.Card{ID: "me01-077", Name: "Mega Lucario ex", Category: catalog.Pokemon, Stage: "Stage1", Suffix: "ex", Types: []string{"Fighting"}, SetCode: "MEG", Number: "077", RegulationMark: "I", Series: "me"}
	charizard  = catalog.Card{ID: "sv03-125", Name: "Charizard ex", Category: catalog.Pokemon, Stage: "Stage2", Suffix: "ex", Types: []string{"Darkness"}, SetCode: "OBF", Number: "125", RegulationMark: "G", Series: "sv"}
	pikachu    = catalog.Card{ID: "x-1", Name: "Pikachu", Category: catalog.Pokemon, Stage: "Basic", Types: []string{"Lightning"}, SetCode: "X", Number: "1", RegulationMark: "H", Series: "sv"}
	arvenG     = catalog.Card{ID: "sv01-166", Name: "Arven", Category: catalog.Trainer, TrainerType: "Supporter", Text: "Search your deck for an Item card and a Pokémon Tool card.", SetCode: "SVI", Number: "166", RegulationMark: "G", Series: "sv"}
	arvenH     = catalog.Card{ID: "sv04.5-235", Name: "Arven", Category: catalog.Trainer, TrainerType: "Supporter", Text: "Search your deck for an Item card and a Pokémon Tool card.", SetCode: "PAF", Number: "235", RegulationMark: "H", Series: "sv"}
	catcherTEF = catalog.Card{ID: "sv05-157", Name: "Prime Catcher", Category: catalog.Trainer, Rarity: "ACE SPEC Rare", Text: "Switch.", SetCode: "TEF", Number: "157", RegulationMark: "H", Series: "sv"}
	maxBelt    = catalog.Card{ID: "sv05-154", Name: "Maximum Belt", Category: catalog.Trainer, Rarity: "ACE SPEC Rare", SetCode: "TEF", Number: "154", RegulationMark: "H", Series: "sv"}
	nest       = catalog.Card{ID: "sv01-181", Name: "Nest Ball", Category: catalog.Trainer, Text: "Bench a Basic.", SetCode: "SVI", Number: "181", RegulationMark: "G", Series: "sv"}
	pokegear   = catalog.Card{ID: "dp-1", Name: "Pokégear", Category: catalog.Trainer, SetCode: "DP", Number: "1", Series: "dp"}
	fighting   = catalog.Card{ID: "sve-006", Name: "Fighting Energy", Category: catalog.Energy, EnergyType: "Normal", SetCode: "SVE", Number: "006", Series: "sv"}
)

func entry(qty int, c catalog.Card) deck.Entry {
	return deck.Entry{Line: deck.Line{Qty: qty}, Card: &c}
}

// base returns a legal 60-card Standard deck to mutate.
func base() []deck.Entry {
	return []deck.Entry{
		entry(4, riolu), entry(3, megaLuc), entry(4, arvenH), entry(1, catcherTEF), entry(48, fighting),
	}
}

func rules(vs []Violation) []string {
	var out []string
	for _, v := range vs {
		out = append(out, v.Rule)
	}
	return out
}

func TestValidateStandard(t *testing.T) {
	std, _ := Get("standard")
	cat := &catalogtest.Fake{Cards: []catalog.Card{arvenG, arvenH, nest, charizard}}

	tests := []struct {
		name      string
		mutate    func([]deck.Entry) []deck.Entry
		wantRules []string
		wantMsg   string
		wantNote  string
	}{
		{name: "legal", mutate: func(e []deck.Entry) []deck.Entry { return e }},
		{
			name:      "59 cards",
			mutate:    func(e []deck.Entry) []deck.Entry { e[4].Line.Qty = 47; return e },
			wantRules: []string{"deck-size"},
			wantMsg:   "deck has 59 cards; Standard requires exactly 60",
		},
		{
			name: "5 copies across printings",
			mutate: func(e []deck.Entry) []deck.Entry {
				e[4].Line.Qty = 47
				return append(e, entry(1, arvenG))
			},
			wantRules: []string{"copy-limit"},
			wantMsg:   "5 copies of Arven (4 × Arven (PAF 235), 1 × Arven (SVI 166)); the limit is 4 per name",
			wantNote:  "Arven (SVI 166) has regulation mark G; Standard allows H, I, J, but is legal through its reprint Arven (PAF 235)",
		},
		{
			name: "two ACE SPECs",
			mutate: func(e []deck.Entry) []deck.Entry {
				e[4].Line.Qty = 47
				return append(e, entry(1, maxBelt))
			},
			wantRules: []string{"ace-spec"},
			wantMsg:   "deck has 2 ACE SPEC cards (1 × Prime Catcher, 1 × Maximum Belt); Standard allows 1",
		},
		{
			name: "rotated card without reprint",
			mutate: func(e []deck.Entry) []deck.Entry {
				e[4].Line.Qty = 47
				return append(e, entry(1, charizard))
			},
			wantRules: []string{"pool"},
			wantMsg:   "Charizard ex (OBF 125) has regulation mark G; Standard allows H, I, J and no legal reprint was found",
		},
		{
			name: "no basics",
			mutate: func(e []deck.Entry) []deck.Entry {
				e[0].Line.Qty = 0
				e[4].Line.Qty = 52
				return e
			},
			wantRules: []string{"basic-pokemon"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := &Validator{Format: std, Reprints: CatalogPrintings(cat)}
			res, err := v.Validate(context.Background(), tt.mutate(base()))
			if err != nil {
				t.Fatal(err)
			}
			if got := rules(res.Violations); strings.Join(got, ",") != strings.Join(tt.wantRules, ",") {
				t.Fatalf("rules = %v, want %v\n%+v", got, tt.wantRules, res.Violations)
			}
			if res.Legal() != (len(tt.wantRules) == 0) {
				t.Error("Legal() disagrees with violations")
			}
			if tt.wantMsg != "" && res.Violations[0].Message != tt.wantMsg {
				t.Errorf("message = %q\nwant      %q", res.Violations[0].Message, tt.wantMsg)
			}
			if tt.wantNote != "" && (len(res.Notes) != 1 || res.Notes[0].Message != tt.wantNote) {
				t.Errorf("notes = %+v\nwant %q", res.Notes, tt.wantNote)
			}
		})
	}
}

func TestBasicEnergyExempt(t *testing.T) {
	std, _ := Get("standard")
	oldEnergy := fighting
	oldEnergy.Series, oldEnergy.RegulationMark = "base", ""
	e := base()
	e[4] = entry(48, oldEnergy)
	res, err := (&Validator{Format: std}).Validate(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Legal() {
		t.Errorf("basic energy should be exempt from copy and pool rules: %+v", res.Violations)
	}
}

func TestValidateExpandedSeries(t *testing.T) {
	exp, _ := Get("expanded")
	e := base()
	e[4].Line.Qty = 47
	e = append(e, entry(1, pokegear))
	res, err := (&Validator{Format: exp}).Validate(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	if got := rules(res.Violations); len(got) != 1 || got[0] != "pool" || !strings.Contains(res.Violations[0].Message, `is from series "dp"`) {
		t.Errorf("violations = %+v", res.Violations)
	}
}

func TestValidateGLC(t *testing.T) {
	glc, _ := Get("glc")
	e := []deck.Entry{
		entry(2, riolu), entry(1, megaLuc), entry(1, pikachu), entry(1, catcherTEF), entry(1, nest), entry(54, fighting),
	}
	res, err := (&Validator{Format: glc}).Validate(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(rules(res.Violations), ",")
	if got != "copy-limit,ace-spec,rule-box,single-type" {
		t.Fatalf("rules = %s\n%+v", got, res.Violations)
	}
	want := []string{
		"2 copies of Riolu; Gym Leader Challenge is singleton except Basic Energy",
		"Gym Leader Challenge allows no ACE SPEC cards; deck has 1 × Prime Catcher",
		"Mega Lucario ex (MEG 77) is a Rule Box Pokémon; Gym Leader Challenge bans them",
		"Pokémon types Fighting, Lightning have no type in common; Gym Leader Challenge requires every Pokémon to share one type",
	}
	for i, w := range want {
		if res.Violations[i].Message != w {
			t.Errorf("message %d = %q\nwant        %q", i, res.Violations[i].Message, w)
		}
	}
}

func TestBanList(t *testing.T) {
	f := &Format{Name: "Test", Rules: Rules{Size: 60, CopyLimit: 4, MaxAceSpec: 1},
		BanList: []Ban{{Name: "Arven", Effective: "2026-06-01"}}}
	v := &Validator{Format: f, AsOf: time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)}
	res, _ := v.Validate(context.Background(), base())
	if !res.Legal() {
		t.Errorf("ban not yet effective: %+v", res.Violations)
	}
	v.AsOf = time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	res, _ = v.Validate(context.Background(), base())
	if got := rules(res.Violations); len(got) != 1 || got[0] != "banned" {
		t.Errorf("violations = %+v", res.Violations)
	}
}

func TestCopyLimitUsesRuleName(t *testing.T) {
	std, _ := Get("standard")
	boss := catalog.Card{ID: "a-1", Name: "Boss's Orders (Ghetsis)", Category: catalog.Trainer, SetCode: "PAL", Number: "172", RegulationMark: "H"}
	boss2 := boss
	boss2.ID, boss2.Name, boss2.SetCode, boss2.Number = "b-1", "Boss's Orders", "MEG", "114"
	e := base()
	e[4].Line.Qty = 43
	e = append(e, entry(3, boss), entry(2, boss2))
	res, err := (&Validator{Format: std}).Validate(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	want := "5 copies of Boss's Orders (3 × Boss's Orders (Ghetsis) (PAL 172), 2 × Boss's Orders (MEG 114)); the limit is 4 per name"
	if len(res.Violations) != 1 || res.Violations[0].Message != want {
		t.Errorf("violations = %+v", res.Violations)
	}

	// Pokémon-EX and Pokémon ex are different names.
	exOld := catalog.Card{ID: "c-1", Name: "Charizard EX", Category: catalog.Pokemon, Stage: "Basic", Suffix: "EX", Series: "xy"}
	exNew := catalog.Card{ID: "c-2", Name: "Charizard ex", Category: catalog.Pokemon, Stage: "Stage2", Suffix: "ex", Series: "sv"}
	exp, _ := Get("expanded")
	e = base()
	e[4].Line.Qty = 42
	e = append(e, entry(3, exOld), entry(3, exNew))
	res, _ = (&Validator{Format: exp}).Validate(context.Background(), e)
	if !res.Legal() {
		t.Errorf("EX and ex should count separately: %+v", res.Violations)
	}
}

func TestRadiantAndPrismStar(t *testing.T) {
	exp, _ := Get("expanded")
	radA := catalog.Card{ID: "r-1", Name: "Radiant Greninja", Category: catalog.Pokemon, Stage: "Basic", Series: "swsh"}
	radB := catalog.Card{ID: "r-2", Name: "Radiant Charizard", Category: catalog.Pokemon, Stage: "Basic", Series: "swsh"}
	lys := catalog.Card{ID: "p-1", Name: "Lysandre ◇", Category: catalog.Trainer, Series: "sm"}
	e := base()
	e[4].Line.Qty = 44
	e = append(e, entry(1, radA), entry(1, radB), entry(2, lys))
	res, err := (&Validator{Format: exp}).Validate(context.Background(), e)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(rules(res.Violations), ","); got != "radiant,prism-star" {
		t.Fatalf("rules = %s: %+v", got, res.Violations)
	}
	if res.Violations[0].Message != "deck has 2 Radiant Pokémon (1 × Radiant Greninja, 1 × Radiant Charizard); the limit is 1" {
		t.Errorf("message = %q", res.Violations[0].Message)
	}
}

func TestLegacyAceSpecCounts(t *testing.T) {
	exp, _ := Get("expanded")
	cs := catalog.Card{ID: "bw7-137", Name: "Computer Search", Category: catalog.Trainer, Rarity: "Rare", AceSpec: true, Series: "bw"}
	e := base()
	e[4].Line.Qty = 47
	e = append(e, entry(1, cs))
	res, _ := (&Validator{Format: exp}).Validate(context.Background(), e)
	if got := rules(res.Violations); len(got) != 1 || got[0] != "ace-spec" {
		t.Errorf("violations = %+v", res.Violations)
	}
}
