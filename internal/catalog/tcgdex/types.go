package tcgdex

import (
	"bytes"
	"encoding/json"
	"strconv"

	"github.com/mxygem/deckstrat/internal/catalog"
)

type apiBrief struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Image string `json:"image"`
}

type apiSet struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	ReleaseDate string `json:"releaseDate"`
	Cards       []struct {
		ID string `json:"id"`
	} `json:"cards"`
	Abbreviation struct {
		Official string `json:"official"`
	} `json:"abbreviation"`
	Serie struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"serie"`
}

type apiCard struct {
	ID       string `json:"id"`
	LocalID  string `json:"localId"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Rarity   string `json:"rarity"`
	Image    string `json:"image"`
	Set      struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"set"`

	HP         int      `json:"hp"`
	Types      []string `json:"types"`
	EvolveFrom string   `json:"evolveFrom"`
	Stage      string   `json:"stage"`
	Suffix     string   `json:"suffix"`
	Retreat    int      `json:"retreat"`
	Abilities  []struct {
		Type   string `json:"type"`
		Name   string `json:"name"`
		Effect string `json:"effect"`
	} `json:"abilities"`
	Attacks []struct {
		Name   string   `json:"name"`
		Cost   []string `json:"cost"`
		Effect string   `json:"effect"`
		Damage damage   `json:"damage"`
	} `json:"attacks"`

	TrainerType    string `json:"trainerType"`
	EnergyType     string `json:"energyType"`
	Effect         string `json:"effect"`
	RegulationMark string `json:"regulationMark"`

	ThirdParty struct {
		TCGPlayer json.Number `json:"tcgplayer"`
	} `json:"thirdParty"`
}

// damage is a JSON number (330) or string ("180+").
type damage string

func (d *damage) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if bytes.Equal(b, []byte("null")) {
		*d = ""
		return nil
	}
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*d = damage(s)
		return nil
	}
	n, err := strconv.ParseFloat(string(b), 64)
	if err != nil {
		return err
	}
	*d = damage(strconv.FormatFloat(n, 'f', -1, 64))
	return nil
}

func (ac *apiCard) toCard() *catalog.Card {
	c := &catalog.Card{
		ID:             ac.ID,
		Name:           ac.Name,
		Category:       catalog.Category(ac.Category),
		SetID:          ac.Set.ID,
		SetName:        ac.Set.Name,
		Number:         ac.LocalID,
		RegulationMark: ac.RegulationMark,
		Rarity:         ac.Rarity,
		Stage:          ac.Stage,
		Suffix:         ac.Suffix,
		EvolvesFrom:    ac.EvolveFrom,
		HP:             ac.HP,
		Types:          ac.Types,
		Retreat:        ac.Retreat,
		TrainerType:    ac.TrainerType,
		EnergyType:     ac.EnergyType,
		Text:           ac.Effect,
		TCGPlayerID:    ac.ThirdParty.TCGPlayer.String(),
		Image:          ac.Image,
	}
	c.AceSpec = legacyAceSpecs[ac.ID]
	// TCGdex leaves stage null on Sun & Moon GX and TAG TEAM cards; a
	// Pokémon that evolves from nothing is a Basic.
	if c.Category == catalog.Pokemon && c.Stage == "" && c.EvolvesFrom == "" {
		c.Stage = "Basic"
	}
	for _, a := range ac.Abilities {
		c.Abilities = append(c.Abilities, catalog.Ability{Type: a.Type, Name: a.Name, Effect: a.Effect})
	}
	for _, a := range ac.Attacks {
		c.Attacks = append(c.Attacks, catalog.Attack{Name: a.Name, Cost: a.Cost, Damage: string(a.Damage), Effect: a.Effect})
	}
	return c
}
