package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mxygem/deckstrat/internal/catalog"
	"github.com/mxygem/deckstrat/internal/catalog/catalogtest"
)

var fake = &catalogtest.Fake{Cards: []catalog.Card{
	{ID: "me01-076", Name: "Riolu", Category: catalog.Pokemon, Stage: "Basic", Types: []string{"Fighting"}, SetCode: "MEG", Number: "076", RegulationMark: "I", Series: "me"},
	{ID: "sv03-125", Name: "Charizard ex", Category: catalog.Pokemon, Stage: "Stage2", Suffix: "ex", SetCode: "OBF", Number: "125", RegulationMark: "G", Series: "sv"},
	{ID: "sve-006", Name: "Fighting Energy", Category: catalog.Energy, EnergyType: "Normal", SetCode: "SVE", Number: "006", Series: "sv"},
}}

func TestRun(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		deck     string
		wantCode int
		wantOut  []string
	}{
		{
			name:     "legal",
			args:     []string{"-"},
			deck:     "Pokémon: 4\n4 Riolu MEG 76\nEnergy: 56\n56 Basic {F} Energy SVE 6\n",
			wantCode: 0,
			wantOut:  []string{"Standard (", "60 cards", "\nlegal\n"},
		},
		{
			name:     "illegal",
			args:     []string{"-"},
			deck:     "4 Riolu MEG 76\n1 Charizard ex OBF 125\n55 Fighting Energy SVE 6\n",
			wantCode: 1,
			wantOut:  []string{"illegal [pool]: Charizard ex (OBF 125) has regulation mark G", "not legal"},
		},
		{
			name:     "unmatched line",
			args:     []string{"-"},
			deck:     "4 Riolu MEG 76\n1 Iono PAL 185\n55 Fighting Energy SVE 6\n",
			wantCode: 1,
			wantOut:  []string{`unmatched line 2: "1 Iono PAL 185": no card PAL 185`, "deck has 59 cards"},
		},
		{
			name:     "export",
			args:     []string{"--export", "-"},
			deck:     "4 Riolu MEG 76\n56 Basic {F} Energy SVE 6\n",
			wantCode: 0,
			wantOut:  []string{"Pokémon: 4\n4 Riolu MEG 76\n", "56 Basic {F} Energy SVE 6", "Total Cards: 60"},
		},
		{name: "no file", args: nil, wantCode: 2},
		{name: "bad format", args: []string{"-f", "nope", "-"}, wantCode: 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out, errOut bytes.Buffer
			code := run(tt.args, strings.NewReader(tt.deck), &out, &errOut, fake)
			if code != tt.wantCode {
				t.Fatalf("code = %d, want %d\nstdout:\n%s\nstderr:\n%s", code, tt.wantCode, out.String(), errOut.String())
			}
			for _, w := range tt.wantOut {
				if !strings.Contains(out.String(), w) {
					t.Errorf("output missing %q:\n%s", w, out.String())
				}
			}
		})
	}
}

func TestRunJSON(t *testing.T) {
	var out bytes.Buffer
	code := run([]string{"--json", "-"}, strings.NewReader("4 Riolu MEG 76\n56 Fighting Energy SVE 6\n"), &out, &bytes.Buffer{}, fake)
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	var got struct {
		Format     string `json:"format"`
		Legal      bool   `json:"legal"`
		Violations []any  `json:"violations"`
		Unmatched  []any  `json:"unmatched"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Format != "standard" || !got.Legal || got.Violations == nil || got.Unmatched == nil {
		t.Errorf("got %+v from %s", got, out.String())
	}
}

func TestRunJSONExport(t *testing.T) {
	var out bytes.Buffer
	code := run([]string{"--json", "--export", "-"}, strings.NewReader("4 Riolu MEG 76\n56 Fighting Energy SVE 6\n"), &out, &bytes.Buffer{}, fake)
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	var got struct {
		Export string `json:"export"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not one JSON document: %v\n%s", err, out.String())
	}
	if !strings.Contains(got.Export, "4 Riolu MEG 76") {
		t.Errorf("export = %q", got.Export)
	}
}
