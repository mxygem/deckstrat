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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mxygem/deckstrat/internal/catalog"
)

const DefaultBaseURL = "https://api.tcgdex.net/v2/en"

// pocketSeries is TCG Pocket, a different game that TCGdex also serves.
const pocketSeries = "tcgp"

// setCodeAliases maps TCG Live set codes that differ from TCGdex's
// official abbreviation.
var setCodeAliases = map[string]string{
	"PR-SV": "SVP",
	"PR-ME": "MEP",
}

type Client struct {
	baseURL string
	http    *http.Client

	mu        sync.Mutex
	sets      map[string]*apiSet // by set ID
	codeToSet map[string]string  // TCG Live code -> set ID
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
		codeToSet: map[string]string{},
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

func (c *Client) GetByCode(ctx context.Context, setCode, number string) (*catalog.Card, error) {
	setID, err := c.SetIDForCode(ctx, setCode)
	if err != nil {
		return nil, err
	}
	var ac apiCard
	path := "/sets/" + url.PathEscape(setID) + "/" + url.PathEscape(number)
	if err := c.get(ctx, path, nil, &ac); err != nil {
		return nil, fmt.Errorf("tcgdex card %s %s: %w", setCode, number, err)
	}
	return c.toCard(ctx, &ac)
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
		refs = append(refs, catalog.Ref{ID: b.ID, Name: b.Name})
		if q.Limit > 0 && len(refs) == q.Limit {
			break
		}
	}
	return refs, nil
}

// SetIDForCode maps a TCG Live set code such as "OBF" to a TCGdex set ID.
func (c *Client) SetIDForCode(ctx context.Context, code string) (string, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if alias, ok := setCodeAliases[code]; ok {
		code = alias
	}
	c.mu.Lock()
	id, ok := c.codeToSet[code]
	c.mu.Unlock()
	if ok {
		return id, nil
	}

	var sets []apiBrief
	params := url.Values{"abbreviation.official": {"eq:" + code}}
	if err := c.get(ctx, "/sets", params, &sets); err != nil {
		return "", fmt.Errorf("tcgdex set code %s: %w", code, err)
	}
	if len(sets) == 0 {
		return "", fmt.Errorf("set code %q: %w", code, catalog.ErrNotFound)
	}
	c.mu.Lock()
	c.codeToSet[code] = sets[0].ID
	c.mu.Unlock()
	return sets[0].ID, nil
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
	card.SetCode = s.Abbreviation.Official
	card.Series = s.Serie.ID
	return card, nil
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
