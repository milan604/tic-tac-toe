package protocol

// Each TCP message is one JSON object followed by a newline.
type Message struct {
	Type     string     `json:"type"`
	Position *int       `json:"position,omitempty"`
	Board    *[9]string `json:"board,omitempty"`
	Symbol   string     `json:"symbol,omitempty"`
	Turn     string     `json:"turn,omitempty"`
	Winner   string     `json:"winner,omitempty"`
	Finished bool       `json:"finished,omitempty"`
	Detail   string     `json:"detail,omitempty"`
}
