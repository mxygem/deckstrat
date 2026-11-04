package deck

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/mxygem/deckstrat/internal/catalog"
	"github.com/mxygem/deckstrat/internal/catalog/catalogtest"
)

const tcgLiveList = `Pokémon: 3
2 Riolu MEG 76
1 Mega Lucario ex MEG 77

Trainer: 5
4 Arven SVI 166
1 Prime Catcher TEF 157

Energy: 4
4 Basic {F} Energy SVE 6

Total Cards: 12
`

func TestParseTCGLive(t *testing.T) {
	d, bad, err := ParseTCGLive(strings.NewReader(tcgLiveList))
	if err != nil {
		t.Fatal(err)
	}
	if len(bad) != 0 {
		t.Fatalf("unparsed: %+v", bad)
	}
	want := []Line{
		{Qty: 2, Name: "Riolu", SetCode: "MEG", Number: "76", Section: "Pokémon", LineNo: 2},
		{Qty: 1, Name: "Mega Lucario ex", SetCode: "MEG", Number: "77", Section: "Pokémon", LineNo: 3},
		{Qty: 4, Name: "Arven", SetCode: "SVI", Number: "166", Section: "Trainer", LineNo: 6},
		{Qty: 1, Name: "Prime Catcher", SetCode: "TEF", Number: "157", Section: "Trainer", LineNo: 7},
		{Qty: 4, Name: "Basic {F} Energy", SetCode: "SVE", Number: "6", Section: "Energy", LineNo: 10},
	}
	if !reflect.DeepEqual(d.Lines, want) {
		t.Errorf("lines:\n got %+v\nwant %+v", d.Lines, want)
	}
	if d.Count() != 12 {
		t.Errorf("count = %d", d.Count())
	}
}

func TestParseVariants(t *testing.T) {
	in := `##Pokémon - 1
* 1 Pikachu with Grey Felt Hat PR-SV 85
##Trainer - 1
* 2 Arven
Energy - 1
1 Psychic Energy Energy 5
this is not a card
0 Iono PAL 185
`
	d, bad, err := ParseTCGLive(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	want := []Line{
		{Qty: 1, Name: "Pikachu with Grey Felt Hat", SetCode: "PR-SV", Number: "85", Section: "Pokémon", LineNo: 2},
		{Qty: 2, Name: "Arven", Section: "Trainer", LineNo: 4},
		// Old PTCGO energy lines have no usable set code, so the whole rest is the name.
		{Qty: 1, Name: "Psychic Energy Energy 5", Section: "Energy", LineNo: 6},
	}
	if !reflect.DeepEqual(d.Lines, want) {
		t.Errorf("lines:\n got %+v\nwant %+v", d.Lines, want)
	}
	if len(bad) != 2 || bad[0].LineNo != 7 || bad[1].LineNo != 8 {
		t.Errorf("unparsed = %+v", bad)
	}
}

func TestCatalogName(t *testing.T) {
	for in, want := range map[string]string{
		"Basic {R} Energy":     "Fire Energy",
		"Basic Psychic Energy": "Psychic Energy",
		"Basic {X} Energy":     "Basic {X} Energy",
		"Arven":                "Arven",
	} {
		if got := CatalogName(in); got != want {
			t.Errorf("CatalogName(%q) = %q, want %q", in, got, want)
		}
	}
}

var cards = []catalog.Card{
	{ID: "me01-076", Name: "Riolu", Category: catalog.Pokemon, Stage: "Basic", SetCode: "MEG", Number: "076", RegulationMark: "I"},
	{ID: "me01-077", Name: "Mega Lucario ex", Category: catalog.Pokemon, Stage: "Stage1", Suffix: "ex", SetCode: "MEG", Number: "077", RegulationMark: "I"},
	{ID: "sv01-166", Name: "Arven", Category: catalog.Trainer, TrainerType: "Supporter", SetCode: "SVI", Number: "166", RegulationMark: "G"},
	{ID: "sv04.5-235", Name: "Arven", Category: catalog.Trainer, TrainerType: "Supporter", SetCode: "PAF", Number: "235", RegulationMark: "H"},
	{ID: "sv05-157", Name: "Prime Catcher", Category: catalog.Trainer, Rarity: "ACE SPEC Rare", SetCode: "TEF", Number: "157", RegulationMark: "H"},
	{ID: "sve-006", Name: "Fighting Energy", Category: catalog.Energy, EnergyType: "Normal", SetCode: "SVE", Number: "006"},
}

func TestResolveAndExportRoundTrip(t *testing.T) {
	ctx := context.Background()
	cat := &catalogtest.Fake{Cards: cards}
	d, _, err := ParseTCGLive(strings.NewReader(tcgLiveList))
	if err != nil {
		t.Fatal(err)
	}
	entries, missing, err := Resolve(ctx, cat, d)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 0 {
		t.Fatalf("missing: %+v", missing)
	}
	if entries[4].Card.ID != "sve-006" {
		t.Errorf("energy resolved to %s", entries[4].Card.ID)
	}

	out := ExportTCGLive(entries)
	want := `Pokémon: 3
2 Riolu MEG 76
1 Mega Lucario ex MEG 77

Trainer: 5
4 Arven SVI 166
1 Prime Catcher TEF 157

Energy: 4
4 Fighting Energy SVE 6

Total Cards: 12
`
	if out != want {
		t.Errorf("export:\n%s\nwant:\n%s", out, want)
	}

	// Re-importing the export resolves to the same printings.
	d2, bad, err := ParseTCGLive(strings.NewReader(out))
	if err != nil || len(bad) != 0 {
		t.Fatalf("reparse: %v %+v", err, bad)
	}
	entries2, missing2, err := Resolve(ctx, cat, d2)
	if err != nil || len(missing2) != 0 {
		t.Fatalf("re-resolve: %v %+v", err, missing2)
	}
	for i := range entries {
		if entries[i].Card.ID != entries2[i].Card.ID || entries[i].Line.Qty != entries2[i].Line.Qty {
			t.Errorf("entry %d: %s x%d != %s x%d", i, entries[i].Card.ID, entries[i].Line.Qty, entries2[i].Card.ID, entries2[i].Line.Qty)
		}
	}
}

func TestExportMergesDuplicatePrintings(t *testing.T) {
	c := &cards[2]
	out := ExportTCGLive([]Entry{{Line: Line{Qty: 2}, Card: c}, {Line: Line{Qty: 1}, Card: c}})
	if !strings.Contains(out, "Trainer: 3\n3 Arven SVI 166\n") {
		t.Errorf("export:\n%s", out)
	}
}

func TestResolveUnmatched(t *testing.T) {
	cat := &catalogtest.Fake{Cards: cards}
	d := Deck{Lines: []Line{
		{Qty: 1, Name: "Arven", LineNo: 1},                               // name only -> newest mark (H)
		{Qty: 1, Name: "Iono", SetCode: "PAL", Number: "185", LineNo: 2}, // not in catalog
		{Qty: 1, Name: "Iono", SetCode: "SVI", Number: "166", LineNo: 3}, // wrong name for printing
		{Qty: 1, Name: "Nonexistent", LineNo: 4},                         // no such name
	}}
	entries, missing, err := Resolve(context.Background(), cat, d)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Card.ID != "sv04.5-235" {
		t.Errorf("entries = %+v", entries)
	}
	if len(missing) != 3 {
		t.Fatalf("missing = %+v", missing)
	}
	if !strings.Contains(missing[1].Reason, `is "Arven", not "Iono"`) {
		t.Errorf("reason = %q", missing[1].Reason)
	}
}

type failingCatalog struct{ catalogtest.Fake }

func (failingCatalog) GetByCode(context.Context, string, string) (*catalog.Card, error) {
	return nil, errors.New("network down")
}

func TestResolveCatalogErrorAborts(t *testing.T) {
	d := Deck{Lines: []Line{{Qty: 1, Name: "Arven", SetCode: "SVI", Number: "166"}}}
	if _, _, err := Resolve(context.Background(), &failingCatalog{}, d); err == nil {
		t.Fatal("want error")
	}
}

func TestNamesAgree(t *testing.T) {
	if !namesAgree("Professor's Research", "Professor's Research (Professor Turo)") {
		t.Error("catalog suffix should be tolerated")
	}
	if namesAgree("Iono", "Arven") {
		t.Error("different names agreed")
	}
}
