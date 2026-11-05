// Command search looks up cards in the TCGdex catalog or prices on JustTCG.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mxygem/deckstrat/internal/catalog"
	"github.com/mxygem/deckstrat/internal/catalog/tcgdex"
	"github.com/mxygem/deckstrat/internal/config"
	"github.com/mxygem/deckstrat/internal/format"
	"github.com/mxygem/deckstrat/internal/httpcache"
	"github.com/mxygem/deckstrat/internal/price"
	"github.com/mxygem/deckstrat/internal/price/justtcg"
	flag "github.com/spf13/pflag"
)

func main() {
	source := flag.String("source", "tcgdex", "where to search: tcgdex (cards) or justtcg (prices)")
	name := flag.StringP("name", "n", "", "card name")
	set := flag.StringP("set", "s", "", "set: TCG Live code for tcgdex (e.g. OBF), JustTCG set ID for justtcg")
	number := flag.String("number", "", "collector number within the set, e.g. 125 or TG01")
	lang := flag.StringP("language", "l", "English", "printing language (justtcg)")
	limit := flag.Int("limit", 10, "maximum results")
	asJSON := flag.Bool("json", false, "print JSON")
	flag.Parse()

	if *name == "" && (*set == "" || *number == "") {
		fmt.Fprintln(os.Stderr, "search: need --name, or --set with --number")
		flag.Usage()
		os.Exit(2)
	}
	cfg, err := config.Load(config.Options{})
	if err != nil {
		fail(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	switch *source {
	case "tcgdex":
		cards, err := searchTCGdex(ctx, cfg, *name, *set, *number, *limit)
		if err != nil {
			fail(err)
		}
		if *asJSON {
			printJSON(cards)
			return
		}
		printCards(cards)
	case "justtcg":
		if err := cfg.RequireJustTCG(); err != nil {
			fail(err)
		}
		client := justtcg.New(cfg.JustTCGAPIKey)
		resp, err := client.Search(ctx, justtcg.Query{Q: *name, Set: *set, Number: *number, Language: []string{*lang}, Limit: *limit})
		if err != nil {
			fail(err)
		}
		if *asJSON {
			printJSON(resp.Data)
			return
		}
		var quotes []price.Quote
		for _, c := range resp.Data {
			quotes = append(quotes, justtcg.ToQuotes(c)...)
		}
		printQuotes(quotes)
		if resp.Metadata != nil {
			fmt.Fprintf(os.Stderr, "JustTCG %s plan: %d requests left today\n", resp.Metadata.APIPlan, resp.Metadata.APIDailyRequestsRemaining)
		}
	default:
		fail(fmt.Errorf("unknown --source %q (want tcgdex or justtcg)", *source))
	}
}

func newTCGdex(cfg *config.Config) *tcgdex.Client {
	cache := httpcache.New(filepath.Join(cfg.CacheDir, "tcgdex"), 24*time.Hour)
	return tcgdex.New(tcgdex.WithHTTPClient(&http.Client{Transport: cache, Timeout: 15 * time.Second}))
}

func searchTCGdex(ctx context.Context, cfg *config.Config, name, set, number string, limit int) ([]*catalog.Card, error) {
	cat := newTCGdex(cfg)
	if set != "" && number != "" {
		c, err := cat.GetByCode(ctx, set, number, name)
		if err != nil {
			return nil, err
		}
		return []*catalog.Card{c}, nil
	}
	setIDs := []string{""}
	if set != "" {
		ids, err := cat.SetIDsForCode(ctx, set)
		if err != nil {
			return nil, err
		}
		setIDs = ids
	}
	var refs []catalog.Ref
	for _, id := range setIDs {
		found, err := cat.Search(ctx, catalog.Query{Name: name, SetID: id, Limit: limit})
		if err != nil {
			return nil, err
		}
		refs = append(refs, found...)
	}
	cards := make([]*catalog.Card, 0, len(refs))
	for _, r := range refs {
		c, err := cat.Get(ctx, r.ID)
		if errors.Is(err, catalog.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		cards = append(cards, c)
	}
	return cards, nil
}

func printCards(cards []*catalog.Card) {
	if len(cards) == 0 {
		fmt.Println("no cards found")
		return
	}
	std, _ := format.Get("standard")
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tNAME\tSET\tNO.\tMARK\tKIND\tSTANDARD")
	for _, c := range cards {
		kind := string(c.Category)
		switch {
		case c.Stage != "":
			kind = strings.TrimSpace(c.Stage + " " + c.Suffix)
		case c.TrainerType != "":
			kind = c.TrainerType
		case c.EnergyType != "":
			kind = c.EnergyType + " Energy"
		}
		if c.IsAceSpec() {
			kind += " (ACE SPEC)"
		}
		legal := "yes"
		if std.PoolReason(c) != "" {
			legal = "no"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", c.ID, c.Name, c.SetCode, c.Number, c.RegulationMark, kind, legal)
	}
	w.Flush()
}

func printQuotes(quotes []price.Quote) {
	if len(quotes) == 0 {
		fmt.Println("no prices found")
		return
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSET\tNO.\tPRINTING\tCONDITION\tUSD\tUPDATED")
	for _, q := range quotes {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%.2f\t%s\n", q.Name, q.Set, q.Number, q.Printing, q.Condition, q.USD, q.Updated.Format(time.DateOnly))
	}
	w.Flush()
}

func printJSON(v any) {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "search:", err)
	os.Exit(1)
}
