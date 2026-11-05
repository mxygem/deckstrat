package justtcg

// Response is the JustTCG v1 envelope. Errors use the same shape with
// Error and Code set.
type Response struct {
	Data     []Card    `json:"data"`
	Meta     *Meta     `json:"meta"`
	Metadata *Metadata `json:"_metadata"`
	Error    string    `json:"error"`
	Code     string    `json:"code"`
}

// Meta is pagination data.
type Meta struct {
	Total   int  `json:"total"`
	Limit   int  `json:"limit"`
	Offset  int  `json:"offset"`
	HasMore bool `json:"hasMore"`
}

// Metadata reports plan usage.
type Metadata struct {
	APIPlan                   string `json:"apiPlan"`
	APIRequestsRemaining      int    `json:"apiRequestsRemaining"`
	APIDailyRequestsRemaining int    `json:"apiDailyRequestsRemaining"`
	APIRateLimit              int    `json:"apiRateLimit"`
}

// Card is a JustTCG card. It carries no gameplay data.
type Card struct {
	UUID        string    `json:"uuid"`
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Game        string    `json:"game"`
	Set         string    `json:"set"`
	SetName     string    `json:"set_name"`
	Number      string    `json:"number"`
	TCGPlayerID string    `json:"tcgplayerId"`
	Rarity      string    `json:"rarity"`
	Details     *string   `json:"details"`
	Variants    []Variant `json:"variants"`
}

// Variant is one condition, printing and language of a card. Only the
// fields deckstrat uses are modeled; price statistics are ignored.
type Variant struct {
	UUID            string   `json:"uuid"`
	ID              string   `json:"id"`
	Condition       string   `json:"condition"`
	Printing        string   `json:"printing"`
	Language        string   `json:"language"` // empty means English
	TCGPlayerSkuID  string   `json:"tcgplayerSkuId"`
	Price           *float64 `json:"price"`
	LastUpdated     *int64   `json:"lastUpdated"` // unix seconds
	PriceStatus     string   `json:"priceStatus"` // current, expired, unpriced
	PriceChange24hr *float64 `json:"priceChange24hr"`
	PriceChange7d   *float64 `json:"priceChange7d"`
}
