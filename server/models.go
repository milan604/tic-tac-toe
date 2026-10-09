package server

import (
	"net"
	"sync"
	"tictactoe/game"
	"tictactoe/protocol"
)

type player struct {
	conn     net.Conn
	outgoing chan protocol.Message
	done     chan struct{}
	game     *match
	symbol   string
	closed   bool
}

type match struct {
	game    game.Game
	players [2]*player
}

// Server owns matchmaking and game state. All changes to them are made under mu.
type Server struct {
	mu       sync.Mutex
	waiting  *player
	players  map[*player]struct{}
	stopping bool
	wg       sync.WaitGroup
}
