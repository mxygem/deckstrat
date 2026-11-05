package justtcg

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/mxygem/deckstrat/internal/price"
)

func TestSearch(t *testing.T) {
	var got *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		b, err := os.ReadFile("testdata/cards_fire_energy.json")
		if err != nil {
			t.Fatal(err)
		}
		w.Write(b)
	}))
	defer srv.Close()

	c := New("tcg_test", WithBaseURL(srv.URL))
	resp, err := c.Search(context.Background(), Query{
		Q:         "Fire Energy",
		Condition: []string{"NM", "LP"},
		Language:  []string{"English"},
		Limit:     5,
	})
	if err != nil {
		t.Fatal(err)
	}

	if k := got.Header.Get("x-api-key"); k != "tcg_test" {
		t.Errorf("x-api-key = %q", k)
	}
	if got.URL.Path != "/cards" {
		t.Errorf("path = %s", got.URL.Path)
	}
	want := url.Values{
		"game":                  {"pokemon"},
		"q":                     {"Fire Energy"},
		"condition":             {"NM,LP"},
		"language":              {"English"},
		"limit":                 {"5"},
		"include_price_history": {"false"},
		"include_statistics":    {""},
	}
	if q := got.URL.Query(); q.Encode() != want.Encode() {
		t.Errorf("query = %s\nwant    %s", q.Encode(), want.Encode())
	}

	if len(resp.Data) != 1 || resp.Meta == nil || resp.Metadata.APIPlan != "Free" {
		t.Fatalf("resp = %+v", resp)
	}
	card := resp.Data[0]
	if card.TCGPlayerID != "219042" || card.Details != nil || len(card.Variants) != 2 {
		t.Errorf("card = %+v", card)
	}
	if p := card.Variants[0].Price; p == nil || *p != 4.99 {
		t.Errorf("price = %v", p)
	}

	quotes := ToQuotes(card)
	if len(quotes) != 1 {
		t.Fatalf("unpriced variant should be skipped: %+v", quotes)
	}
	wantQ := price.Quote{Source: "justtcg", Name: card.Name, Set: "Battle Academy", Number: "N/A", Printing: "Normal",
		Condition: "Near Mint", Language: "English", USD: 4.99, Updated: time.Unix(1743100261, 0).UTC()}
	if quotes[0] != wantQ {
		t.Errorf("quote = %+v\nwant  %+v", quotes[0], wantQ)
	}
}

func TestSearchErrors(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		body       string
		retryAfter string
		wantCode   string
		wantRetry  time.Duration
	}{
		{name: "missing key", status: 401, body: `{"error":"API key is required","code":"MISSING_API_KEY"}`, wantCode: "MISSING_API_KEY"},
		{name: "rate limited", status: 429, body: `{"error":"Too many requests","code":"RATE_LIMIT_EXCEEDED"}`, retryAfter: "30", wantCode: "RATE_LIMIT_EXCEEDED", wantRetry: 30 * time.Second},
		{name: "non-json", status: 502, body: `<html>bad gateway</html>`},
		{name: "200 with error", status: 200, body: `{"data":null,"error":"bad param","code":"INVALID_PARAM"}`, wantCode: "INVALID_PARAM"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.retryAfter != "" {
					w.Header().Set("Retry-After", tt.retryAfter)
				}
				w.WriteHeader(tt.status)
				w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			_, err := New("k", WithBaseURL(srv.URL)).Search(context.Background(), Query{Q: "x"})
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("err = %v, want *APIError", err)
			}
			if apiErr.Status != tt.status || apiErr.Code != tt.wantCode || apiErr.RetryAfter != tt.wantRetry {
				t.Errorf("err = %+v", apiErr)
			}
			if apiErr.Message == "" {
				t.Error("message should be set")
			}
		})
	}
}

func TestQuotesUsesTCGPlayerID(t *testing.T) {
	var q url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q = r.URL.Query()
		w.Write([]byte(`{"data":[]}`))
	}))
	defer srv.Close()

	c := New("k", WithBaseURL(srv.URL))
	if _, err := c.Quotes(context.Background(), price.Ref{TCGPlayerID: "219042", Name: "ignored"}); err != nil {
		t.Fatal(err)
	}
	if q.Get("tcgplayerId") != "219042" || q.Has("q") {
		t.Errorf("query = %v", q)
	}
}
