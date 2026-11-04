package deck

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mxygem/deckstrat/internal/catalog"
)

// ExportTCGLive writes resolved entries in TCG Live's import format,
// grouped by card category and merging repeated printings.
func ExportTCGLive(entries []Entry) string {
	type row struct {
		qty  int
		text string
	}
	sections := []struct {
		title string
		cat   catalog.Category
		rows  []*row
	}{
		{"Pokémon", catalog.Pokemon, nil},
		{"Trainer", catalog.Trainer, nil},
		{"Energy", catalog.Energy, nil},
	}
	byKey := map[string]*row{}
	total := 0
	for _, e := range entries {
		text := exportLine(e.Card)
		total += e.Line.Qty
		if r, ok := byKey[text]; ok {
			r.qty += e.Line.Qty
			continue
		}
		r := &row{qty: e.Line.Qty, text: text}
		byKey[text] = r
		for i := range sections {
			if sections[i].cat == e.Card.Category {
				sections[i].rows = append(sections[i].rows, r)
			}
		}
	}

	var b strings.Builder
	for _, s := range sections {
		n := 0
		for _, r := range s.rows {
			n += r.qty
		}
		fmt.Fprintf(&b, "%s: %d\n", s.title, n)
		for _, r := range s.rows {
			fmt.Fprintf(&b, "%d %s\n", r.qty, r.text)
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "Total Cards: %d\n", total)
	return b.String()
}

// exportLine writes a card the way TCG Live does: rule names without
// catalog subtitles, "Basic {R} Energy" for basic energy, and the set code
// and number. Cards with no known TCG Live code are written by name only.
func exportLine(c *catalog.Card) string {
	name := catalog.RuleName(c.Name)
	if c.IsBasicEnergy() {
		if sym, ok := energySymbol(name); ok {
			name = "Basic {" + sym + "} Energy"
		}
	}
	if c.SetCode == "" {
		return name
	}
	return fmt.Sprintf("%s %s %s", name, c.SetCode, exportNumber(c.Number))
}

func energySymbol(name string) (string, bool) {
	typ := strings.TrimSuffix(strings.TrimPrefix(name, "Basic "), " Energy")
	for sym, t := range energySymbols {
		if t == typ {
			return sym, true
		}
	}
	return "", false
}

// exportNumber drops the catalog's zero padding: TCG Live writes "5", not
// "005". Prefixed numbers like "TG01" are kept as they are.
func exportNumber(n string) string {
	if _, err := strconv.Atoi(n); err != nil {
		return n
	}
	if t := strings.TrimLeft(n, "0"); t != "" {
		return t
	}
	return n
}
