package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"tictactoe/protocol"
)

type testClient struct {
	conn    net.Conn
	decoder *json.Decoder
	encoder *json.Encoder
}

func startServer(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- New().Serve(ctx, listener) }()
	t.Cleanup(func() {
		cancel()
		listener.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("server stopped: %v", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("server did not stop")
		}
	})
	return listener.Addr().String()
}

func connect(t *testing.T, addr string) *testClient {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return &testClient{conn: conn, decoder: json.NewDecoder(conn), encoder: json.NewEncoder(conn)}
}

func (c *testClient) read(t *testing.T, want string) protocol.Message {
	t.Helper()
	c.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var msg protocol.Message
	if err := c.decoder.Decode(&msg); err != nil {
		t.Fatalf("read %s: %v", want, err)
	}
	if msg.Type != want {
		t.Fatalf("message type = %q, want %q: %+v", msg.Type, want, msg)
	}
	return msg
}

func (c *testClient) move(t *testing.T, position int) {
	t.Helper()
	if err := c.encoder.Encode(protocol.Message{Type: "move", Position: &position}); err != nil {
		t.Fatal(err)
	}
}

func TestMatchAndMoves(t *testing.T) {
	addr := startServer(t)
	x := connect(t, addr)
	x.read(t, "waiting")
	o := connect(t, addr)
	if msg := x.read(t, "state"); msg.Symbol != "X" || msg.Turn != "X" {
		t.Fatalf("first player state: %+v", msg)
	}
	if msg := o.read(t, "state"); msg.Symbol != "O" || msg.Turn != "X" {
		t.Fatalf("second player state: %+v", msg)
	}

	o.move(t, 0)
	if msg := o.read(t, "error"); msg.Detail != "it is not your turn" {
		t.Fatalf("wrong turn response: %+v", msg)
	}
	x.move(t, 0)
	if msg := x.read(t, "state"); msg.Board[0] != "X" || msg.Turn != "O" {
		t.Fatalf("move response: %+v", msg)
	}
	o.read(t, "state")
	o.move(t, 0)
	o.read(t, "error")

	for _, move := range []struct {
		client   *testClient
		position int
	}{
		{o, 3}, {x, 1}, {o, 4}, {x, 2},
	} {
		move.client.move(t, move.position)
		xState := x.read(t, "state")
		oState := o.read(t, "state")
		if move.position == 2 {
			if !xState.Finished || xState.Winner != "X" || !oState.Finished {
				t.Fatalf("expected X to win: x=%+v o=%+v", xState, oState)
			}
		}
	}
	for _, client := range []*testClient{x, o} {
		client.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		var msg protocol.Message
		if err := client.decoder.Decode(&msg); err != io.EOF {
			t.Fatalf("finished client read error = %v, want EOF", err)
		}
	}
}

func TestDisconnectQueuesOpponentAgain(t *testing.T) {
	addr := startServer(t)
	first := connect(t, addr)
	first.read(t, "waiting")
	second := connect(t, addr)
	first.read(t, "state")
	second.read(t, "state")
	first.conn.Close()
	second.read(t, "opponent_left")
	second.read(t, "waiting")
	third := connect(t, addr)
	if msg := second.read(t, "state"); msg.Symbol != "X" {
		t.Fatalf("requeued player state: %+v", msg)
	}
	if msg := third.read(t, "state"); msg.Symbol != "O" {
		t.Fatalf("new player state: %+v", msg)
	}
}

func TestManyClients(t *testing.T) {
	addr := startServer(t)
	for range 50 {
		first := connect(t, addr)
		first.read(t, "waiting")
		second := connect(t, addr)
		first.read(t, "state")
		second.read(t, "state")
	}
}

func TestBadMessage(t *testing.T) {
	addr := startServer(t)
	client := connect(t, addr)
	client.read(t, "waiting")
	if _, err := client.conn.Write([]byte("not-json\n")); err != nil {
		t.Fatal(err)
	}
	client.read(t, "error")
	if err := client.encoder.Encode(protocol.Message{Type: "move"}); err != nil {
		t.Fatal(err)
	}
	client.read(t, "error")
}

func TestQueuedStateUsesBoardSnapshot(t *testing.T) {
	s := New()
	first := &player{outgoing: make(chan protocol.Message, 2)}
	second := &player{outgoing: make(chan protocol.Message, 2)}
	m := &match{players: [2]*player{first, second}}

	s.sendStateLocked(m)
	m.game.Board[4] = "X"
	s.sendStateLocked(m)

	before := <-first.outgoing
	after := <-first.outgoing
	if before.Board == nil || after.Board == nil || before.Board[4] != "" || after.Board[4] != "X" {
		t.Fatalf("queued boards changed: before=%+v after=%+v", before.Board, after.Board)
	}
}

func TestOversizedMessageClosesConnection(t *testing.T) {
	addr := startServer(t)
	client := connect(t, addr)
	client.read(t, "waiting")
	if _, err := client.conn.Write([]byte(strings.Repeat("x", 5000) + "\n")); err != nil {
		t.Fatal(err)
	}
	client.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var msg protocol.Message
	err := client.decoder.Decode(&msg)
	if err == nil {
		t.Fatal("oversized message left the connection open")
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		t.Fatalf("oversized message did not close the connection: %v", err)
	}
}

type gatedReadConn struct {
	net.Conn
	gate <-chan struct{}
}

func (c *gatedReadConn) Read(b []byte) (int, error) {
	<-c.gate
	return c.Conn.Read(b)
}

func TestDisconnectedWaitingPlayerIsRequeued(t *testing.T) {
	s := New()
	gate := make(chan struct{})
	released := false
	var clients []net.Conn
	defer func() {
		if !released {
			close(gate)
		}
		for _, conn := range clients {
			conn.Close()
		}
		s.wg.Wait()
	}()
	connectPipe := func(serverConn, clientConn net.Conn) *testClient {
		clients = append(clients, clientConn)
		s.wg.Add(1)
		go s.handleConn(serverConn)
		return &testClient{conn: clientConn, decoder: json.NewDecoder(clientConn), encoder: json.NewEncoder(clientConn)}
	}

	firstServer, firstConn := net.Pipe()
	first := connectPipe(&gatedReadConn{Conn: firstServer, gate: gate}, firstConn)
	first.read(t, "waiting")
	firstConn.Close() // The reader cannot observe this until the gate opens.

	secondServer, secondConn := net.Pipe()
	second := connectPipe(secondServer, secondConn)
	second.read(t, "state") // It was temporarily paired with the stale player.
	close(gate)
	released = true
	second.read(t, "opponent_left")
	second.read(t, "waiting")

	thirdServer, thirdConn := net.Pipe()
	third := connectPipe(thirdServer, thirdConn)
	if msg := second.read(t, "state"); msg.Symbol != "X" {
		t.Fatalf("requeued player state: %+v", msg)
	}
	if msg := third.read(t, "state"); msg.Symbol != "O" {
		t.Fatalf("new player state: %+v", msg)
	}
}

type failingListener struct {
	err    error
	closed bool
}

func (l *failingListener) Accept() (net.Conn, error) { return nil, l.err }
func (l *failingListener) Close() error              { l.closed = true; return nil }
func (l *failingListener) Addr() net.Addr            { return &net.TCPAddr{} }

func TestServeClosesListenerOnAcceptError(t *testing.T) {
	want := errors.New("accept failed")
	listener := &failingListener{err: want}
	if err := New().Serve(context.Background(), listener); !errors.Is(err, want) {
		t.Fatalf("Serve() error = %v, want %v", err, want)
	}
	if !listener.closed {
		t.Fatal("listener was not closed")
	}
}

func TestShutdownClosesActiveClients(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer listener.Close()
	done := make(chan error, 1)
	go func() { done <- New().Serve(ctx, listener) }()

	first := connect(t, listener.Addr().String())
	first.read(t, "waiting")
	second := connect(t, listener.Addr().String())
	first.read(t, "state")
	second.read(t, "state")

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("server stopped: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not stop")
	}
	for _, client := range []*testClient{first, second} {
		client.conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		var msg protocol.Message
		if err := client.decoder.Decode(&msg); err != io.EOF {
			t.Fatalf("client %s read error = %v, want EOF after shutdown", client.conn.RemoteAddr(), err)
		}
	}
}

func TestShutdownRejectsLateRegistration(t *testing.T) {
	s := New()
	s.mu.Lock()
	s.stopping = true
	s.mu.Unlock()
	serverConn, clientConn := net.Pipe()
	defer serverConn.Close()
	defer clientConn.Close()
	done := make(chan struct{})
	s.wg.Add(1)
	go func() {
		s.handleConn(serverConn)
		close(done)
	}()

	clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	var b [1]byte
	if _, err := clientConn.Read(b[:]); err != io.EOF {
		t.Fatalf("late connection read error = %v, want EOF", err)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("late connection handler did not finish")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.players) != 0 || s.waiting != nil {
		t.Fatalf("late connection was registered: players=%d waiting=%v", len(s.players), s.waiting)
	}
}
