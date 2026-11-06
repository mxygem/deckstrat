package tag

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/mxygem/deckstrat/internal/catalog"
)

// Rules tags cards from their stats and text with fixed patterns. It is
// free and deterministic but only finds common wordings; the LLM tagger
// covers the rest.
type Rules struct{}

type textRule struct {
	kind Kind
	term string
	re   *regexp.Regexp
	// not rejects a sentence the pattern matched.
	not *regexp.Regexp
	// qualify takes the Energy type from the matched sentence.
	qualify bool
	conf    float64
}

const pkmn = `pok[eé]mon`

// Patterns run on one lower-cased sentence at a time.
var textRules = []textRule{
	{kind: Provides, term: "search-pokemon", conf: 0.8,
		re:  regexp.MustCompile(`search your deck for [^.]*?` + pkmn),
		not: regexp.MustCompile(`for [^.]*?(energy|` + pkmn + ` tool|item|supporter|stadium)`)},
	{kind: Provides, term: "search-trainer", conf: 0.8,
		re: regexp.MustCompile(`search your deck for [^.]*?(item|supporter|stadium|` + pkmn + ` tool|trainer)`)},
	{kind: Provides, term: "energy-accel-deck", conf: 0.8, qualify: true,
		re: regexp.MustCompile(`search your deck for [^.]*?energy[^.]*attach`)},
	{kind: Provides, term: "search-energy", conf: 0.7, qualify: true,
		re:  regexp.MustCompile(`search your deck for [^.]*?energy`),
		not: regexp.MustCompile(`attach`)},
	{kind: Provides, term: "energy-accel-discard", conf: 0.8, qualify: true,
		re: regexp.MustCompile(`attach [^.]*?energy[^.]*?from your discard pile`)},
	{kind: Provides, term: "energy-accel-hand", conf: 0.7, qualify: true,
		re: regexp.MustCompile(`attach [^.]*?energy (card )?from your hand`)},
	{kind: Provides, term: "energy-move", conf: 0.8,
		re: regexp.MustCompile(`move [^.]*?energy from 1 of your ` + pkmn)},
	{kind: Provides, term: "draw", conf: 0.8,
		re:  regexp.MustCompile(`\bdraws? (a card|\d+ cards|cards|up to \d+ cards|that many cards)`),
		not: regexp.MustCompile(`opponent draws`)},
	{kind: Provides, term: "evolution-accel", conf: 0.8,
		re: regexp.MustCompile(`(skip(ping)? the stage 1|evolve [^.]*?(during (your|their) first turn|the turn you play))`)},
	{kind: Provides, term: "switch", conf: 0.8,
		re: regexp.MustCompile(`(switch your active ` + pkmn + ` with 1 of your benched|switch this ` + pkmn + ` with 1 of your benched|retreat cost [^.]*?(less|is 0|no retreat)|has no retreat cost)`)},
	{kind: Provides, term: "gust", conf: 0.85,
		re: regexp.MustCompile(`switch (in )?1 of your opponent'?s benched ` + pkmn)},
	{kind: Provides, term: "heal", conf: 0.8,
		re:  regexp.MustCompile(`heal (\d+ damage|all damage)`),
		not: regexp.MustCompile(`opponent'?s`)},
	{kind: Provides, term: "hand-disruption", conf: 0.75,
		re: regexp.MustCompile(`(opponent (shuffles|discards) [^.]*?hand|each player shuffles their hand|opponent reveals their hand)`)},
	{kind: Provides, term: "energy-disruption", conf: 0.8,
		re: regexp.MustCompile(`(discard|move) [^.]*?energy (card )?(attached to|from) [^.]*?opponent`)},
	{kind: Provides, term: "spread-damage", conf: 0.75,
		re: regexp.MustCompile(`(damage (counters )?(to|on) (each of |1 of )?your opponent'?s benched|damage counters on your opponent'?s ` + pkmn + ` in any way)`)},
	{kind: Provides, term: "discard-from-hand", conf: 0.7,
		re: regexp.MustCompile(`discard ([^.]*?from your hand|your hand)`)},
	{kind: Provides, term: "recover-pokemon", conf: 0.75,
		re: regexp.MustCompile(`(put|shuffle) [^.]*?` + pkmn + `[^.]*?from your discard pile`)},
	{kind: Provides, term: "recover-energy", conf: 0.75,
		re:  regexp.MustCompile(`(put|shuffle) [^.]*?energy[^.]*?from your discard pile`),
		not: regexp.MustCompile(`attach`)},
	{kind: Provides, term: "recover-trainer", conf: 0.75,
		re: regexp.MustCompile(`(put|shuffle) [^.]*?(item|supporter|trainer)[^.]*?from your discard pile`)},
	{kind: Provides, term: "bench-expand", conf: 0.85,
		re: regexp.MustCompile(`(can|may) have up to \d+ ` + pkmn + ` on (their|your) bench`)},
	{kind: Provides, term: "ability-lock", conf: 0.8,
		re: regexp.MustCompile(pkmn + `[^.]*?have no abilities`)},
	{kind: Provides, term: "item-lock", conf: 0.85,
		re: regexp.MustCompile(`can'?t play any item cards`)},
	{kind: Provides, term: "damage-prevention", conf: 0.7,
		re:  regexp.MustCompile(`(prevent all damage|takes \d+ less damage)`),
		not: regexp.MustCompile(`benched`)},
	{kind: Provides, term: "bench-protection", conf: 0.8,
		re: regexp.MustCompile(`prevent all damage [^.]*?benched`)},

	{kind: Needs, term: "energy-in-discard", conf: 0.85,
		re: regexp.MustCompile(`for each [^.]*?energy (card )?in your discard pile`)},
	{kind: Needs, term: "cards-in-hand", conf: 0.85,
		re: regexp.MustCompile(`for each card in your hand`)},
	{kind: Needs, term: "bench-size", conf: 0.85,
		re: regexp.MustCompile(`for each (of )?(your |your opponent'?s )?benched ` + pkmn)},
	{kind: Needs, term: "damage-counters", conf: 0.85,
		re: regexp.MustCompile(`for each damage counter on (this|your)`)},
	{kind: Needs, term: "pokemon-in-discard", conf: 0.85,
		re: regexp.MustCompile(`for each ` + pkmn + `[^.]*?in your discard pile`)},
	{kind: Needs, term: "prizes-behind", conf: 0.8,
		re: regexp.MustCompile(`(prize cards? your opponent has taken|if your opponent has taken|more prize cards remaining than)`)},
	{kind: Needs, term: "tools-attached", conf: 0.8,
		re: regexp.MustCompile(`(has a ` + pkmn + ` tool attached|for each ` + pkmn + ` tool)`)},
}

var energyTypes = []string{"Grass", "Fire", "Water", "Lightning", "Psychic", "Fighting", "Darkness", "Metal", "Dragon", "Fairy"}

// EnergyTypes lists the Basic Energy types.
func EnergyTypes() []string { return slices.Clone(energyTypes) }

// energySymbols are the type symbols card text uses, as in "Basic {F} Energy".
var energySymbols = map[string]string{
	"Grass": "{g}", "Fire": "{r}", "Water": "{w}", "Lightning": "{l}", "Psychic": "{p}",
	"Fighting": "{f}", "Darkness": "{d}", "Metal": "{m}", "Dragon": "{n}", "Fairy": "{y}",
}

var sentenceEnd = regexp.MustCompile(`\.\s+`)

// Tag implements Tagger.
func (Rules) Tag(_ context.Context, c *catalog.Card) ([]Tag, error) {
	var tags []Tag
	add := func(kind Kind, term, qualifier string, conf float64, reason string) {
		tags = append(tags, Tag{Kind: kind, Term: term, Qualifier: qualifier, Source: Rule, Confidence: conf, Reason: reason})
	}

	if c.Category == catalog.Pokemon {
		switch c.Stage {
		case "Stage2":
			add(Needs, "evolution-accel", "", 0.8, "Stage 2 Pokémon")
			add(Needs, "search-pokemon", "", 0.8, "Stage 2 Pokémon")
		case "Stage1":
			add(Needs, "search-pokemon", "", 0.6, "Stage 1 Pokémon")
		}
		tags = append(tags, attackCostTags(c)...)
	}

	for _, src := range textSources(c) {
		for _, s := range sentenceEnd.Split(strings.ToLower(src.text), -1) {
			for _, r := range textRules {
				m := r.re.FindString(s)
				if m == "" || (r.not != nil && r.not.MatchString(m)) {
					continue
				}
				q := ""
				if r.qualify {
					q = energyType(s)
				}
				reason := fmt.Sprintf("%s: %q", src.label, m)
				add(r.kind, r.term, q, r.conf, reason)
				if r.term == "energy-accel-discard" {
					add(Needs, "energy-in-discard", "", 0.75, reason)
				}
			}
		}
	}
	return Merge(tags), nil
}

// attackCostTags turns attack costs into Energy needs: one per type, with
// higher confidence for attacks costing 3 or more. An all-Colorless attack
// costing 2 or more gives an untyped need.
func attackCostTags(c *catalog.Card) []Tag {
	var tags []Tag
	for _, a := range c.Attacks {
		counts := map[string]int{}
		for _, e := range a.Cost {
			if e != "Colorless" {
				counts[e]++
			}
		}
		if len(counts) == 0 && len(a.Cost) >= 2 {
			tags = append(tags, Tag{Kind: Needs, Term: "energy-attached", Source: Rule, Confidence: 0.6,
				Reason: fmt.Sprintf("%s costs %d Colorless Energy", a.Name, len(a.Cost))})
		}
		for t, n := range counts {
			conf := 0.7
			if len(a.Cost) >= 3 {
				conf = 0.9
			}
			tags = append(tags, Tag{Kind: Needs, Term: "energy-attached", Qualifier: t, Source: Rule, Confidence: conf,
				Reason: fmt.Sprintf("%s costs %d %s Energy (%d total)", a.Name, n, t, len(a.Cost))})
		}
	}
	return tags
}

type textSource struct{ label, text string }

func textSources(c *catalog.Card) []textSource {
	var out []textSource
	if c.Text != "" {
		out = append(out, textSource{"card text", c.Text})
	}
	for _, a := range c.Abilities {
		out = append(out, textSource{"ability " + a.Name, a.Effect})
	}
	for _, a := range c.Attacks {
		if a.Effect != "" {
			out = append(out, textSource{"attack " + a.Name, a.Effect})
		}
	}
	return out
}

// energyType finds the one Energy type a sentence names, or "".
func energyType(s string) string {
	found := ""
	for _, t := range energyTypes {
		if strings.Contains(s, strings.ToLower(t)+" energy") || strings.Contains(s, energySymbols[t]+" energy") {
			if found != "" {
				return ""
			}
			found = t
		}
	}
	return found
}
