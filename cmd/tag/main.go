// Command tag gives cards needs and provides tags.
//
//	tag pool [-f standard] [--refresh]   build or load a format's card pool
//	tag run  [-f standard]               tag every card in the pool
//	tag show <card> [<card>...]          print a card's tags
//
// Cards are named as decklist lines without the quantity: a name, or a
// name with a TCG Live set code and number ("Ultra Ball MEG 131").
package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
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
	"github.com/mxygem/deckstrat/internal/tag"
	flag "github.com/spf13/pflag"
)

// DefaultStore is the committed tag store, relative to the repo root.
const DefaultStore = "data/tags.json"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

type env struct {
	cfg    *config.Config
	client *tcgdex.Client
	stdout io.Writer
	stderr io.Writer
}

func run(args []string, stdout, stderr io.Writer) int {
	usage := func() int {
		fmt.Fprintln(stderr, "usage: tag pool|run|show [flags] [cards]")
		return 2
	}
	if len(args) == 0 {
		return usage()
	}
	cfg, err := config.Load(config.Options{})
	if err != nil {
		fmt.Fprintln(stderr, "tag:", err)
		return 2
	}
	// Card data rarely changes, so a week-old cache is fine here.
	cache := httpcache.New(filepath.Join(cfg.CacheDir, "tcgdex"), 7*24*time.Hour)
	e := &env{
		cfg:    cfg,
		client: tcgdex.New(tcgdex.WithHTTPClient(&http.Client{Transport: cache, Timeout: 30 * time.Second})),
		stdout: stdout,
		stderr: stderr,
	}
	ctx := context.Background()
	var cmdErr error
	switch args[0] {
	case "pool":
		cmdErr = e.pool(ctx, args[1:])
	case "run":
		cmdErr = e.run(ctx, args[1:])
	case "show":
		cmdErr = e.show(ctx, args[1:])
	default:
		return usage()
	}
	if cmdErr != nil {
		fmt.Fprintln(stderr, "tag:", cmdErr)
		return 2
	}
	return 0
}

// loadPool reads the cached pool, rebuilding it when missing, older than
// a day, or refresh is set.
func (e *env) loadPool(ctx context.Context, formatID string, refresh bool) (*pool.Pool, error) {
	f, err := format.Get(formatID)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(e.cfg.CacheDir, "pool", f.ID+".json")
	if !refresh {
		p, err := pool.Load(path)
		if err != nil {
			return nil, err
		}
		if p != nil && p.AsOf == time.Now().Format(time.DateOnly) {
			return p, nil
		}
	}
	fmt.Fprintf(e.stderr, "building the %s pool (first run fetches a few thousand cards)...\n", f.Name)
	last := time.Now()
	p, err := pool.Build(ctx, e.client, e.client, f, pool.Options{Progress: func(done, total int) {
		if time.Since(last) > 2*time.Second || done == total {
			fmt.Fprintf(e.stderr, "  %d/%d cards\n", done, total)
			last = time.Now()
		}
	}})
	if err != nil {
		return nil, err
	}
	return p, p.Save(path)
}

func (e *env) pool(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("pool", flag.ContinueOnError)
	formatID := fs.StringP("format", "f", "standard", "format")
	refresh := fs.Bool("refresh", false, "rebuild even if today's pool is cached")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, err := e.loadPool(ctx, *formatID, *refresh)
	if err != nil {
		return err
	}
	counts := map[catalog.Category]int{}
	for _, c := range p.Cards {
		counts[c.Category]++
	}
	fmt.Fprintf(e.stdout, "%s pool on %s: %d distinct cards (%d Pokémon, %d Trainer, %d Energy)\n",
		p.Format, p.AsOf, len(p.Cards), counts[catalog.Pokemon], counts[catalog.Trainer], counts[catalog.Energy])
	return nil
}

func (e *env) run(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	formatID := fs.StringP("format", "f", "standard", "format whose pool to tag")
	storePath := fs.String("store", DefaultStore, "tag store file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	p, err := e.loadPool(ctx, *formatID, false)
	if err != nil {
		return err
	}
	store, err := tag.LoadStore(*storePath)
	if err != nil {
		return err
	}
	tagged, untagged := 0, 0
	for _, c := range p.Cards {
		tags, err := tag.Rules{}.Tag(ctx, c)
		if err != nil {
			return err
		}
		store.Put(c, tags)
		if len(tags) > 0 {
			tagged++
		} else {
			untagged++
		}
	}
	if err := store.Save(*storePath); err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "tagged %d of %d cards with rules (%d got no tags); wrote %s\n",
		tagged, len(p.Cards), untagged, *storePath)
	return nil
}

func (e *env) show(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	storePath := fs.String("store", DefaultStore, "tag store file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("name at least one card")
	}
	store, err := tag.LoadStore(*storePath)
	if err != nil {
		return err
	}
	cards, err := resolve(ctx, e.client, fs.Args())
	if err != nil {
		return err
	}
	for i, c := range cards {
		tags, err := tag.Rules{}.Tag(ctx, c)
		if err != nil {
			return err
		}
		reviewed := false
		if entry := store.Get(c); entry != nil {
			tags = tag.Merge(tags, entry.Tags)
			reviewed = entry.Reviewed
		}
		if i > 0 {
			fmt.Fprintln(e.stdout)
		}
		printCard(e.stdout, c, tags, reviewed)
	}
	return nil
}

// resolve looks cards up the way a decklist does.
func resolve(ctx context.Context, cat catalog.Catalog, names []string) ([]*catalog.Card, error) {
	var b strings.Builder
	for _, n := range names {
		fmt.Fprintf(&b, "1 %s\n", n)
	}
	d, unparsed, err := deck.ParseTCGLive(strings.NewReader(b.String()))
	if err != nil {
		return nil, err
	}
	if len(unparsed) > 0 {
		return nil, fmt.Errorf("can't read %q", unparsed[0].Text)
	}
	entries, unmatched, err := deck.Resolve(ctx, cat, d)
	if err != nil {
		return nil, err
	}
	if len(unmatched) > 0 {
		return nil, fmt.Errorf("%s: %s", unmatched[0].Line.Name, unmatched[0].Reason)
	}
	cards := make([]*catalog.Card, len(entries))
	for i, e := range entries {
		cards[i] = e.Card
	}
	return cards, nil
}

func printCard(w io.Writer, c *catalog.Card, tags []tag.Tag, reviewed bool) {
	note := ""
	if reviewed {
		note = ", reviewed"
	}
	fmt.Fprintf(w, "%s (%s %s, %s%s)\n", c.Name, c.SetCode, c.Number, c.ID, note)
	if len(tags) == 0 {
		fmt.Fprintln(w, "  no tags")
		return
	}
	sort.SliceStable(tags, func(i, j int) bool { return tags[i].Kind > tags[j].Kind }) // provides, then needs
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, t := range tags {
		term := t.Term
		if t.Qualifier != "" {
			term += " (" + t.Qualifier + ")"
		}
		fmt.Fprintf(tw, "  %s\t%s\t%.2f\t%s\t%s\n", t.Kind, term, t.Confidence, t.Source, t.Reason)
	}
	tw.Flush()
}
