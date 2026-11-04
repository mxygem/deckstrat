package deck

import (
	"fmt"
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
		c := e.Card
		text := fmt.Sprintf("%s %s %s", c.Name, c.SetCode, exportNumber(c.Number))
		total += e.Line.Qty
		if r, ok := byKey[text]; ok {
			r.qty += e.Line.Qty
			continue
		}
		r := &row{qty: e.Line.Qty, text: text}
		byKey[text] = r
		for i := range sections {
			if sections[i].cat == c.Category {
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

// exportNumber drops the catalog's zero padding: TCG Live writes "5", not "005".
func exportNumber(n string) string {
	if t := strings.TrimLeft(n, "0"); t != "" {
		return t
	}
	return n
}
