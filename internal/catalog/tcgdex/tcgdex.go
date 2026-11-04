// Package tcgdex is a catalog.Catalog backed by the TCGdex REST API
// (https://tcgdex.dev). It is free and needs no key.
package tcgdex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mxygem/deckstrat/internal/catalog"
)

const DefaultBaseURL = "https://api.tcgdex.net/v2/en"

// pocketSeries is TCG Pocket, a different game that TCGdex also serves.
const pocketSeries = "tcgp"

// setCodeToID maps TCG Live set codes that TCGdex's abbreviations do not
// cover straight to set IDs. Subsets use "CODE:PREFIX" keys, matching
// TCGdex abbreviations like "BRS:TG".
var setCodeToID = map[string]string{
	"PR-SV":  "svp",
	"PR-SW":  "swshp",
	"PR-ME":  "mep",
	"HIF:SV": "sma",
}

// setIDToCode is the reverse for export, for sets whose TCG Live code is
// not their TCGdex abbreviation.
var setIDToCode = map[string]string{
	"svp":   "PR-SV",
	"swshp": "PR-SW",
	"sma":   "HIF",
}

// legacyAceSpecs are Black & White ACE SPECs. TCGdex gives them rarity
// "Rare" and no ACE SPEC text, so they are listed by ID.
var legacyAceSpecs = map[string]bool{
	"bw7-137": true, "bw7-138": true, "bw7-139": true, "bw7-140": true, // Computer Search, Crystal Edge, Crystal Wall, Gold Potion
	"bw8-128": true, "bw8-129": true, "bw8-130": true, // Dowsing Machine, Scramble Switch, Victory Piece
	"bw9-107": true, "bw9-108": true, // Life Dew, Rock Guard
	"bw10-92": true, "bw10-93": true, "bw10-94": true, "bw10-95": true, // G Booster, G Scope, Master Ball, Scoop Up Cyclone
}

type Client struct {
	baseURL string
	http    *http.Client

	mu        sync.Mutex
	sets      map[string]*apiSet  // by set ID
	codeToSet map[string][]string // TCG Live code -> set IDs
}

type Option func(*Client)

// WithBaseURL points the client at another server, e.g. in tests.
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") } }

// WithHTTPClient sets the HTTP client, e.g. one with an httpcache transport.
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

func New(opts ...Option) *Client {
	c := &Client{
		baseURL:   DefaultBaseURL,
		http:      &http.Client{Timeout: 15 * time.Second},
		sets:      map[string]*apiSet{},
		codeToSet: map[string][]string{},
	}
	for _, o := range opts {
		o(c)
	}
	return c
}

var _ catalog.Catalog = (*Client)(nil)

func (c *Client) Get(ctx context.Context, id string) (*catalog.Card, error) {
	var ac apiCard
	if err := c.get(ctx, "/cards/"+url.PathEscape(id), nil, &ac); err != nil {
		return nil, fmt.Errorf("tcgdex card %s: %w", id, err)
	}
	return c.toCard(ctx, &ac)
}

// GetByCode finds a printing by TCG Live set code and number. Subset
// numbers like "TG01" or "GG44" are looked up in the matching subset set,
// and short numbers are retried zero-padded ("SV49" -> "SV049"). See
// catalog.Catalog for how name is used.
func (c *Client) GetByCode(ctx context.Context, setCode, number, name string) (*catalog.Card, error) {
	setIDs, err := c.setIDsForCode(ctx, setCode, number)
	if err != nil {
		return nil, err
	}
	var first *catalog.Card
	for _, setID := range setIDs {
		for _, n := range numberCandidates(number) {
			var ac apiCard
			path := "/sets/" + url.PathEscape(setID) + "/" + url.PathEscape(n)
			err := c.get(ctx, path, nil, &ac)
			if errors.Is(err, catalog.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, fmt.Errorf("tcgdex card %s %s: %w", setCode, number, err)
			}
			card, err := c.toCard(ctx, &ac)
			if err != nil {
				return nil, err
			}
			if name == "" || catalog.NamesAgree(name, card.Name) {
				return card, nil
			}
			if first == nil {
				first = card
			}
			break
		}
	}
	if name != "" {
		card, err := c.uniqueInSets(ctx, setIDs, name)
		if err != nil {
			return nil, err
		}
		if card != nil {
			return card, nil
		}
	}
	if first != nil {
		return first, nil
	}
	return nil, fmt.Errorf("tcgdex card %s %s: %w", setCode, number, catalog.ErrNotFound)
}

// uniqueInSets returns the one card named name in the sets, or nil when
// there are none or several.
func (c *Client) uniqueInSets(ctx context.Context, setIDs []string, name string) (*catalog.Card, error) {
	var match []catalog.Ref
	for _, setID := range setIDs {
		refs, err := c.Search(ctx, catalog.Query{Name: name, Exact: true, SetID: setID})
		if err != nil {
			return nil, err
		}
		for _, r := range refs {
			if catalog.NamesAgree(name, r.Name) {
				match = append(match, r)
			}
		}
	}
	if len(match) != 1 {
		return nil, nil
	}
	return c.Get(ctx, match[0].ID)
}

// setIDsForCode returns the sets to try for a code and number, subset first.
func (c *Client) setIDsForCode(ctx context.Context, code, number string) ([]string, error) {
	codes := []string{code}
	if prefix := numberPrefixRe.FindStringSubmatch(number); prefix != nil && prefix[1] != "" {
		codes = []string{code + ":" + strings.ToUpper(prefix[1]), code}
	}
	var ids []string
	for _, cd := range codes {
		found, err := c.SetIDsForCode(ctx, cd)
		if errors.Is(err, catalog.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		ids = append(ids, found...)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("set code %q: %w", code, catalog.ErrNotFound)
	}
	return ids, nil
}

var numberPrefixRe = regexp.MustCompile(`^([A-Za-z]*)(\d+)$`)

// numberCandidates lists spellings of a collector number to try.
func numberCandidates(n string) []string {
	out := []string{n}
	if m := numberPrefixRe.FindStringSubmatch(n); m != nil && m[1] != "" && len(m[2]) < 3 {
		out = append(out, m[1]+fmt.Sprintf("%03s", m[2]))
	}
	return out
}

func (c *Client) Search(ctx context.Context, q catalog.Query) ([]catalog.Ref, error) {
	params := url.Values{}
	if q.Name != "" {
		if q.Exact {
			params.Set("name", "eq:"+q.Name)
		} else {
			params.Set("name", q.Name)
		}
	}
	if q.SetID != "" {
		params.Set("set.id", "eq:"+q.SetID)
	}
	if q.Limit > 0 {
		params.Set("pagination:page", "1")
		// Over-fetch a little since Pocket cards are dropped below.
		params.Set("pagination:itemsPerPage", strconv.Itoa(q.Limit*2))
	}
	var brief []apiBrief
	if err := c.get(ctx, "/cards", params, &brief); err != nil {
		return nil, fmt.Errorf("tcgdex search: %w", err)
	}
	refs := make([]catalog.Ref, 0, len(brief))
	for _, b := range brief {
		if seriesFromImage(b.Image) == pocketSeries {
			continue
		}
		refs = append(refs, catalog.Ref{ID: b.ID, Name: b.Name, Series: seriesFromImage(b.Image)})
		if q.Limit > 0 && len(refs) == q.Limit {
			break
		}
	}
	return refs, nil
}

// SetIDsForCode maps a TCG Live set code such as "OBF" to TCGdex set IDs.
// Most codes name one set, but some name several ("30C").
func (c *Client) SetIDsForCode(ctx context.Context, code string) ([]string, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if id, ok := setCodeToID[code]; ok {
		return []string{id}, nil
	}
	c.mu.Lock()
	ids, ok := c.codeToSet[code]
	c.mu.Unlock()
	if ok {
		return ids, nil
	}

	var sets []apiBrief
	params := url.Values{"abbreviation.official": {"eq:" + code}}
	if err := c.get(ctx, "/sets", params, &sets); err != nil {
		return nil, fmt.Errorf("tcgdex set code %s: %w", code, err)
	}
	if len(sets) == 0 {
		return nil, fmt.Errorf("set code %q: %w", code, catalog.ErrNotFound)
	}
	for _, s := range sets {
		ids = append(ids, s.ID)
	}
	c.mu.Lock()
	c.codeToSet[code] = ids
	c.mu.Unlock()
	return ids, nil
}

func (c *Client) set(ctx context.Context, id string) (*apiSet, error) {
	c.mu.Lock()
	s, ok := c.sets[id]
	c.mu.Unlock()
	if ok {
		return s, nil
	}
	s = &apiSet{}
	if err := c.get(ctx, "/sets/"+url.PathEscape(id), nil, s); err != nil {
		return nil, fmt.Errorf("tcgdex set %s: %w", id, err)
	}
	c.mu.Lock()
	c.sets[id] = s
	c.mu.Unlock()
	return s, nil
}

func (c *Client) toCard(ctx context.Context, ac *apiCard) (*catalog.Card, error) {
	card := ac.toCard()
	s, err := c.set(ctx, ac.Set.ID)
	if err != nil {
		return nil, err
	}
	card.SetCode = tcgLiveCode(s)
	card.Series = s.Serie.ID
	return card, nil
}

// tcgLiveCode is the set code TCG Live uses: "BRS" for the "BRS:TG"
// subset, "PR-SV" for SVP promos, "" when unknown.
func tcgLiveCode(s *apiSet) string {
	if code, ok := setIDToCode[s.ID]; ok {
		return code
	}
	code, _, _ := strings.Cut(s.Abbreviation.Official, ":")
	return code
}

// APIError is a non-2xx response other than 404.
type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string { return fmt.Sprintf("tcgdex: HTTP %d: %s", e.Status, e.Body) }

func (c *Client) get(ctx context.Context, path string, params url.Values, out any) error {
	u := c.baseURL + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "deckstrat")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return catalog.ErrNotFound
	case resp.StatusCode != http.StatusOK:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return &APIError{Status: resp.StatusCode, Body: strings.TrimSpace(string(body))}
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decoding %s: %w", path, err)
	}
	return nil
}

// IsNotFound reports whether err means the card or set does not exist.
func IsNotFound(err error) bool { return errors.Is(err, catalog.ErrNotFound) }

// seriesFromImage reads the series ID out of an asset URL like
// https://assets.tcgdex.net/en/tcgp/A2b/010.
func seriesFromImage(img string) string {
	u, err := url.Parse(img)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[1]
}
