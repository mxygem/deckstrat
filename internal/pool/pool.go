// Package pool lists the cards legal in a format, one printing per
// functional card, for the tagger and the strat engine to choose from.
package pool

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/mxygem/deckstrat/internal/catalog"
	"github.com/mxygem/deckstrat/internal/format"
)

// Lister lists a catalog's sets and their cards.
type Lister interface {
	SeriesSets(ctx context.Context, series string) ([]string, error)
	SetCards(ctx context.Context, setID string) ([]string, error)
}

// DefaultSeries are searched when a format has no series rule. Standard
// pools by regulation mark, and only these series carry H and later.
var DefaultSeries = []string{"sv", "me"}

type Options struct {
	// Series to list cards from. Empty means the format's series, or
	// DefaultSeries.
	Series []string
	// Workers fetching cards at once. Zero means 8.
	Workers int
	// AsOf decides which sets are legal yet. Zero means now.
	AsOf time.Time
	// Progress, if set, is called after each card is fetched.
	Progress func(done, total int)
}

// Pool is a format's legal cards on a date.
type Pool struct {
	Format string          `json:"format"`
	AsOf   string          `json:"as_of"` // YYYY-MM-DD
	Cards  []*catalog.Card `json:"cards"`
}

// Build fetches every card in the series and keeps the legal ones. Bans
// are not applied here; validate the finished deck for those.
func Build(ctx context.Context, l Lister, cat catalog.Catalog, f *format.Format, opt Options) (*Pool, error) {
	series := opt.Series
	if len(series) == 0 {
		series = f.Pool.Series
	}
	if len(series) == 0 {
		series = DefaultSeries
	}
	workers := cmp.Or(opt.Workers, 8)
	asOf := opt.AsOf
	if asOf.IsZero() {
		asOf = time.Now()
	}
	today := asOf.Format(time.DateOnly)

	var ids []string
	for _, s := range series {
		sets, err := l.SeriesSets(ctx, s)
		if err != nil {
			return nil, err
		}
		for _, set := range sets {
			cards, err := l.SetCards(ctx, set)
			if err != nil {
				return nil, err
			}
			ids = append(ids, cards...)
		}
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		mu       sync.Mutex
		firstErr error
		done     int
		legal    []*catalog.Card
	)
	jobs := make(chan string)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				c, err := cat.Get(ctx, id)
				mu.Lock()
				done++
				switch {
				case errors.Is(err, catalog.ErrNotFound):
				case err != nil:
					if firstErr == nil {
						firstErr = fmt.Errorf("fetching %s: %w", id, err)
						cancel()
					}
				case f.PoolReason(c) == "" && f.LegalFrom(c) <= today:
					legal = append(legal, c)
				}
				if opt.Progress != nil {
					opt.Progress(done, len(ids))
				}
				mu.Unlock()
			}
		}()
	}
feed:
	for _, id := range ids {
		select {
		case jobs <- id:
		case <-ctx.Done():
			break feed
		}
	}
	close(jobs)
	wg.Wait()
	if firstErr != nil {
		return nil, firstErr
	}
	return &Pool{Format: f.ID, AsOf: today, Cards: Distinct(legal)}, nil
}

// Distinct keeps one printing per functional card: the newest release,
// then the lowest number in the set (usually the regular printing rather
// than a full-art one). The result is sorted by name, then ID.
func Distinct(cards []*catalog.Card) []*catalog.Card {
	best := map[string]*catalog.Card{}
	for _, c := range cards {
		k := c.FunctionalKey()
		if cur, ok := best[k]; !ok || preferred(c, cur) {
			best[k] = c
		}
	}
	out := make([]*catalog.Card, 0, len(best))
	for _, c := range best {
		out = append(out, c)
	}
	slices.SortFunc(out, func(a, b *catalog.Card) int {
		return cmp.Or(cmp.Compare(a.Name, b.Name), cmp.Compare(a.ID, b.ID))
	})
	return out
}

func preferred(a, b *catalog.Card) bool {
	if a.ReleaseDate != b.ReleaseDate {
		return a.ReleaseDate > b.ReleaseDate
	}
	an, aerr := strconv.Atoi(a.Number)
	bn, berr := strconv.Atoi(b.Number)
	switch {
	case aerr == nil && berr == nil && an != bn:
		return an < bn
	case aerr == nil && berr != nil:
		return true // "131" over "TG01"
	case aerr != nil && berr == nil:
		return false
	}
	return a.ID < b.ID
}

// LoadOrBuild returns the pool saved at path when it was built today, and
// otherwise builds it, saves it and returns it. Progress lines go to log
// when it is not nil.
func LoadOrBuild(ctx context.Context, path string, l Lister, cat catalog.Catalog, f *format.Format, refresh bool, log io.Writer) (*Pool, error) {
	if !refresh {
		p, err := Load(path)
		if err != nil {
			return nil, err
		}
		if p != nil && p.Format == f.ID && p.AsOf == time.Now().Format(time.DateOnly) {
			return p, nil
		}
	}
	if log == nil {
		log = io.Discard
	}
	fmt.Fprintf(log, "building the %s pool (the first run fetches a few thousand cards)...\n", f.Name)
	last := time.Now()
	p, err := Build(ctx, l, cat, f, Options{Progress: func(done, total int) {
		if time.Since(last) > 2*time.Second || done == total {
			fmt.Fprintf(log, "  %d/%d cards\n", done, total)
			last = time.Now()
		}
	}})
	if err != nil {
		return nil, err
	}
	return p, p.Save(path)
}

// Load reads a saved pool. It returns nil, nil when the file is missing.
func Load(path string) (*Pool, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p Pool
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, fmt.Errorf("reading pool %s: %w", path, err)
	}
	return &p, nil
}

// Save writes the pool atomically.
func (p *Pool) Save(path string) error {
	b, err := json.Marshal(p)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".pool-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
