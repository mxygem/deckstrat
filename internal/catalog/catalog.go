// Package catalog defines card data and the interface catalog providers
// implement. Gameplay data (stage, regulation mark, text) comes from here;
// prices come from package price.
package catalog

import (
	"context"
	"errors"
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
	ID   string `json:"id"`
	Name string `json:"name"`
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
	SetCode string `json:"set_code,omitempty"` // TCG Live code, e.g. "OBF"
	SetName string `json:"set_name,omitempty"`
	Series  string `json:"series,omitempty"`
	Number  string `json:"number"`

	RegulationMark string `json:"regulation_mark,omitempty"`
	Rarity         string `json:"rarity,omitempty"`

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
	return strings.HasPrefix(strings.ToUpper(c.Rarity), "ACE SPEC")
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
	return ruleBoxSuffixes[c.Suffix] || ruleBoxStages[c.Stage] ||
		strings.HasPrefix(c.Name, "Radiant ") || strings.Contains(c.Name, "◇")
}

// FunctionalKey identifies the card's gameplay text, ignoring printing
// details. Two printings with the same key are the same card for legality.
func (c *Card) FunctionalKey() string {
	var b strings.Builder
	add := func(s string) { b.WriteString(normalize(s)); b.WriteByte('|') }
	add(RuleName(c.Name))
	add(string(c.Category))
	add(c.Stage)
	add(c.TrainerType)
	add(c.Text)
	for _, a := range c.Abilities {
		add(a.Name)
		add(a.Effect)
	}
	for _, a := range c.Attacks {
		add(a.Name)
		add(strings.Join(a.Cost, ","))
		add(a.Damage)
		add(a.Effect)
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
