// Package deck holds decklists and converts them to and from the
// Pokémon TCG Live text format.
package deck

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// Line is one decklist row as written by the user.
type Line struct {
	Qty     int
	Name    string
	SetCode string // TCG Live code, e.g. "OBF"; empty when not given
	Number  string
	Section string // Pokémon, Trainer or Energy, from the list's headers
	LineNo  int
}

func (l Line) String() string {
	s := fmt.Sprintf("%d %s", l.Qty, l.Name)
	if l.SetCode != "" {
		s += " " + l.SetCode + " " + l.Number
	}
	return s
}

// Deck is a parsed decklist.
type Deck struct {
	Lines []Line
}

// Count is the total number of cards.
func (d Deck) Count() int {
	n := 0
	for _, l := range d.Lines {
		n += l.Qty
	}
	return n
}

// Unparsed is an input line the parser could not read.
type Unparsed struct {
	LineNo int
	Text   string
	Reason string
}

var (
	// "Pokémon: 13", "Trainer - 35", "##Energy - 12"
	sectionRe = regexp.MustCompile(`(?i)^#*\s*(pok[eé]mon|trainer|energy)\s*[:\-]\s*\d+\s*$`)
	// "4 Dreepy TWM 128", "1 Professor Sada's Vitality PAR 170"
	withSetRe = regexp.MustCompile(`^(\d+)\s+(.+?)\s+([A-Z][A-Z0-9]*(?:-[A-Z0-9]+)?)\s+([A-Za-z]*\d+[A-Za-z]*)$`)
	// "4 Arven" with no printing
	nameOnlyRe = regexp.MustCompile(`^(\d+)\s+(.+)$`)
	totalRe    = regexp.MustCompile(`(?i)^total\s+cards\s*:`)
)

// maxQty bounds a line's quantity. No deck holds more than 60 cards, and
// the bound keeps sums of quantities from overflowing.
const maxQty = 60

// ParseTCGLive reads a TCG Live (or PTCGO-style) decklist. Lines it cannot
// read are returned as Unparsed, never silently dropped.
func ParseTCGLive(r io.Reader) (Deck, []Unparsed, error) {
	var (
		d       Deck
		bad     []Unparsed
		section string
		lineNo  int
	)
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		lineNo++
		text := sc.Text()
		if lineNo == 1 {
			text = strings.TrimPrefix(text, "\uFEFF") // BOM from Windows editors
		}
		text = strings.TrimSpace(text)
		text = strings.TrimPrefix(text, "* ")
		if text == "" || totalRe.MatchString(text) {
			continue
		}
		if m := sectionRe.FindStringSubmatch(text); m != nil {
			section = canonicalSection(m[1])
			continue
		}

		var (
			l   Line
			qty string
		)
		if m := withSetRe.FindStringSubmatch(text); m != nil {
			l, qty = Line{Name: m[2], SetCode: m[3], Number: m[4]}, m[1]
		} else if m := nameOnlyRe.FindStringSubmatch(text); m != nil {
			l, qty = Line{Name: strings.TrimSpace(m[2])}, m[1]
		} else {
			bad = append(bad, Unparsed{LineNo: lineNo, Text: text, Reason: "expected \"<qty> <name> [<set code> <number>]\""})
			continue
		}
		n, err := strconv.Atoi(qty)
		if err != nil || n < 1 || n > maxQty {
			bad = append(bad, Unparsed{LineNo: lineNo, Text: text, Reason: fmt.Sprintf("quantity must be 1 to %d", maxQty)})
			continue
		}
		l.Qty = n
		l.Section, l.LineNo = section, lineNo
		d.Lines = append(d.Lines, l)
	}
	if err := sc.Err(); err != nil {
		return Deck{}, nil, err
	}
	return d, bad, nil
}

func canonicalSection(s string) string {
	switch strings.ToLower(s) {
	case "trainer":
		return "Trainer"
	case "energy":
		return "Energy"
	default:
		return "Pokémon"
	}
}

var energySymbols = map[string]string{
	"G": "Grass", "R": "Fire", "W": "Water", "L": "Lightning",
	"P": "Psychic", "F": "Fighting", "D": "Darkness", "M": "Metal",
}

var basicEnergyRe = regexp.MustCompile(`^Basic\s+(?:\{([A-Z])\}|(\w+))\s+Energy$`)

// CatalogName converts TCG Live spellings to catalog names, e.g.
// "Basic {R} Energy" -> "Fire Energy".
func CatalogName(name string) string {
	m := basicEnergyRe.FindStringSubmatch(name)
	if m == nil {
		return name
	}
	if m[1] != "" {
		if t, ok := energySymbols[m[1]]; ok {
			return t + " Energy"
		}
		return name
	}
	return m[2] + " Energy"
}
