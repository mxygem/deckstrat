package tag

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go/v2"

	"github.com/mxygem/deckstrat/internal/catalog"
)

// DefaultModel tags cards. Override with Claude.Model.
const DefaultModel = "claude-opus-5-5"

// PromptVersion changes whenever the prompt or schema changes, so tags
// can be traced to the prompt that made them.
const PromptVersion = "2026-11-05"

// Claude tags cards with the Claude API. Use Tag for a few cards and Batch for a
// whole pool (half price, results within a day, usually minutes).
type Claude struct {
	Client anthropic.Client
	Model  string                       // "" means DefaultModel
	Effort anthropic.OutputConfigEffort // "" means medium
	// PollEvery is how often Batch checks a running batch. Zero means 30s.
	PollEvery time.Duration
	// Progress, if set, gets batch status while Batch waits.
	Progress func(counts anthropic.MessageBatchRequestCounts)
}

// RefusalError means Claude declined to tag a card.
type RefusalError struct{ Card string }

func (e *RefusalError) Error() string { return fmt.Sprintf("claude declined to tag %s", e.Card) }

// Tag implements Tagger with one request.
func (l *Claude) Tag(ctx context.Context, c *catalog.Card) ([]Tag, error) {
	msg, err := l.Client.Messages.New(ctx, l.params(c))
	if err != nil {
		return nil, fmt.Errorf("tagging %s: %w", c.Name, err)
	}
	return parseResponse(c, msg)
}

// BatchResult is one card's outcome in a batch.
type BatchResult struct {
	Card  *catalog.Card
	Tags  []Tag
	Usage anthropic.Usage // set when the request succeeded
	Err   error
}

// Batch tags many cards through the Message Batches API and waits for
// the results. onSubmit, if set, gets the batch ID as soon as it exists,
// so a caller can save it and resume with Results after an interruption.
func (l *Claude) Batch(ctx context.Context, cards []*catalog.Card, onSubmit func(id string)) ([]BatchResult, error) {
	reqs := make([]anthropic.MessageBatchNewParamsRequest, len(cards))
	for i, c := range cards {
		p := l.params(c)
		reqs[i] = anthropic.MessageBatchNewParamsRequest{
			CustomID: KeyOf(c),
			Params: anthropic.MessageBatchNewParamsRequestParams{
				Model:        p.Model,
				MaxTokens:    p.MaxTokens,
				System:       p.System,
				Messages:     p.Messages,
				OutputConfig: p.OutputConfig,
			},
		}
	}
	batch, err := l.Client.Messages.Batches.New(ctx, anthropic.MessageBatchNewParams{Requests: reqs})
	if err != nil {
		return nil, fmt.Errorf("submitting batch: %w", err)
	}
	if onSubmit != nil {
		onSubmit(batch.ID)
	}
	return l.Results(ctx, batch.ID, cards)
}

// Results waits for a submitted batch and matches its results to cards
// by store key. Cards with no result come back with an error.
func (l *Claude) Results(ctx context.Context, batchID string, cards []*catalog.Card) ([]BatchResult, error) {
	poll := l.PollEvery
	if poll == 0 {
		poll = 30 * time.Second
	}
	for {
		b, err := l.Client.Messages.Batches.Get(ctx, batchID, anthropic.MessageBatchGetParams{})
		if err != nil {
			return nil, fmt.Errorf("checking batch %s: %w", batchID, err)
		}
		if l.Progress != nil {
			l.Progress(b.RequestCounts)
		}
		if b.ProcessingStatus == anthropic.MessageBatchProcessingStatusEnded {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(poll):
		}
	}

	byKey := map[string]*catalog.Card{}
	for _, c := range cards {
		byKey[KeyOf(c)] = c
	}
	results := map[string]BatchResult{}
	stream := l.Client.Messages.Batches.ResultsStreaming(ctx, batchID, anthropic.MessageBatchResultsParams{})
	defer stream.Close()
	for stream.Next() {
		r := stream.Current()
		c, ok := byKey[r.CustomID]
		if !ok {
			continue
		}
		res := BatchResult{Card: c}
		switch r.Result.Type {
		case "succeeded":
			res.Usage = r.Result.Message.Usage
			res.Tags, res.Err = parseResponse(c, &r.Result.Message)
		case "errored":
			res.Err = fmt.Errorf("tagging %s: %s", c.Name, r.Result.Error.Error.Message)
		default:
			res.Err = fmt.Errorf("tagging %s: request %s", c.Name, r.Result.Type)
		}
		results[r.CustomID] = res
	}
	if err := stream.Err(); err != nil {
		return nil, fmt.Errorf("reading batch %s results: %w", batchID, err)
	}

	out := make([]BatchResult, 0, len(cards))
	for _, c := range cards {
		r, ok := results[KeyOf(c)]
		if !ok {
			r = BatchResult{Card: c, Err: fmt.Errorf("batch %s has no result for %s", batchID, c.Name)}
		}
		out = append(out, r)
	}
	return out, nil
}

func (l *Claude) params(c *catalog.Card) anthropic.MessageNewParams {
	model := l.Model
	if model == "" {
		model = DefaultModel
	}
	effort := l.Effort
	if effort == "" {
		effort = anthropic.OutputConfigEffortMedium
	}
	return anthropic.MessageNewParams{
		Model:     anthropic.Model(model),
		MaxTokens: 8000,
		System: []anthropic.TextBlockParam{{
			Text:         systemPrompt,
			CacheControl: anthropic.NewCacheControlEphemeralParam(),
		}},
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(DescribeCard(c))),
		},
		OutputConfig: anthropic.OutputConfigParam{
			Effort: effort,
			Format: anthropic.JSONOutputFormatParam{Schema: outputSchema},
		},
	}
}

type llmOutput struct {
	Tags []struct {
		Kind       Kind    `json:"kind"`
		Term       string  `json:"term"`
		Qualifier  string  `json:"qualifier"`
		Confidence float64 `json:"confidence"`
		Reason     string  `json:"reason"`
	} `json:"tags"`
}

// parseResponse reads tags from a structured-output response. Tags that
// break the vocabulary are dropped; if every tag is bad that is an error.
func parseResponse(c *catalog.Card, msg *anthropic.Message) ([]Tag, error) {
	switch msg.StopReason {
	case anthropic.StopReasonRefusal:
		return nil, &RefusalError{Card: c.Name}
	case anthropic.StopReasonMaxTokens:
		return nil, fmt.Errorf("tagging %s: response hit max_tokens", c.Name)
	}
	var text strings.Builder
	for _, b := range msg.Content {
		if t, ok := b.AsAny().(anthropic.TextBlock); ok {
			text.WriteString(t.Text)
		}
	}
	var out llmOutput
	if err := json.Unmarshal([]byte(text.String()), &out); err != nil {
		return nil, fmt.Errorf("tagging %s: reading response: %w", c.Name, err)
	}
	tags := make([]Tag, 0, len(out.Tags))
	var bad []error
	for _, t := range out.Tags {
		tg := Tag{Kind: t.Kind, Term: t.Term, Qualifier: t.Qualifier, Source: LLM, Confidence: t.Confidence, Reason: t.Reason}
		if err := Vocab().Check(tg); err != nil {
			bad = append(bad, err)
			continue
		}
		tags = append(tags, tg)
	}
	if len(tags) == 0 && len(bad) > 0 {
		return nil, fmt.Errorf("tagging %s: every tag was invalid: %w", c.Name, errors.Join(bad...))
	}
	return Merge(tags), nil
}

// DescribeCard renders a card's gameplay text for the prompt.
func DescribeCard(c *catalog.Card) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Name: %s\nCategory: %s\n", c.Name, c.Category)
	if c.Category == catalog.Pokemon {
		if c.Stage != "" {
			fmt.Fprintf(&b, "Stage: %s\n", c.Stage)
		}
		if c.EvolvesFrom != "" {
			fmt.Fprintf(&b, "Evolves from: %s\n", c.EvolvesFrom)
		}
		if c.Suffix != "" {
			fmt.Fprintf(&b, "Suffix: %s\n", c.Suffix)
		}
		fmt.Fprintf(&b, "HP: %d\nTypes: %s\nRetreat: %d\n", c.HP, strings.Join(c.Types, ", "), c.Retreat)
		for _, a := range c.Abilities {
			fmt.Fprintf(&b, "Ability (%s) %s: %s\n", a.Type, a.Name, a.Effect)
		}
		for _, a := range c.Attacks {
			fmt.Fprintf(&b, "Attack %s [%s] %s", a.Name, strings.Join(a.Cost, ", "), a.Damage)
			if a.Effect != "" {
				fmt.Fprintf(&b, ": %s", a.Effect)
			}
			b.WriteString("\n")
		}
	}
	if c.TrainerType != "" {
		fmt.Fprintf(&b, "Trainer type: %s\n", c.TrainerType)
	}
	if c.EnergyType != "" {
		fmt.Fprintf(&b, "Energy type: %s\n", c.EnergyType)
	}
	if c.IsAceSpec() {
		b.WriteString("ACE SPEC\n")
	}
	if c.Text != "" {
		fmt.Fprintf(&b, "Text: %s\n", c.Text)
	}
	return b.String()
}

var systemPrompt = buildSystemPrompt()

func buildSystemPrompt() string {
	var b strings.Builder
	b.WriteString(`You tag Pokémon TCG cards for a deck-building tool. For the card in the user message, list what it NEEDS to work well and what it PROVIDES to a deck, using only the vocabulary below.

How the tags are used: a deck builder starts from one card, collects its needs, and looks for cards whose provides meet them. So tag what matters for building a deck, not every word on the card.

Guidance:
- "provides" is something the card does for you: searching, drawing, attaching Energy, switching, and so on. Tag an attack's effect as a provide only if a deck would play the card for that effect.
- "needs" is a resource or support the card depends on: Energy of a type to attack, Energy in the discard pile for an attack that scales with it, a big Bench, evolution support for a Stage 2.
- energy-attached needs a qualifier: the Energy type the card's main attack needs (Grass, Fire, Water, Lightning, Psychic, Fighting, Darkness, Metal, Dragon, Fairy). Skip Colorless. Use one tag per type.
- energy-accel-* and search-energy take a qualifier when they only work with one Energy type; leave it empty otherwise.
- stadium-in-play takes the Stadium's name as its qualifier when the card names one.
- Other terms take no qualifier: use "".
- confidence is how sure you are the tag is right and useful, from 0 to 1.
- reason is one short sentence quoting or paraphrasing the card text the tag comes from.
- A vanilla card with nothing worth tagging gets an empty list.

Vocabulary (id, category: description):
`)
	for _, t := range Vocab().Terms {
		fmt.Fprintf(&b, "- %s, %s: %s", t.ID, t.Category, t.Description)
		if t.Qualified {
			b.WriteString(" Takes a qualifier.")
		}
		b.WriteString("\n")
	}
	return b.String()
}

var outputSchema = buildSchema()

func buildSchema() map[string]any {
	terms := make([]any, len(Vocab().Terms))
	for i, t := range Vocab().Terms {
		terms[i] = t.ID
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []any{"tags"},
		"properties": map[string]any{
			"tags": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []any{"kind", "term", "qualifier", "confidence", "reason"},
					"properties": map[string]any{
						"kind":       map[string]any{"type": "string", "enum": []any{"needs", "provides"}},
						"term":       map[string]any{"type": "string", "enum": terms},
						"qualifier":  map[string]any{"type": "string"},
						"confidence": map[string]any{"type": "number"},
						"reason":     map[string]any{"type": "string"},
					},
				},
			},
		},
	}
}
