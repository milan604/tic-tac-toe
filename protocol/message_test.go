package protocol

import (
	"encoding/json"
	"testing"
)

func TestMessageOptionalFields(t *testing.T) {
	zero := 0
	board := [9]string{}
	tests := []struct {
		name         string
		message      Message
		wantBoard    bool
		wantPosition bool
	}{
		{"waiting has no board", Message{Type: "waiting"}, false, false},
		{"move zero keeps position", Message{Type: "move", Position: &zero}, false, true},
		{"empty state keeps board", Message{Type: "state", Board: &board}, true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.message)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(data, &fields); err != nil {
				t.Fatal(err)
			}
			_, hasBoard := fields["board"]
			_, hasPosition := fields["position"]
			if hasBoard != tt.wantBoard || hasPosition != tt.wantPosition {
				t.Fatalf("encoded %s: board=%v position=%v", data, hasBoard, hasPosition)
			}
		})
	}
}
