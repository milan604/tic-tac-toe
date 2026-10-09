package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"log"
	"net"
	"time"

	"tictactoe/game"
	"tictactoe/protocol"
)

func New() *Server {
	return &Server{players: make(map[*player]struct{})}
}

func (s *Server) Serve(ctx context.Context, listener net.Listener) error {
	defer listener.Close()
	stop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			listener.Close()
		case <-stop:
		}
	}()
	defer close(stop)
	defer func() {
		s.mu.Lock()
		s.stopping = true
		connections := make([]net.Conn, 0, len(s.players))
		for p := range s.players {
			connections = append(connections, p.conn)
		}
		s.mu.Unlock()
		for _, conn := range connections {
			conn.Close()
		}
		s.wg.Wait()
	}()

	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		s.wg.Add(1)
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer s.wg.Done()
	p := &player{
		conn:     conn,
		outgoing: make(chan protocol.Message, 16),
		done:     make(chan struct{}),
	}
	s.mu.Lock()
	if s.stopping {
		s.mu.Unlock()
		conn.Close()
		return
	}
	s.players[p] = struct{}{}
	s.pairLocked(p)
	s.mu.Unlock()

	writerDone := make(chan struct{})
	go func() {
		writeMessages(p)
		close(writerDone)
	}()

	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 0, 256), 4096)
	for scanner.Scan() {
		var msg protocol.Message
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			s.sendError(p, "invalid JSON message")
			continue
		}
		if msg.Type != "move" || msg.Position == nil {
			s.sendError(p, "expected a move with a position from 0 to 8")
			continue
		}
		s.move(p, *msg.Position)
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, net.ErrClosed) {
		log.Printf("read from %s: %v", conn.RemoteAddr(), err)
	}

	s.disconnect(p)
	conn.Close()
	<-writerDone
}

func writeMessages(p *player) {
	defer p.conn.Close()
	encoder := json.NewEncoder(p.conn)
	for {
		select {
		case <-p.done:
			return
		case msg := <-p.outgoing:
			p.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := encoder.Encode(msg); err != nil {
				return
			}
			if msg.Type == "state" && msg.Finished {
				return
			}
		}
	}
}

func (s *Server) pairLocked(p *player) {
	if s.waiting == nil {
		s.waiting = p
		s.queueLocked(p, protocol.Message{Type: "waiting", Detail: "waiting for another player"})
		return
	}

	opponent := s.waiting
	s.waiting = nil
	m := &match{game: game.New(), players: [2]*player{opponent, p}}
	opponent.game, opponent.symbol = m, "X"
	p.game, p.symbol = m, "O"
	s.sendStateLocked(m)
}

func (s *Server) move(p *player, position int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.closed {
		return
	}
	if p.game == nil {
		s.queueLocked(p, protocol.Message{Type: "error", Detail: "waiting for an opponent"})
		return
	}
	if err := p.game.game.Move(p.symbol, position); err != nil {
		s.queueLocked(p, protocol.Message{Type: "error", Detail: err.Error()})
		return
	}
	s.sendStateLocked(p.game)
}

func (s *Server) sendStateLocked(m *match) {
	board := m.game.Board // Keep the queued state independent of later moves
	for _, p := range m.players {
		s.queueLocked(p, protocol.Message{
			Type:     "state",
			Board:    &board,
			Symbol:   p.symbol,
			Turn:     m.game.Turn,
			Winner:   m.game.Winner,
			Finished: m.game.Finished,
		})
	}
}

func (s *Server) sendError(p *player, detail string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queueLocked(p, protocol.Message{Type: "error", Detail: detail})
}

func (s *Server) queueLocked(p *player, msg protocol.Message) {
	if p.closed {
		return
	}

	select {
	case p.outgoing <- msg:
	default:
		p.conn.Close() // The reader removes this client and requeues its opponent if the server is running.
	}
}

func (s *Server) disconnect(p *player) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.closed {
		return
	}
	p.closed = true
	close(p.done)
	delete(s.players, p)
	if s.waiting == p {
		s.waiting = nil
	}
	if p.game == nil || p.game.game.Finished || s.stopping {
		return
	}

	for _, opponent := range p.game.players {
		if opponent == p || opponent.closed {
			continue
		}
		opponent.game = nil
		opponent.symbol = ""
		s.queueLocked(opponent, protocol.Message{Type: "opponent_left", Detail: "opponent disconnected"})
		s.pairLocked(opponent)
	}
}
