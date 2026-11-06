package tag

import (
	"cmp"
	"slices"
	"strings"
)

// TermScore counts one term's hits across an eval set. Terms are compared
// as Tag keys, so a wrong qualifier is a miss and a false positive.
type TermScore struct {
	Term                    string
	TP, FP, FN              int
	Precision, Recall       float64
	HasPrecision, HasRecall bool // false when the denominator is 0
}

// CardDiff lists one eval card's disagreements.
type CardDiff struct {
	Card    string
	Missing []string // gold tags the tagger did not give
	Extra   []string // tags the tagger gave that are not gold
}

// Report scores a tagger against gold tags.
type Report struct {
	Terms             []TermScore
	TP, FP, FN        int
	Precision, Recall float64
	Diffs             []CardDiff // only cards with a disagreement
}

// Score compares predicted tag keys with gold keys, card by card. Cards
// missing from pred count as predicting nothing.
func Score(gold, pred map[string][]string) Report {
	terms := map[string]*TermScore{}
	term := func(key string) *TermScore {
		// "needs:energy-attached:Fire" scores under "needs:energy-attached".
		k := key
		if parts := strings.SplitN(key, ":", 3); len(parts) == 3 {
			k = parts[0] + ":" + parts[1]
		}
		if terms[k] == nil {
			terms[k] = &TermScore{Term: k}
		}
		return terms[k]
	}

	var r Report
	cards := make([]string, 0, len(gold))
	for c := range gold {
		cards = append(cards, c)
	}
	slices.Sort(cards)
	for _, card := range cards {
		g, p := gold[card], pred[card]
		d := CardDiff{Card: card}
		for _, k := range p {
			if slices.Contains(g, k) {
				term(k).TP++
				r.TP++
			} else {
				term(k).FP++
				r.FP++
				d.Extra = append(d.Extra, k)
			}
		}
		for _, k := range g {
			if !slices.Contains(p, k) {
				term(k).FN++
				r.FN++
				d.Missing = append(d.Missing, k)
			}
		}
		if len(d.Missing)+len(d.Extra) > 0 {
			slices.Sort(d.Missing)
			slices.Sort(d.Extra)
			r.Diffs = append(r.Diffs, d)
		}
	}
	r.Precision, _ = ratio(r.TP, r.TP+r.FP)
	r.Recall, _ = ratio(r.TP, r.TP+r.FN)
	for _, t := range terms {
		t.Precision, t.HasPrecision = ratio(t.TP, t.TP+t.FP)
		t.Recall, t.HasRecall = ratio(t.TP, t.TP+t.FN)
		r.Terms = append(r.Terms, *t)
	}
	slices.SortFunc(r.Terms, func(a, b TermScore) int { return cmp.Compare(a.Term, b.Term) })
	return r
}

func ratio(n, d int) (float64, bool) {
	if d == 0 {
		return 0, false
	}
	return float64(n) / float64(d), true
}
