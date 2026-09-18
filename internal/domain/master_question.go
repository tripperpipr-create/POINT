package domain

type MasterQuestion struct {
	ID      string   `json:"id"`
	Text    string   `json:"text"`
	Kind    string   `json:"kind"`
	Options []string `json:"options,omitempty"`
}
