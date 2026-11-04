// Package catalog defines card data and the interface catalog providers
// implement. Gameplay data (stage, regulation mark, text) comes from here;
// prices come from package price.
package catalog

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"unicode"
)

// ErrNotFound is returned when a card or set does not exist.
var ErrNotFound = errors.New("not found")

// Catalog looks up cards.
type Catalog interface {
	// Get returns one printing by provider ID, e.g. "sv03-125".
	Get(ctx context.Context, id string) (*Card, error)
	// GetByCode returns a printing by TCG Live set code and collector
	// number, e.g. ("OBF", "125").
	GetByCode(ctx context.Context, setCode, number string) (*Card, error)
	// Search returns brief matches; call Get for full data.
	Search(ctx context.Context, q Query) ([]Ref, error)
}

// Query filters a Search. Empty fields are ignored.
type Query struct {
	Name string
	// Exact matches Name exactly instead of as a substring.
	Exact bool
	SetID string
	Limit int
}

// Ref is a brief search result.
type Ref struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Series string `json:"series,omitempty"` // when the provider knows it
}

// seriesOrder lists series newest first.
var seriesOrder = []string{"me", "sv", "swsh", "sm", "xy", "bw", "col", "hgss", "pl", "dp", "pop", "ex", "ecard", "neo", "gym", "base"}

// SeriesRank orders series by age: higher is newer, unknown is 0.
func SeriesRank(series string) int {
	for i, s := range seriesOrder {
		if s == series {
			return len(seriesOrder) - i
		}
	}
	return 0
}

type Category string

const (
	Pokemon Category = "Pokemon"
	Trainer Category = "Trainer"
	Energy  Category = "Energy"
)

// Card is one printing along with its functional (gameplay) data.
//
// Three identities matter: Name (copy limits), FunctionalKey (legality via
// reprints, simulation) and ID (prices, ownership).
type Card struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Category Category `json:"category"`

	SetID   string `json:"set_id"`
	SetCode string `json:"set_code,omitempty"` // TCG Live code, e.g. "OBF"; empty when unknown
	SetName string `json:"set_name,omitempty"`
	Series  string `json:"series,omitempty"`
	Number  string `json:"number"`

	RegulationMark string `json:"regulation_mark,omitempty"`
	Rarity         string `json:"rarity,omitempty"`
	// AceSpec is set by providers that know a card is an ACE SPEC even
	// when its rarity does not say so (Black & White era printings).
	AceSpec bool `json:"ace_spec,omitempty"`

	Stage       string    `json:"stage,omitempty"`  // Basic, Stage1, Stage2, VMAX...
	Suffix      string    `json:"suffix,omitempty"` // ex, V, GX...
	EvolvesFrom string    `json:"evolves_from,omitempty"`
	HP          int       `json:"hp,omitempty"`
	Types       []string  `json:"types,omitempty"`
	Abilities   []Ability `json:"abilities,omitempty"`
	Attacks     []Attack  `json:"attacks,omitempty"`
	Retreat     int       `json:"retreat,omitempty"`

	TrainerType string `json:"trainer_type,omitempty"` // Item, Supporter, Tool, Stadium
	EnergyType  string `json:"energy_type,omitempty"`  // Normal (basic) or Special
	Text        string `json:"text,omitempty"`         // Trainer and Energy effect text

	TCGPlayerID string `json:"tcgplayer_id,omitempty"`
	Image       string `json:"image,omitempty"`
}

type Ability struct {
	Type   string `json:"type"`
	Name   string `json:"name"`
	Effect string `json:"effect"`
}

type Attack struct {
	Name   string   `json:"name"`
	Cost   []string `json:"cost,omitempty"`
	Damage string   `json:"damage,omitempty"` // "180+", "30×", "120"
	Effect string   `json:"effect,omitempty"`
}

// IsBasicEnergy reports whether the card is exempt from copy limits.
func (c *Card) IsBasicEnergy() bool {
	return c.Category == Energy && strings.EqualFold(c.EnergyType, "Normal")
}

// IsBasicPokemon reports whether the card can start the game.
func (c *Card) IsBasicPokemon() bool {
	return c.Category == Pokemon && c.Stage == "Basic"
}

// IsAceSpec reports whether the card is an ACE SPEC.
func (c *Card) IsAceSpec() bool {
	return c.AceSpec || strings.HasPrefix(strings.ToUpper(c.Rarity), "ACE SPEC")
}

// IsRadiant reports whether the card is a Radiant Pokémon (max 1 per deck).
func (c *Card) IsRadiant() bool {
	return c.Category == Pokemon && strings.HasPrefix(c.Name, "Radiant ")
}

// IsPrismStar reports whether the card is a Prism Star card (max 1 per name).
func (c *Card) IsPrismStar() bool {
	return strings.Contains(c.Name, "◇") || strings.Contains(c.Name, "Prism Star")
}

var (
	ruleBoxSuffixes = map[string]bool{"ex": true, "EX": true, "GX": true, "TAG TEAM-GX": true, "V": true, "Legend": true}
	ruleBoxStages   = map[string]bool{"VMAX": true, "VSTAR": true, "V-UNION": true, "BREAK": true}
)

// IsRuleBox reports whether the card has a Rule Box: ex, V, VMAX, VSTAR,
// GX, EX, Radiant, Prism Star, BREAK, LEGEND and similar.
func (c *Card) IsRuleBox() bool {
	if c.Category != Pokemon {
		return false
	}
	return ruleBoxSuffixes[c.Suffix] || ruleBoxStages[c.Stage] || c.IsRadiant() || c.IsPrismStar()
}

// FunctionalKey identifies the card for legality, ignoring printing
// details. Two printings with the same key are treated as the same card.
//
// Effect text is left out on purpose: reprints often modernize wording
// without changing play (SWSH and SV Boss's Orders read differently in
// the catalog). The key uses what identifies a card in practice instead:
// name, kind, HP, types, attack names, costs and damage, and ability
// names. Two same-name cards that differ only in effect text would match;
// that has not been seen in practice.
func (c *Card) FunctionalKey() string {
	var b strings.Builder
	add := func(s string) { b.WriteString(normalize(s)); b.WriteByte('|') }
	add(RuleName(c.Name))
	add(string(c.Category))
	add(c.TrainerType)
	add(c.EnergyType)
	if c.IsAceSpec() {
		add("acespec")
	}
	if c.Category == Pokemon {
		add(strconv.Itoa(c.HP))
		add(strings.Join(c.Types, ","))
		for _, a := range c.Abilities {
			add(a.Name)
		}
		for _, a := range c.Attacks {
			add(a.Name)
			add(strings.Join(a.Cost, ","))
			add(a.Damage)
		}
	}
	return b.String()
}

// normalize lower-cases and drops everything but letters and digits so
// punctuation and accent-free spellings ("Pokemon") still match.
func normalize(s string) string {
	s = strings.ReplaceAll(s, "é", "e")
	s = strings.ReplaceAll(s, "É", "e")
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, s)
}

// SameName reports whether two names match, ignoring case, accents and
// punctuation. Use it for matching user input, not for copy limits.
func SameName(a, b string) bool { return normalize(a) == normalize(b) }

// RuleName is the card's name for game rules such as copy limits. It drops
// the subtitle catalogs add to tell versions apart:
// "Boss's Orders (Giovanni)" is named "Boss's Orders". Case matters, since
// "Charizard EX" and "Charizard ex" are different names.
func RuleName(name string) string {
	name = strings.TrimSpace(name)
	if strings.HasSuffix(name, ")") {
		if i := strings.LastIndex(name, " ("); i > 0 {
			return name[:i]
		}
	}
	return name
}
