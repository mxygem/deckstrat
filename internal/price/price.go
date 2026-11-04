// Package price defines price quotes and the interface price feeds implement.
package price

import (
	"context"
	"time"
)

// Source returns current prices for a printing.
type Source interface {
	Quotes(ctx context.Context, ref Ref) ([]Quote, error)
}

// Ref identifies a printing. TCGPlayerID is the most precise join key
// between catalog and price data; the rest is a fallback.
type Ref struct {
	TCGPlayerID string
	Name        string
	SetName     string
	Number      string
}

// Quote is one price for one condition and finish of a printing.
type Quote struct {
	Source    string    `json:"source"`
	Name      string    `json:"name"`
	Set       string    `json:"set"`
	Number    string    `json:"number"`
	Printing  string    `json:"printing"`  // Normal, Holofoil, Reverse Holofoil...
	Condition string    `json:"condition"` // Near Mint, Lightly Played...
	Language  string    `json:"language"`
	USD       float64   `json:"usd"`
	Updated   time.Time `json:"updated"`
}
