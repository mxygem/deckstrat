package tag

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"

	"github.com/mxygem/deckstrat/internal/catalog"
)

// message builds a Messages API response whose text is the given output.
func message(stop string, output any) map[string]any {
	text, _ := json.Marshal(output)
	return map[string]any{
		"id": "msg_1", "type": "message", "role": "assistant", "model": DefaultModel,
		"content":     []any{map[string]any{"type": "text", "text": string(text)}},
		"stop_reason": stop,
		"usage":       map[string]any{"input_tokens": 10, "output_tokens": 10},
	}
}

var gardevoirOutput = map[string]any{"tags": []any{
	map[string]any{"kind": "provides", "term": "energy-accel-discard", "qualifier": "Psychic", "confidence": 0.95, "reason": "Psychic Embrace attaches Psychic Energy from the discard."},
	map[string]any{"kind": "needs", "term": "energy-in-discard", "qualifier": "", "confidence": 0.9, "reason": "Psychic Embrace needs Energy in the discard."},
	map[string]any{"kind": "provides", "term": "draw", "qualifier": "Fire", "confidence": 0.5, "reason": "invalid: draw takes no qualifier"},
}}

type fakeAPI struct {
	t        *testing.T
	mu       sync.Mutex
	requests []map[string]any // decoded bodies of message and batch requests
	gets     int
	reply    map[string]any
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	batch := map[string]any{"id": "msgbatch_1", "type": "message_batch", "processing_status": "in_progress",
		"request_counts": map[string]any{"processing": 2, "succeeded": 0, "errored": 0, "canceled": 0, "expired": 0}}
	switch {
	case r.Method == http.MethodPost && r.URL.Path == "/v1/messages":
		f.record(r)
		json.NewEncoder(w).Encode(f.reply)
	case r.Method == http.MethodPost && r.URL.Path == "/v1/messages/batches":
		f.record(r)
		json.NewEncoder(w).Encode(batch)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/messages/batches/msgbatch_1":
		f.gets++
		if f.gets > 1 {
			batch["processing_status"] = "ended"
		}
		json.NewEncoder(w).Encode(batch)
	case r.Method == http.MethodGet && r.URL.Path == "/v1/messages/batches/msgbatch_1/results":
		reqs := f.requests[len(f.requests)-1]["requests"].([]any)
		first := reqs[0].(map[string]any)["custom_id"].(string)
		second := reqs[1].(map[string]any)["custom_id"].(string)
		lines := []any{
			// Results come back in any order.
			map[string]any{"custom_id": second, "result": map[string]any{"type": "errored", "error": map[string]any{"type": "error", "error": map[string]any{"type": "overloaded_error", "message": "overloaded"}}}},
			map[string]any{"custom_id": first, "result": map[string]any{"type": "succeeded", "message": message("end_turn", gardevoirOutput)}},
		}
		for _, l := range lines {
			b, _ := json.Marshal(l)
			fmt.Fprintf(w, "%s\n", b)
		}
	default:
		f.t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}
}

func (f *fakeAPI) record(r *http.Request) {
	b, _ := io.ReadAll(r.Body)
	var body map[string]any
	if err := json.Unmarshal(b, &body); err != nil {
		f.t.Error(err)
	}
	f.requests = append(f.requests, body)
}

func newClaude(t *testing.T, reply map[string]any) (*Claude, *fakeAPI) {
	t.Helper()
	api := &fakeAPI{t: t, reply: reply}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	client := anthropic.NewClient(option.WithBaseURL(srv.URL), option.WithAPIKey("test"), option.WithMaxRetries(0))
	return &Claude{Client: client, PollEvery: time.Millisecond}, api
}

func TestClaudeTag(t *testing.T) {
	cards := loadCards(t)
	gardevoir := cards["sv01-086"]
	l, api := newClaude(t, message("end_turn", gardevoirOutput))

	tags, err := l.Tag(context.Background(), gardevoir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"needs:energy-in-discard", "provides:energy-accel-discard:Psychic"}
	if got := keys(tags); !slices.Equal(got, want) {
		t.Errorf("tags = %v, want %v (the invalid draw tag is dropped)", got, want)
	}
	for _, tg := range tags {
		if tg.Source != LLM || tg.Reason == "" {
			t.Errorf("%s: want source llm and a reason, got %+v", tg.Key(), tg)
		}
	}

	req := api.requests[0]
	if req["model"] != DefaultModel {
		t.Errorf("model = %v", req["model"])
	}
	system := req["system"].([]any)[0].(map[string]any)
	if system["cache_control"] == nil || !strings.Contains(system["text"].(string), "energy-accel-discard") {
		t.Error("system prompt should carry the vocabulary and a cache breakpoint")
	}
	format := req["output_config"].(map[string]any)["format"].(map[string]any)
	if format["type"] != "json_schema" || format["schema"] == nil {
		t.Errorf("output_config.format = %v", format)
	}
	user := req["messages"].([]any)[0].(map[string]any)["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(user, "Psychic Embrace") || !strings.Contains(user, "Stage: Stage2") {
		t.Errorf("user message should describe the card:\n%s", user)
	}
}

func TestClaudeRefusalAndBadOutput(t *testing.T) {
	c := loadCards(t)["me01-131"]
	l, _ := newClaude(t, message("refusal", map[string]any{}))
	var refusal *RefusalError
	if _, err := l.Tag(context.Background(), c); !errors.As(err, &refusal) {
		t.Errorf("want a RefusalError, got %v", err)
	}

	allBad := map[string]any{"tags": []any{map[string]any{"kind": "provides", "term": "teleport", "qualifier": "", "confidence": 1, "reason": "x"}}}
	l, _ = newClaude(t, message("end_turn", allBad))
	if _, err := l.Tag(context.Background(), c); err == nil || !strings.Contains(err.Error(), "every tag was invalid") {
		t.Errorf("want an invalid-tags error, got %v", err)
	}

	l, _ = newClaude(t, message("end_turn", map[string]any{"tags": []any{}}))
	if tags, err := l.Tag(context.Background(), c); err != nil || len(tags) != 0 {
		t.Errorf("an empty list is a valid answer: %v %v", tags, err)
	}
}

func TestClaudeBatch(t *testing.T) {
	cards := loadCards(t)
	gardevoir, ball := cards["sv01-086"], cards["me01-131"]
	l, api := newClaude(t, nil)
	var progressCalls int
	l.Progress = func(anthropic.MessageBatchRequestCounts) { progressCalls++ }

	var submitted string
	results, err := l.Batch(context.Background(), []*catalog.Card{gardevoir, ball}, func(id string) { submitted = id })
	if err != nil {
		t.Fatal(err)
	}
	if submitted != "msgbatch_1" || progressCalls != 2 {
		t.Errorf("submitted = %q, progress calls = %d", submitted, progressCalls)
	}
	if len(results) != 2 || results[0].Card != gardevoir || results[1].Card != ball {
		t.Fatalf("results should follow input order: %+v", results)
	}
	if results[0].Err != nil || len(results[0].Tags) != 2 {
		t.Errorf("gardevoir = %+v", results[0])
	}
	if results[1].Err == nil || !strings.Contains(results[1].Err.Error(), "overloaded") {
		t.Errorf("ultra ball should carry the batch error, got %v", results[1].Err)
	}
	reqs := api.requests[0]["requests"].([]any)
	if id := reqs[0].(map[string]any)["custom_id"]; id != KeyOf(gardevoir) {
		t.Errorf("custom_id = %v, want the store key", id)
	}
}
