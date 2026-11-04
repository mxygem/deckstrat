// Package format holds format records (card pool plus deck rules) and
// validates decks against them. Formats are data: a rotation or a ban is
// a change to formats/*.json, not to code.
package format

import (
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"slices"
	"sort"
	"strings"

	"github.com/mxygem/deckstrat/internal/catalog"
)

//go:embed formats/*.json
var formatFS embed.FS

type Format struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Version string   `json:"version"`
	Sources []string `json:"sources"`
	Pool    Pool     `json:"pool"`
	Rules   Rules    `json:"rules"`
	BanList []Ban    `json:"ban_list"`
	// BanListVerified is false until the ban list has been checked
	// against its source. Callers should warn when it is false.
	BanListVerified bool   `json:"ban_list_verified"`
	Notes           string `json:"notes"`
}

// Pool decides which printings are legal. Empty lists allow anything.
type Pool struct {
	RegulationMarks []string `json:"regulation_marks"`
	Series          []string `json:"series"`
	// ReprintsLegal lets an out-of-pool printing count as legal when a
	// functionally identical printing is in the pool.
	ReprintsLegal bool `json:"reprints_legal"`
}

type Rules struct {
	Size       int  `json:"size"`
	CopyLimit  int  `json:"copy_limit"` // per name; Basic Energy is exempt
	MaxAceSpec int  `json:"max_ace_spec"`
	SingleType bool `json:"single_type"` // every Pokémon shares a type
	NoRuleBox  bool `json:"no_rule_box"`
}

type Ban struct {
	Name      string `json:"name"`
	Effective string `json:"effective"` // YYYY-MM-DD
	Note      string `json:"note,omitempty"`
}

var registry = mustLoad()

func mustLoad() map[string]*Format {
	files, err := formatFS.ReadDir("formats")
	if err != nil {
		panic(err)
	}
	m := map[string]*Format{}
	for _, f := range files {
		b, err := formatFS.ReadFile(path.Join("formats", f.Name()))
		if err != nil {
			panic(err)
		}
		var fm Format
		dec := json.NewDecoder(strings.NewReader(string(b)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&fm); err != nil {
			panic(fmt.Sprintf("format %s: %v", f.Name(), err))
		}
		m[fm.ID] = &fm
	}
	return m
}

// Get returns the format with the given ID, e.g. "standard".
func Get(id string) (*Format, error) {
	f, ok := registry[strings.ToLower(id)]
	if !ok {
		return nil, fmt.Errorf("unknown format %q (have %s)", id, strings.Join(IDs(), ", "))
	}
	return f, nil
}

// IDs lists the known format IDs.
func IDs() []string {
	ids := make([]string, 0, len(registry))
	for id := range registry {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// PoolReason explains why a printing is outside the card pool, or returns
// "" when it is in. It ignores reprints; see Validator for those.
func (f *Format) PoolReason(c *catalog.Card) string {
	if c.IsBasicEnergy() {
		return ""
	}
	p := f.Pool
	if len(p.RegulationMarks) > 0 && !slices.Contains(p.RegulationMarks, c.RegulationMark) {
		if c.RegulationMark == "" {
			return fmt.Sprintf("has no regulation mark; %s allows %s", f.Name, strings.Join(p.RegulationMarks, ", "))
		}
		return fmt.Sprintf("has regulation mark %s; %s allows %s", c.RegulationMark, f.Name, strings.Join(p.RegulationMarks, ", "))
	}
	if len(p.Series) > 0 && !slices.Contains(p.Series, c.Series) {
		return fmt.Sprintf("is from series %q; %s allows %s", c.Series, f.Name, strings.Join(p.Series, ", "))
	}
	return ""
}
