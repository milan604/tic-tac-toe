package game

import (
	"errors"
	"testing"
)

func TestMoveRejectsInvalidMoves(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(*Game)
		symbol   string
		position int
		want     error
	}{
		{"wrong symbol", nil, "Z", 0, ErrSymbol},
		{"wrong turn", nil, "O", 0, ErrTurn},
		{"negative position", nil, "X", -1, ErrPosition},
		{"position too large", nil, "X", 9, ErrPosition},
		{"occupied", func(g *Game) { _ = g.Move("X", 0) }, "O", 0, ErrOccupied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := New()
			if tt.setup != nil {
				tt.setup(&g)
			}
			if err := g.Move(tt.symbol, tt.position); !errors.Is(err, tt.want) {
				t.Fatalf("Move() error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestMoveOutcomes(t *testing.T) {
	tests := []struct {
		name   string
		moves  []int
		winner string
	}{
		{"top row", []int{0, 3, 1, 4, 2}, "X"},
		{"middle row", []int{3, 0, 4, 1, 5}, "X"},
		{"bottom row", []int{6, 0, 7, 1, 8}, "X"},
		{"left column", []int{0, 1, 3, 2, 6}, "X"},
		{"middle column", []int{1, 0, 4, 2, 7}, "X"},
		{"right column", []int{2, 0, 5, 1, 8}, "X"},
		{"down diagonal", []int{0, 1, 4, 2, 8}, "X"},
		{"up diagonal", []int{2, 0, 4, 1, 6}, "X"},
		{"second player wins", []int{0, 3, 1, 4, 8, 5}, "O"},
		{"draw", []int{0, 1, 2, 4, 3, 5, 7, 6, 8}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := New()
			for i, position := range tt.moves {
				symbol := "X"
				if i%2 == 1 {
					symbol = "O"
				}
				if err := g.Move(symbol, position); err != nil {
					t.Fatalf("move %d: %v", i, err)
				}
			}
			if !g.Finished || g.Winner != tt.winner {
				t.Fatalf("outcome: finished=%v winner=%q, want finished=true winner=%q", g.Finished, g.Winner, tt.winner)
			}
			board := g.Board
			if err := g.Move("X", 0); !errors.Is(err, ErrFinished) {
				t.Fatalf("move after completion error = %v, want %v", err, ErrFinished)
			}
			if g.Board != board {
				t.Fatal("move after completion changed the board")
			}
		})
	}
}
