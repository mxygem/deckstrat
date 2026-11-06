// Command tag gives cards needs and provides tags.
//
//	tag show "Ultra Ball MEG 131" "Rare Candy"
//
// Cards are named as decklist lines without the quantity: a name, or a
// name with a TCG Live set code and number.
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
	"github.com/mxygem/deckstrat/internal/httpcache"
	"github.com/mxygem/deckstrat/internal/tag"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, nil))
}

// run executes the command and returns its exit code. A nil cat means the
// cached TCGdex catalog.
func run(args []string, stdout, stderr io.Writer, cat catalog.Catalog) int {
	usage := func() int {
		fmt.Fprintln(stderr, `usage: tag show <card> [<card>...]`)
		return 2
	}
	if len(args) < 2 || args[0] != "show" {
		return usage()
	}
	fail := func(err error) int {
		fmt.Fprintln(stderr, "tag:", err)
		return 2
	}
	if cat == nil {
		var err error
		if cat, err = defaultCatalog(); err != nil {
			return fail(err)
		}
	}

	ctx := context.Background()
	cards, err := resolve(ctx, cat, args[1:])
	if err != nil {
		return fail(err)
	}
	for i, c := range cards {
		tags, err := tag.Rules{}.Tag(ctx, c)
		if err != nil {
			return fail(err)
		}
		if i > 0 {
			fmt.Fprintln(stdout)
		}
		printCard(stdout, c, tags)
	}
	return 0
}

func defaultCatalog() (catalog.Catalog, error) {
	cfg, err := config.Load(config.Options{})
	if err != nil {
		return nil, err
	}
	cache := httpcache.New(filepath.Join(cfg.CacheDir, "tcgdex"), 24*time.Hour)
	return tcgdex.New(tcgdex.WithHTTPClient(&http.Client{Transport: cache, Timeout: 15 * time.Second})), nil
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

func printCard(w io.Writer, c *catalog.Card, tags []tag.Tag) {
	fmt.Fprintf(w, "%s (%s %s, %s)\n", c.Name, c.SetCode, c.Number, c.ID)
	if len(tags) == 0 {
		fmt.Fprintln(w, "  no tags")
		return
	}
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
