package game

import "errors"

var (
	ErrFinished = errors.New("game is already finished")
	ErrTurn     = errors.New("it is not your turn")
	ErrPosition = errors.New("position must be between 0 and 8")
	ErrOccupied = errors.New("position is already taken")
	ErrSymbol   = errors.New("invalid player symbol")
)

// Game represents the single game state of a tic-tac-toe match
type Game struct {
	Board    [9]string
	Turn     string
	Winner   string
	Finished bool
	moves    int
}

func New() Game {
	return Game{Turn: "X"}
}

func (g *Game) Move(symbol string, position int) error {
	if symbol != "X" && symbol != "O" {
		return ErrSymbol
	}
	if g.Finished {
		return ErrFinished
	}
	if symbol != g.Turn {
		return ErrTurn
	}
	if position < 0 || position >= len(g.Board) {
		return ErrPosition
	}
	if g.Board[position] != "" {
		return ErrOccupied
	}

	g.Board[position] = symbol
	g.moves++
	if g.hasWon(symbol) {
		g.Winner = symbol
		g.Finished = true
		return nil
	}
	if g.moves == len(g.Board) {
		g.Finished = true
		return nil
	}
	if symbol == "X" {
		g.Turn = "O"
	} else {
		g.Turn = "X"
	}
	return nil
}

func (g *Game) hasWon(symbol string) bool {
	lines := [8][3]int{
		{0, 1, 2}, {3, 4, 5}, {6, 7, 8},
		{0, 3, 6}, {1, 4, 7}, {2, 5, 8},
		{0, 4, 8}, {2, 4, 6},
	}
	for _, line := range lines {
		if g.Board[line[0]] == symbol && g.Board[line[1]] == symbol && g.Board[line[2]] == symbol {
			return true
		}
	}
	return false
}
