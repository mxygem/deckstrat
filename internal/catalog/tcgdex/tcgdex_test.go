package tcgdex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mxygem/deckstrat/internal/catalog"
)

// fakeServer serves recorded TCGdex responses from testdata.
func fakeServer(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
		switch {
		case len(parts) == 1 && parts[0] == "cards" && r.URL.Query().Has("set.id"):
			serveCardSearch(t, w, r)
		case len(parts) == 1 && parts[0] == "cards":
			serveFile(w, r, "search_charizard_ex.json")
		case len(parts) == 2 && parts[0] == "cards":
			serveFile(w, r, filepath.Join("cards", parts[1]+".json"))
		case len(parts) == 2 && parts[0] == "series":
			serveFile(w, r, filepath.Join("series", parts[1]+".json"))
		case len(parts) == 1 && parts[0] == "sets":
			serveSetsByCode(t, w, strings.TrimPrefix(r.URL.Query().Get("abbreviation.official"), "eq:"))
		case len(parts) == 2 && parts[0] == "sets":
			serveFile(w, r, filepath.Join("sets", parts[1]+".json"))
		case len(parts) == 3 && parts[0] == "sets":
			// Like the real API: plain numbers may be unpadded, but
			// prefixed numbers must match the localId exactly.
			n := parts[2]
			if _, err := strconv.Atoi(n); err == nil {
				n = fmt.Sprintf("%03s", n)
			}
			serveFile(w, r, filepath.Join("cards", parts[1]+"-"+n+".json"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &paths
}

func serveFile(w http.ResponseWriter, r *http.Request, name string) {
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(b)
}

// serveCardSearch filters the card fixtures like TCGdex does for
// name=eq:X&set.id=eq:Y: the name match ignores case.
func serveCardSearch(t *testing.T, w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(r.URL.Query().Get("name"), "eq:")
	setID := strings.TrimPrefix(r.URL.Query().Get("set.id"), "eq:")
	files, _ := filepath.Glob(filepath.Join("testdata", "cards", "*.json"))
	out := []apiBrief{}
	for _, f := range files {
		b, _ := os.ReadFile(f)
		var c apiCard
		if err := json.Unmarshal(b, &c); err != nil {
			t.Error(err)
		}
		if c.Set.ID == setID && strings.EqualFold(c.Name, name) {
			out = append(out, apiBrief{ID: c.ID, Name: c.Name, Image: c.Image})
		}
	}
	json.NewEncoder(w).Encode(out)
}

func serveSetsByCode(t *testing.T, w http.ResponseWriter, code string) {
	files, _ := filepath.Glob(filepath.Join("testdata", "sets", "*.json"))
	out := []apiBrief{}
	for _, f := range files {
		b, _ := os.ReadFile(f)
		var s apiSet
		if err := json.Unmarshal(b, &s); err != nil {
			t.Error(err)
		}
		if s.Abbreviation.Official == code {
			out = append(out, apiBrief{ID: s.ID, Name: s.Name})
		}
	}
	json.NewEncoder(w).Encode(out)
}

func newTestClient(t *testing.T) (*Client, *[]string) {
	srv, paths := fakeServer(t)
	return New(WithBaseURL(srv.URL)), paths
}

func TestGet(t *testing.T) {
	c, _ := newTestClient(t)
	ctx := context.Background()

	tests := []struct {
		id    string
		check func(t *testing.T, c *catalog.Card)
	}{
		{"sv03-125", func(t *testing.T, c *catalog.Card) {
			want := catalog.Card{Name: "Charizard ex", Category: catalog.Pokemon, Stage: "Stage2", Suffix: "ex", HP: 330, RegulationMark: "G", SetCode: "OBF", Series: "sv", Number: "125"}
			if c.Name != want.Name || c.Category != want.Category || c.Stage != want.Stage || c.Suffix != want.Suffix ||
				c.HP != want.HP || c.RegulationMark != want.RegulationMark || c.SetCode != want.SetCode || c.Series != want.Series || c.Number != want.Number {
				t.Errorf("got %+v", c)
			}
			if !c.IsRuleBox() || c.IsBasicPokemon() {
				t.Error("Charizard ex should be a non-basic Rule Box Pokémon")
			}
			if len(c.Attacks) != 1 || c.Attacks[0].Damage != "180+" {
				t.Errorf("string damage not decoded: %+v", c.Attacks)
			}
		}},
		{"me01-077", func(t *testing.T, c *catalog.Card) {
			if c.Attacks[1].Damage != "270" {
				t.Errorf("numeric damage = %q", c.Attacks[1].Damage)
			}
			if !c.IsRuleBox() || c.RegulationMark != "I" || c.SetCode != "MEG" || c.ReleaseDate != "2025-09-26" {
				t.Errorf("got %+v", c)
			}
		}},
		{"sv08.5-119", func(t *testing.T, c *catalog.Card) {
			if !c.IsAceSpec() || c.TrainerType != "Item" || c.Text == "" {
				t.Errorf("Prime Catcher: %+v", c)
			}
		}},
		{"sve-002", func(t *testing.T, c *catalog.Card) {
			if !c.IsBasicEnergy() || c.RegulationMark != "" {
				t.Errorf("Fire Energy: %+v", c)
			}
		}},
		{"me01-001", func(t *testing.T, c *catalog.Card) {
			if !c.IsBasicPokemon() || c.IsRuleBox() {
				t.Errorf("Bulbasaur: %+v", c)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			card, err := c.Get(ctx, tt.id)
			if err != nil {
				t.Fatal(err)
			}
			tt.check(t, card)
		})
	}
}

func TestReprintsShareFunctionalKey(t *testing.T) {
	c, _ := newTestClient(t)
	a, err := c.Get(context.Background(), "sv05-157")
	if err != nil {
		t.Fatal(err)
	}
	b, err := c.Get(context.Background(), "sv08.5-119")
	if err != nil {
		t.Fatal(err)
	}
	if a.FunctionalKey() != b.FunctionalKey() {
		t.Errorf("Prime Catcher printings differ:\n%s\n%s", a.FunctionalKey(), b.FunctionalKey())
	}
}

func TestGetNotFound(t *testing.T) {
	c, _ := newTestClient(t)
	_, err := c.Get(context.Background(), "zzz-999")
	if !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestGetByCode(t *testing.T) {
	c, paths := newTestClient(t)
	ctx := context.Background()
	card, err := c.GetByCode(ctx, "OBF", "125", "")
	if err != nil {
		t.Fatal(err)
	}
	if card.ID != "sv03-125" {
		t.Errorf("id = %s", card.ID)
	}
	if _, err := c.GetByCode(ctx, "sve", "2", ""); err != nil {
		t.Errorf("lowercase code, unpadded number: %v", err)
	}

	// The set code lookup is memoized.
	n := 0
	for _, p := range *paths {
		if strings.HasPrefix(p, "/sets?") {
			n++
		}
	}
	if _, err := c.GetByCode(ctx, "OBF", "125", ""); err != nil {
		t.Fatal(err)
	}
	m := 0
	for _, p := range *paths {
		if strings.HasPrefix(p, "/sets?") {
			m++
		}
	}
	if m != n {
		t.Errorf("set code looked up again: %d -> %d", n, m)
	}

	if _, err := c.GetByCode(ctx, "NOPE", "1", ""); !errors.Is(err, catalog.ErrNotFound) {
		t.Errorf("unknown code err = %v", err)
	}
}

func TestSearchDropsPocketCards(t *testing.T) {
	c, paths := newTestClient(t)
	refs, err := c.Search(context.Background(), catalog.Query{Name: "Charizard ex", Exact: true, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) == 0 || len(refs) > 3 {
		t.Fatalf("refs = %+v", refs)
	}
	for _, r := range refs {
		if strings.HasPrefix(r.ID, "A") {
			t.Errorf("Pocket card in results: %+v", r)
		}
	}
	if last := (*paths)[len(*paths)-1]; !strings.Contains(last, "name=eq%3ACharizard+ex") {
		t.Errorf("query = %s", last)
	}
}

func TestAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer srv.Close()
	_, err := New(WithBaseURL(srv.URL)).Get(context.Background(), "x")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadGateway {
		t.Fatalf("err = %v", err)
	}
}

func TestGetByCodeSubsetsAndPromos(t *testing.T) {
	c, _ := newTestClient(t)
	ctx := context.Background()
	tests := []struct {
		code, number, wantID, wantCode string
	}{
		{"BRS", "TG01", "swsh9tg-TG01", "BRS"},    // Trainer Gallery via "BRS:TG"
		{"SHF", "SV49", "swsh4.5sv-SV049", "SHF"}, // Shiny Vault, number needs padding
		{"HIF", "SV49", "sma-SV49", "HIF"},        // Hidden Fates Shiny Vault has no abbreviation
		{"PR-SV", "85", "svp-085", "PR-SV"},       // promo alias, round-trips to TCG Live's code
	}
	for _, tt := range tests {
		t.Run(tt.code+" "+tt.number, func(t *testing.T) {
			card, err := c.GetByCode(ctx, tt.code, tt.number, "")
			if err != nil {
				t.Fatal(err)
			}
			if card.ID != tt.wantID || card.SetCode != tt.wantCode {
				t.Errorf("got %s code %q, want %s code %q", card.ID, card.SetCode, tt.wantID, tt.wantCode)
			}
		})
	}
}

func TestLegacyCards(t *testing.T) {
	c, _ := newTestClient(t)
	ctx := context.Background()
	cs, err := c.Get(ctx, "bw7-137")
	if err != nil {
		t.Fatal(err)
	}
	if !cs.IsAceSpec() || cs.Rarity != "Rare" {
		t.Errorf("Computer Search: ace spec %v rarity %q", cs.IsAceSpec(), cs.Rarity)
	}
	tag, err := c.Get(ctx, "sm9-33")
	if err != nil {
		t.Fatal(err)
	}
	if !tag.IsBasicPokemon() || !tag.IsRuleBox() {
		t.Errorf("Pikachu & Zekrom GX: stage %q", tag.Stage)
	}
}

func TestNumberCandidates(t *testing.T) {
	for in, want := range map[string]string{
		"SV49": "SV49,SV049", "TG01": "TG01,TG001", "5": "5", "SWSH1": "SWSH1,SWSH001", "GG44": "GG44,GG044",
	} {
		if got := strings.Join(numberCandidates(in), ","); got != want {
			t.Errorf("numberCandidates(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestGetByCodeUsesName(t *testing.T) {
	c, _ := newTestClient(t)
	ctx := context.Background()
	tests := []struct {
		code, number, name, wantID string
	}{
		// TCG Live numbers MEE energy 9-16; TCGdex numbers them 001-008.
		{"MEE", "13", "Psychic Energy", "mee-005"},
		// 30C is the code of two sets with overlapping numbers.
		{"30C", "66", "Mew ex", "30th-066"},
		{"30C", "11", "Crobat G", "30th-c-011"},
		{"30C", "11", "Moltres", "30th-011"},
	}
	for _, tt := range tests {
		t.Run(tt.code+" "+tt.number+" "+tt.name, func(t *testing.T) {
			card, err := c.GetByCode(ctx, tt.code, tt.number, tt.name)
			if err != nil {
				t.Fatal(err)
			}
			if card.ID != tt.wantID {
				t.Errorf("got %s, want %s", card.ID, tt.wantID)
			}
		})
	}

	// A wrong name with no match in the set returns the card at the
	// number, so the caller can report the mismatch.
	card, err := c.GetByCode(ctx, "OBF", "125", "Iono")
	if err != nil || card.ID != "sv03-125" {
		t.Errorf("got %v, %v", card, err)
	}
}

func TestListing(t *testing.T) {
	c, _ := newTestClient(t)
	ctx := context.Background()
	sets, err := c.SeriesSets(ctx, "sv")
	if err != nil || !slices.Equal(sets, []string{"sv05", "sve"}) {
		t.Fatalf("SeriesSets = %v, %v", sets, err)
	}
	cards, err := c.SetCards(ctx, "sv05")
	if err != nil || !slices.Equal(cards, []string{"sv05-157", "sv05-999"}) {
		t.Fatalf("SetCards = %v, %v", cards, err)
	}
	if _, err := c.SeriesSets(ctx, "nope"); !IsNotFound(err) {
		t.Errorf("unknown series should be not found, got %v", err)
	}
}
