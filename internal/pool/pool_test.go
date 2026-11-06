package pool

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mxygem/deckstrat/internal/catalog"
	"github.com/mxygem/deckstrat/internal/catalog/catalogtest"
	"github.com/mxygem/deckstrat/internal/format"
)

type fakeLister map[string][]string // series or set ID -> children

func (l fakeLister) SeriesSets(_ context.Context, s string) ([]string, error) { return l[s], nil }
func (l fakeLister) SetCards(_ context.Context, s string) ([]string, error)   { return l[s], nil }

func TestBuild(t *testing.T) {
	ball := func(id, set, number, released string) catalog.Card {
		return catalog.Card{ID: id, Name: "Ultra Ball", Category: catalog.Trainer, TrainerType: "Item",
			Text: "Search.", SetID: set, Number: number, RegulationMark: "I", Series: "me", ReleaseDate: released}
	}
	cards := []catalog.Card{
		ball("me01-131", "me01", "131", "2025-09-26"),
		ball("me01-186", "me01", "186", "2025-09-26"), // full art in the same set
		ball("sv01-196", "sv01", "196", "2023-03-31"),
		{ID: "sv01-181", Name: "Nest Ball", Category: catalog.Trainer, SetID: "sv01", Number: "181", RegulationMark: "G", Series: "sv", ReleaseDate: "2023-03-31"},
		{ID: "me09-001", Name: "Future Card", Category: catalog.Trainer, SetID: "me09", Number: "1", RegulationMark: "J", Series: "me", ReleaseDate: "2026-11-01"},
	}
	cards[2].RegulationMark = "G" // an older printing, legal only as a reprint
	l := fakeLister{
		"sv": {"sv01"}, "me": {"me01", "me09"},
		"sv01": {"sv01-196", "sv01-181", "sv01-404"}, "me01": {"me01-131", "me01-186"}, "me09": {"me09-001"},
	}
	std, _ := format.Get("standard")
	var progress int
	p, err := Build(context.Background(), l, &catalogtest.Fake{Cards: cards}, std, Options{
		AsOf:     time.Date(2026, 11, 5, 0, 0, 0, 0, time.UTC),
		Progress: func(done, total int) { progress = total },
	})
	if err != nil {
		t.Fatal(err)
	}
	if progress != 6 {
		t.Errorf("progress total = %d, want 6", progress)
	}
	// Nest Ball is G, the future card is not legal until 2026-11-15, the
	// missing ID is skipped, and the three Ultra Balls are one card.
	if len(p.Cards) != 1 || p.Cards[0].ID != "me01-131" {
		var ids []string
		for _, c := range p.Cards {
			ids = append(ids, c.ID)
		}
		t.Fatalf("pool = %v, want [me01-131]", ids)
	}
	if p.Format != "standard" || p.AsOf != "2026-11-05" {
		t.Errorf("pool header = %s %s", p.Format, p.AsOf)
	}

	path := filepath.Join(t.TempDir(), "standard.json")
	if err := p.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil || len(got.Cards) != 1 || got.Cards[0].ReleaseDate != "2025-09-26" {
		t.Errorf("round trip = %+v, %v", got, err)
	}
	if missing, err := Load(filepath.Join(t.TempDir(), "none.json")); missing != nil || err != nil {
		t.Errorf("missing pool should be nil, nil: %v %v", missing, err)
	}
}
