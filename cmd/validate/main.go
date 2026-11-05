// Command validate checks a TCG Live decklist against a format.
//
//	validate --format standard deck.txt
//	pbpaste | validate -
//
// It exits 0 when the deck is legal, 1 when it is not (or some lines could
// not be matched) and 2 on usage or catalog errors.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/mxygem/deckstrat/internal/catalog"
	"github.com/mxygem/deckstrat/internal/catalog/tcgdex"
	"github.com/mxygem/deckstrat/internal/config"
	"github.com/mxygem/deckstrat/internal/deck"
	"github.com/mxygem/deckstrat/internal/format"
	"github.com/mxygem/deckstrat/internal/httpcache"
	flag "github.com/spf13/pflag"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, nil))
}

// run executes the command and returns its exit code. A nil cat means the
// cached TCGdex catalog.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer, cat catalog.Catalog) int {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	formatID := fs.StringP("format", "f", "standard", fmt.Sprintf("format to check against %v", format.IDs()))
	export := fs.Bool("export", false, "also print the deck in TCG Live format (as the \"export\" field with --json)")
	asJSON := fs.Bool("json", false, "print the result as JSON")
	fs.Usage = func() {
		fmt.Fprintln(stderr, "usage: validate [flags] <decklist file | ->")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	fail := func(err error) int {
		fmt.Fprintln(stderr, "validate:", err)
		return 2
	}

	f, err := format.Get(*formatID)
	if err != nil {
		return fail(err)
	}
	if cat == nil {
		cfg, err := config.Load(config.Options{})
		if err != nil {
			return fail(err)
		}
		cache := httpcache.New(filepath.Join(cfg.CacheDir, "tcgdex"), 24*time.Hour)
		cat = tcgdex.New(tcgdex.WithHTTPClient(&http.Client{Transport: cache, Timeout: 15 * time.Second}))
	}

	in := stdin
	if name := fs.Arg(0); name != "-" {
		file, err := os.Open(name)
		if err != nil {
			return fail(err)
		}
		defer file.Close()
		in = file
	}
	d, unparsed, err := deck.ParseTCGLive(in)
	if err != nil {
		return fail(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	entries, unmatched, err := deck.Resolve(ctx, cat, d)
	if err != nil {
		return fail(err)
	}
	v := &format.Validator{Format: f, Reprints: format.CatalogPrintings(cat)}
	res, err := v.Validate(ctx, entries)
	if err != nil {
		return fail(err)
	}

	legal := res.Legal() && len(unparsed) == 0 && len(unmatched) == 0
	exported := ""
	if *export {
		exported = deck.ExportTCGLive(entries)
	}
	if *asJSON {
		printJSON(stdout, f, res, unparsed, unmatched, legal, exported)
	} else {
		printText(stdout, f, d, res, unparsed, unmatched, legal)
		if *export {
			fmt.Fprintln(stdout)
			fmt.Fprint(stdout, exported)
		}
	}
	if !legal {
		return 1
	}
	return 0
}

func printText(w io.Writer, f *format.Format, d deck.Deck, res format.Result, unparsed []deck.Unparsed, unmatched []deck.Unmatched, legal bool) {
	fmt.Fprintf(w, "%s (%s), %d cards\n", f.Name, f.Version, d.Count())
	if !f.BanListVerified {
		fmt.Fprintf(w, "warning: the %s ban list has not been entered yet, so banned cards are not checked\n", f.Name)
	}
	for _, u := range unparsed {
		fmt.Fprintf(w, "unreadable line %d: %q: %s\n", u.LineNo, u.Text, u.Reason)
	}
	for _, u := range unmatched {
		fmt.Fprintf(w, "unmatched line %d: %q: %s\n", u.Line.LineNo, u.Line.String(), u.Reason)
	}
	if len(unparsed)+len(unmatched) > 0 {
		fmt.Fprintln(w, "note: unmatched lines are left out of the checks below")
	}
	for _, n := range res.Notes {
		fmt.Fprintln(w, "note:", n.Message)
	}
	for _, v := range res.Violations {
		fmt.Fprintf(w, "illegal [%s]: %s\n", v.Rule, v.Message)
	}
	if legal {
		fmt.Fprintln(w, "legal")
	} else {
		fmt.Fprintln(w, "not legal")
	}
}

func printJSON(w io.Writer, f *format.Format, res format.Result, unparsed []deck.Unparsed, unmatched []deck.Unmatched, legal bool, exported string) {
	type line struct {
		LineNo int    `json:"line_no"`
		Text   string `json:"text"`
		Reason string `json:"reason"`
	}
	problems := []line{}
	for _, u := range unparsed {
		problems = append(problems, line{u.LineNo, u.Text, u.Reason})
	}
	for _, u := range unmatched {
		problems = append(problems, line{u.Line.LineNo, u.Line.String(), u.Reason})
	}
	out := struct {
		Format          string             `json:"format"`
		Version         string             `json:"version"`
		Legal           bool               `json:"legal"`
		BanListVerified bool               `json:"ban_list_verified"`
		Unmatched       []line             `json:"unmatched"`
		Violations      []format.Violation `json:"violations"`
		Notes           []format.Note      `json:"notes"`
		Export          string             `json:"export,omitempty"`
	}{f.ID, f.Version, legal, f.BanListVerified, problems, res.Violations, res.Notes, exported}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}
