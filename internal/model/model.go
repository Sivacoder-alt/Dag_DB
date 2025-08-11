package model

type GetNodeResponse struct {
	ID               string   `json:"id"`
	Data             string   `json:"data"`
	Parents          []string `json:"parents"`
	Weight           float64  `json:"weight"`
	CumulativeWeight float64  `json:"cumulative_weight"`
	Istip            bool     `json:"is_tip"`
}
