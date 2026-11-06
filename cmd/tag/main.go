// Command tag gives cards needs and provides tags.
//
//	tag pool [-f standard] [--refresh]   build or load a format's card pool
//	tag run  [-f standard] [--llm]       tag every card in the pool
//	tag show [--llm] <card> [<card>...]  print a card's tags
//	tag eval [--source rules|store|llm]  score tags against data/eval/tags.json
//
// --llm also asks Claude, which needs ANTHROPIC_API_KEY or an "ant auth
// login" profile. run sends one Message Batch (half price) and saves its
// ID, so an interrupted run resumes instead of paying twice.
//
// Cards are named as decklist lines without the quantity: a name, or a
// name with a TCG Live set code and number ("Ultra Ball MEG 131").
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"

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
		fmt.Fprintln(stderr, "usage: tag pool|run|show|eval [flags] [cards]")
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
	case "eval":
		cmdErr = e.eval(ctx, args[1:])
	default:
		return usage()
	}
	if cmdErr != nil {
		fmt.Fprintln(stderr, "tag:", cmdErr)
		return 2
	}
	return 0
}

// loadPool reads today's cached pool or builds it.
func (e *env) loadPool(ctx context.Context, formatID string, refresh bool) (*pool.Pool, error) {
	f, err := format.Get(formatID)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(e.cfg.CacheDir, "pool", f.ID+".json")
	return pool.LoadOrBuild(ctx, path, e.client, e.client, f, refresh, e.stderr)
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
	useLLM := fs.Bool("llm", false, "also tag with Claude, in one Message Batch")
	retag := fs.Bool("retag", false, "with --llm, retag cards that already have Claude tags")
	limit := fs.Int("limit", 0, "with --llm, tag at most this many cards (0 means all)")
	model := fs.String("model", tag.DefaultModel, "Claude model")
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

	// Rules first, keeping Claude and human tags already stored.
	tagged := 0
	for _, c := range p.Cards {
		tags, err := tag.Rules{}.Tag(ctx, c)
		if err != nil {
			return err
		}
		if old := store.Get(c); old != nil {
			tags = append(tags, sourced(old.Tags, tag.LLM)...)
		}
		store.Put(c, tags)
		if len(tags) > 0 {
			tagged++
		}
	}
	if err := store.Save(*storePath); err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "rules: tagged %d of %d cards; wrote %s\n", tagged, len(p.Cards), *storePath)
	if !*useLLM {
		return nil
	}

	var todo []*catalog.Card
	for _, c := range p.Cards {
		old := store.Get(c)
		if old != nil && (old.Reviewed || (!*retag && len(sourced(old.Tags, tag.LLM)) > 0)) {
			continue
		}
		todo = append(todo, c)
	}
	if *limit > 0 && len(todo) > *limit {
		todo = todo[:*limit]
	}
	if len(todo) == 0 {
		fmt.Fprintln(e.stdout, "claude: every card already has Claude tags (use --retag to redo them)")
		return nil
	}

	claude := &tag.Claude{Client: anthropic.NewClient(), Model: *model, Progress: func(n anthropic.MessageBatchRequestCounts) {
		fmt.Fprintf(e.stderr, "  batch: %d processing, %d succeeded, %d errored\n", n.Processing, n.Succeeded, n.Errored)
	}}
	pendingPath := filepath.Join(e.cfg.CacheDir, "tag-batch-"+p.Format+".txt")
	var results []tag.BatchResult
	if b, err := os.ReadFile(pendingPath); err == nil && strings.TrimSpace(string(b)) != "" {
		id := strings.TrimSpace(string(b))
		fmt.Fprintf(e.stderr, "resuming batch %s\n", id)
		results, err = claude.Results(ctx, id, todo)
		if err != nil {
			return err
		}
	} else {
		fmt.Fprintf(e.stderr, "sending %d cards to %s in one batch...\n", len(todo), *model)
		results, err = claude.Batch(ctx, todo, func(id string) {
			if err := os.WriteFile(pendingPath, []byte(id+"\n"), 0o600); err != nil {
				fmt.Fprintln(e.stderr, "warning: can't save the batch ID:", err)
			}
			fmt.Fprintf(e.stderr, "batch %s submitted; rerun the same command to resume if interrupted\n", id)
		})
		if err != nil {
			return err
		}
	}

	var ok, failed int
	var usage anthropic.Usage
	for _, r := range results {
		if r.Err != nil {
			failed++
			fmt.Fprintln(e.stderr, " ", r.Err)
			continue
		}
		ok++
		usage.InputTokens += r.Usage.InputTokens
		usage.OutputTokens += r.Usage.OutputTokens
		usage.CacheReadInputTokens += r.Usage.CacheReadInputTokens
		usage.CacheCreationInputTokens += r.Usage.CacheCreationInputTokens
		rules, err := tag.Rules{}.Tag(ctx, r.Card)
		if err != nil {
			return err
		}
		store.Put(r.Card, append(rules, r.Tags...))
	}
	if err := store.Save(*storePath); err != nil {
		return err
	}
	os.Remove(pendingPath)
	fmt.Fprintf(e.stdout, "claude: tagged %d cards, %d failed; tokens: %d input, %d cache read, %d cache write, %d output\n",
		ok, failed, usage.InputTokens, usage.CacheReadInputTokens, usage.CacheCreationInputTokens, usage.OutputTokens)
	return nil
}

// sourced keeps the tags from one source.
func sourced(tags []tag.Tag, src tag.Source) []tag.Tag {
	var out []tag.Tag
	for _, t := range tags {
		if t.Source == src {
			out = append(out, t)
		}
	}
	return out
}

func (e *env) show(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("show", flag.ContinueOnError)
	storePath := fs.String("store", DefaultStore, "tag store file")
	useLLM := fs.Bool("llm", false, "also ask Claude now, one request per card (not saved)")
	model := fs.String("model", tag.DefaultModel, "Claude model")
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
		if *useLLM {
			claude := &tag.Claude{Client: anthropic.NewClient(), Model: *model}
			llmTags, err := claude.Tag(ctx, c)
			if err != nil {
				return err
			}
			tags = tag.Merge(tags, llmTags)
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

// DefaultEval is the hand-tagged eval set, relative to the repo root.
const DefaultEval = "data/eval/tags.json"

func (e *env) eval(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("eval", flag.ContinueOnError)
	evalPath := fs.String("set", DefaultEval, "eval set file")
	source := fs.String("source", "rules", "tags to score: rules (run now), store (merged store tags) or llm (ask Claude now, one request per card)")
	storePath := fs.String("store", DefaultStore, "tag store file, for --source store")
	model := fs.String("model", tag.DefaultModel, "Claude model, for --source llm")
	if err := fs.Parse(args); err != nil {
		return err
	}
	b, err := os.ReadFile(*evalPath)
	if err != nil {
		return err
	}
	var set struct {
		Cards []struct {
			Card string   `json:"card"`
			Tags []string `json:"tags"`
		} `json:"cards"`
	}
	if err := json.Unmarshal(b, &set); err != nil {
		return fmt.Errorf("reading %s: %w", *evalPath, err)
	}
	names := make([]string, len(set.Cards))
	gold := map[string][]string{}
	for i, c := range set.Cards {
		names[i] = c.Card
		gold[c.Card] = c.Tags
	}
	cards, err := resolve(ctx, e.client, names)
	if err != nil {
		return err
	}

	var tagger func(*catalog.Card) ([]tag.Tag, error)
	switch *source {
	case "rules":
		tagger = func(c *catalog.Card) ([]tag.Tag, error) { return tag.Rules{}.Tag(ctx, c) }
	case "llm":
		claude := &tag.Claude{Client: anthropic.NewClient(), Model: *model}
		tagger = func(c *catalog.Card) ([]tag.Tag, error) { return claude.Tag(ctx, c) }
	case "store":
		store, err := tag.LoadStore(*storePath)
		if err != nil {
			return err
		}
		tagger = func(c *catalog.Card) ([]tag.Tag, error) {
			if entry := store.Get(c); entry != nil {
				return entry.Tags, nil
			}
			return nil, nil
		}
	default:
		return fmt.Errorf("unknown --source %q", *source)
	}

	pred := map[string][]string{}
	for i, c := range cards {
		tags, err := tagger(c)
		if err != nil {
			return err
		}
		for _, t := range tags {
			pred[names[i]] = append(pred[names[i]], t.Key())
		}
	}
	r := tag.Score(gold, pred)

	pct := func(v float64, ok bool) string {
		if !ok {
			return "-"
		}
		return fmt.Sprintf("%.0f%%", v*100)
	}
	fmt.Fprintf(e.stdout, "%s on %d cards: precision %s, recall %s (%d right, %d extra, %d missed)\n\n",
		*source, len(cards), pct(r.Precision, r.TP+r.FP > 0), pct(r.Recall, r.TP+r.FN > 0), r.TP, r.FP, r.FN)
	tw := tabwriter.NewWriter(e.stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TERM\tPRECISION\tRECALL\tRIGHT\tEXTRA\tMISSED")
	for _, t := range r.Terms {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%d\n", t.Term, pct(t.Precision, t.HasPrecision), pct(t.Recall, t.HasRecall), t.TP, t.FP, t.FN)
	}
	tw.Flush()
	if len(r.Diffs) > 0 {
		fmt.Fprintln(e.stdout, "\ndisagreements:")
		for _, d := range r.Diffs {
			fmt.Fprintf(e.stdout, "  %s\n", d.Card)
			for _, k := range d.Missing {
				fmt.Fprintf(e.stdout, "    missed %s\n", k)
			}
			for _, k := range d.Extra {
				fmt.Fprintf(e.stdout, "    extra  %s\n", k)
			}
		}
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
