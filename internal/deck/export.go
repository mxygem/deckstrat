package deck

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mxygem/deckstrat/internal/catalog"
)

// ExportTCGLive writes resolved entries in TCG Live's format, grouped by
// card category and merging repeated printings. Lines imported with a set
// code keep their original text, since TCG Live's names and numbers can
// differ from the catalog's (it numbers MEE energy 9-16, TCGdex 1-8).
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
	for _, e := range entries {
		text := exportLine(e)
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
	return strings.TrimSuffix(b.String(), "\n")
}

// exportLine writes one card as TCG Live does: "4 Dreepy TWM 128",
// with basic energy as "Psychic Energy MEE 13". Lines that came in with a
// set code are written back unchanged. Cards with no known TCG Live code
// are written by name only.
func exportLine(e Entry) string {
	if e.Line.SetCode != "" && e.Line.Number != "" {
		return fmt.Sprintf("%s %s %s", e.Line.Name, e.Line.SetCode, e.Line.Number)
	}
	c := e.Card
	name := catalog.RuleName(c.Name)
	if c.SetCode == "" {
		return name
	}
	return fmt.Sprintf("%s %s %s", name, c.SetCode, exportNumber(c.Number))
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
