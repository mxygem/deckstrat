// Package justtcg is a price.Source backed by the JustTCG v1 API
// (https://justtcg.com/docs).
package justtcg

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/mxygem/deckstrat/internal/price"
)

const (
	DefaultBaseURL = "https://api.justtcg.com/v1"
	// GamePokemon is JustTCG's game ID for the English Pokémon TCG.
	GamePokemon = "pokemon"
)

type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

type Option func(*Client)

func WithBaseURL(u string) Option          { return func(c *Client) { c.baseURL = strings.TrimRight(u, "/") } }
func WithHTTPClient(h *http.Client) Option { return func(c *Client) { c.http = h } }

func New(apiKey string, opts ...Option) *Client {
	c := &Client{baseURL: DefaultBaseURL, apiKey: apiKey, http: &http.Client{Timeout: 15 * time.Second}}
	for _, o := range opts {
		o(c)
	}
	return c
}

var _ price.Source = (*Client)(nil)

// Query is a GET /cards search. Empty fields are not sent.
type Query struct {
	Q           string // free-text name search
	Game        string // defaults to GamePokemon
	Set         string // JustTCG set ID
	Number      string
	TCGPlayerID string
	Condition   []string // full names or abbreviations: NM, LP, MP, HP, DMG
	Printing    []string // Normal, Foil, ...
	Language    []string // English, Japanese, ...
	Limit       int
	Offset      int
}

func (q Query) values() url.Values {
	v := url.Values{}
	set := func(k, val string) {
		if val != "" {
			v.Set(k, val)
		}
	}
	game := q.Game
	if game == "" {
		game = GamePokemon
	}
	set("game", game)
	set("q", q.Q)
	set("set", q.Set)
	set("number", q.Number)
	set("tcgplayerId", q.TCGPlayerID)
	set("condition", strings.Join(q.Condition, ","))
	set("printing", strings.Join(q.Printing, ","))
	set("language", strings.Join(q.Language, ","))
	if q.Limit > 0 {
		v.Set("limit", strconv.Itoa(q.Limit))
	}
	if q.Offset > 0 {
		v.Set("offset", strconv.Itoa(q.Offset))
	}
	// Keep payloads small: no history or statistics.
	v.Set("include_price_history", "false")
	v.Set("include_statistics", "")
	return v
}

// APIError is an error response from JustTCG.
type APIError struct {
	Status     int
	Code       string // e.g. MISSING_API_KEY
	Message    string
	RetryAfter time.Duration // set on 429 when the server sends Retry-After
}

func (e *APIError) Error() string {
	msg := fmt.Sprintf("justtcg: HTTP %d", e.Status)
	if e.Code != "" {
		msg += " " + e.Code
	}
	if e.Message != "" {
		msg += ": " + e.Message
	}
	if e.RetryAfter > 0 {
		msg += fmt.Sprintf(" (retry after %s)", e.RetryAfter)
	}
	return msg
}

// Search calls GET /cards.
func (c *Client) Search(ctx context.Context, q Query) (*Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/cards?"+q.values().Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "deckstrat")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("justtcg: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("justtcg: reading body: %w", err)
	}
	var out Response
	decodeErr := json.Unmarshal(body, &out)

	if resp.StatusCode != http.StatusOK || out.Error != "" {
		apiErr := &APIError{Status: resp.StatusCode, Code: out.Code, Message: out.Error}
		if decodeErr != nil {
			apiErr.Message = strings.TrimSpace(string(body[:min(len(body), 256)]))
		}
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
			apiErr.RetryAfter = time.Duration(s) * time.Second
		}
		return nil, apiErr
	}
	if decodeErr != nil {
		return nil, fmt.Errorf("justtcg: decoding response: %w", decodeErr)
	}
	return &out, nil
}

// Quotes returns Near Mint English quotes, or all quotes when none are NM.
func (c *Client) Quotes(ctx context.Context, ref price.Ref) ([]price.Quote, error) {
	q := Query{TCGPlayerID: ref.TCGPlayerID, Language: []string{"English"}}
	if q.TCGPlayerID == "" {
		q.Q, q.Number = ref.Name, ref.Number
	}
	resp, err := c.Search(ctx, q)
	if err != nil {
		return nil, err
	}
	var quotes []price.Quote
	for _, card := range resp.Data {
		if ref.TCGPlayerID == "" && ref.SetName != "" && !strings.EqualFold(card.SetName, ref.SetName) {
			continue
		}
		quotes = append(quotes, ToQuotes(card)...)
	}
	return quotes, nil
}

// ToQuotes converts a card's priced variants to quotes.
func ToQuotes(card Card) []price.Quote {
	var out []price.Quote
	for _, v := range card.Variants {
		if v.Price == nil {
			continue
		}
		q := price.Quote{
			Source:    "justtcg",
			Name:      card.Name,
			Set:       card.SetName,
			Number:    card.Number,
			Printing:  v.Printing,
			Condition: v.Condition,
			Language:  v.Language,
			USD:       *v.Price,
		}
		if q.Language == "" {
			q.Language = "English"
		}
		if v.LastUpdated != nil {
			q.Updated = time.Unix(*v.LastUpdated, 0).UTC()
		}
		out = append(out, q)
	}
	return out
}
