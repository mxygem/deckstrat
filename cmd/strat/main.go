// Command strat builds a deck around a starter card.
//
//	strat "Gardevoir ex"
//	strat -f standard "Dragapult ex TWM 130"
//
// The starter is named as a decklist line without the quantity. It prints
// the starter's needs, every pick with its reason, Pokémon worth testing
// as partners, the format check, and the deck in TCG Live's format.
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/mxygem/deckstrat/internal/catalog"
	"github.com/mxygem/deckstrat/internal/catalog/tcgdex"
	"github.com/mxygem/deckstrat/internal/config"
	"github.com/mxygem/deckstrat/internal/deck"
	"github.com/mxygem/deckstrat/internal/format"
	"github.com/mxygem/deckstrat/internal/httpcache"
	"github.com/mxygem/deckstrat/internal/pool"
	"github.com/mxygem/deckstrat/internal/strat"
	"github.com/mxygem/deckstrat/internal/tag"
	flag "github.com/spf13/pflag"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("strat", flag.ContinueOnError)
	fs.SetOutput(stderr)
	formatID := fs.StringP("format", "f", "standard", fmt.Sprintf("format %v", format.IDs()))
	configPath := fs.String("config", "", "role skeleton (default data/strat/<format>.json)")
	storePath := fs.String("store", "data/tags.json", "tag store")
	fs.Usage = func() {
		fmt.Fprintln(stderr, `usage: strat [flags] "<starter card>"`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	if err := build(context.Background(), fs.Arg(0), *formatID, *configPath, *storePath, stdout, stderr); err != nil {
		fmt.Fprintln(stderr, "strat:", err)
		return 2
	}
	return 0
}

func build(ctx context.Context, starterName, formatID, configPath, storePath string, stdout, stderr io.Writer) error {
	f, err := format.Get(formatID)
	if err != nil {
		return err
	}
	if configPath == "" {
		configPath = filepath.Join("data", "strat", f.ID+".json")
	}
	cfg, err := strat.LoadConfig(configPath)
	if err != nil {
		return err
	}
	conf, err := config.Load(config.Options{})
	if err != nil {
		return err
	}
	cache := httpcache.New(filepath.Join(conf.CacheDir, "tcgdex"), 7*24*time.Hour)
	client := tcgdex.New(tcgdex.WithHTTPClient(&http.Client{Transport: cache, Timeout: 30 * time.Second}))

	p, err := pool.LoadOrBuild(ctx, filepath.Join(conf.CacheDir, "pool", f.ID+".json"), client, client, f, false, stderr)
	if err != nil {
		return err
	}
	store, err := tag.LoadStore(storePath)
	if err != nil {
		return err
	}
	starter, err := findStarter(ctx, client, p, starterName)
	if err != nil {
		return err
	}

	res, err := strat.Build(starter, p.Cards, strat.StoreTags{Store: store}, cfg)
	if err != nil {
		return err
	}
	printResult(stdout, res)

	entries := make([]deck.Entry, len(res.Picks))
	for i, pk := range res.Picks {
		entries[i] = deck.Entry{Line: deck.Line{Qty: pk.Qty, Name: catalog.RuleName(pk.Card.Name)}, Card: pk.Card}
	}
	v := &format.Validator{Format: f, Reprints: format.CatalogPrintings(client)}
	check, err := v.Validate(ctx, entries)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout)
	if check.Legal() {
		fmt.Fprintf(stdout, "%s check: legal, %d cards\n", f.Name, res.Total())
	} else {
		fmt.Fprintf(stdout, "%s check: not legal\n", f.Name)
		for _, vi := range check.Violations {
			fmt.Fprintf(stdout, "  [%s] %s\n", vi.Rule, vi.Message)
		}
	}
	fmt.Fprintf(stdout, "\n%s\n", deck.ExportTCGLive(entries))
	return nil
}

// findStarter resolves the starter like a decklist line, then swaps in
// the pool's printing so tags and legality line up.
func findStarter(ctx context.Context, cat catalog.Catalog, p *pool.Pool, name string) (*catalog.Card, error) {
	d, unparsed, err := deck.ParseTCGLive(strings.NewReader("1 " + name + "\n"))
	if err != nil {
		return nil, err
	}
	if len(unparsed) > 0 {
		return nil, fmt.Errorf("can't read starter %q", name)
	}
	entries, unmatched, err := deck.Resolve(ctx, cat, d)
	if err != nil {
		return nil, err
	}
	if len(unmatched) > 0 {
		return nil, fmt.Errorf("starter %s: %s", name, unmatched[0].Reason)
	}
	c := entries[0].Card
	key := c.FunctionalKey()
	for _, pc := range p.Cards {
		if pc.FunctionalKey() == key {
			return pc, nil
		}
	}
	return nil, fmt.Errorf("%s (%s) is not legal in %s", c.Name, c.ID, p.Format)
}

func printResult(w io.Writer, r *strat.Result) {
	fmt.Fprintf(w, "Starter: %s (%s %s)\n\n", r.Starter.Name, r.Starter.SetCode, r.Starter.Number)
	fmt.Fprintln(w, "Needs:")
	if len(r.Needs) == 0 {
		fmt.Fprintln(w, "  none found")
	}
	for _, n := range r.Needs {
		term := n.Tag.Term
		if n.Tag.Qualifier != "" {
			term += " (" + n.Tag.Qualifier + ")"
		}
		fmt.Fprintf(w, "  %-28s from %s: %s\n", term, n.From, n.Tag.Reason)
	}

	fmt.Fprintln(w, "\nPicks:")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, p := range r.Picks {
		fmt.Fprintf(tw, "  %d\t%s\t%s\t%s\n", p.Qty, p.Card.Name, p.Role, p.Reason)
	}
	tw.Flush()

	if len(r.Leads) > 0 {
		fmt.Fprintln(w, "\nPokémon worth testing as partners:")
		for _, l := range r.Leads {
			fmt.Fprintf(w, "  %s (%s %s): %s\n", l.Card.Name, l.Card.SetCode, l.Card.Number, strings.Join(l.Reasons, "; "))
		}
	}
	if len(r.Notes) > 0 {
		fmt.Fprintln(w, "\nNotes:")
		for _, n := range r.Notes {
			fmt.Fprintf(w, "  %s\n", n)
		}
	}
}
