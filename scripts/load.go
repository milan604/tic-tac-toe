//go:build ignore

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"tictactoe/protocol"
)

type loadClient struct {
	conn    net.Conn
	decoder *json.Decoder
	encoder *json.Encoder
	symbol  string
}

func main() {
	clients := flag.Int("clients", 1000, "clients in the first batch")
	later := flag.Int("later", 2, "clients added while the server is still running")
	parallel := flag.Int("parallel", 100, "maximum simultaneous connection attempts")
	addr := flag.String("addr", "", "existing server address; empty starts a separate server process")
	flag.Parse()
	if *clients < 2 || *clients%2 != 0 || *later < 0 || *later%2 != 0 || *parallel < 1 {
		fmt.Fprintln(os.Stderr, "clients must be an even number of at least 2; later must be even and nonnegative; parallel must be positive")
		os.Exit(1)
	}
	if err := run(*clients, *later, *parallel, *addr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(initial, later, parallel int, addr string) error {
	if addr == "" {
		var stop func()
		var err error
		addr, stop, err = startServerProcess()
		if err != nil {
			return err
		}
		defer stop()
	}
	return runBatches(addr, initial, later, parallel)
}

func startServerProcess() (string, func(), error) {
	dir, err := os.MkdirTemp("", "tic-tac-toe-load-")
	if err != nil {
		return "", nil, err
	}
	binary := filepath.Join(dir, "server")
	build := exec.Command("go", "build", "-o", binary, "./cmd/server")
	if output, err := build.CombinedOutput(); err != nil {
		os.RemoveAll(dir)
		return "", nil, fmt.Errorf("build server: %w\n%s", err, output)
	}
	cmd := exec.Command(binary, "-addr", "127.0.0.1:0")
	stderr, err := cmd.StderrPipe()
	if err != nil {
		os.RemoveAll(dir)
		return "", nil, err
	}
	ready := make(chan string, 1)
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			line := scanner.Text()
			if at := strings.LastIndex(line, "listening on "); at >= 0 {
				ready <- strings.TrimSpace(line[at+len("listening on "):])
			} else {
				fmt.Fprintln(os.Stderr, "[server]", line)
			}
		}
	}()
	if err := cmd.Start(); err != nil {
		os.RemoveAll(dir)
		return "", nil, err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	stop := func() {
		cmd.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			cmd.Process.Kill()
			<-done
		}
		os.RemoveAll(dir)
	}
	select {
	case addr := <-ready:
		fmt.Printf("server listening on %s\n", addr)
		return addr, stop, nil
	case err := <-done:
		os.RemoveAll(dir)
		return "", nil, fmt.Errorf("server exited during startup: %v", err)
	case <-time.After(5 * time.Second):
		stop()
		return "", nil, fmt.Errorf("server did not start within five seconds")
	}
}

func runBatches(addr string, initial, later, parallel int) error {
	if err := runBatch(addr, initial, parallel, "initial"); err != nil {
		return err
	}
	if later > 0 {
		if err := runBatch(addr, later, parallel, "later"); err != nil {
			return err
		}
	}
	fmt.Printf("PASS: clients=%d, later=%d, completed games=%d\n", initial, later, (initial+later)/2)
	return nil
}

func runBatch(addr string, count, parallel int, name string) error {
	started := time.Now()
	clients, err := joinBatch(addr, count, parallel)
	if err != nil {
		return fmt.Errorf("%s batch join: %w", name, err)
	}
	defer closeClients(clients)
	joinTime := time.Since(started)
	if err := playBatch(clients); err != nil {
		return fmt.Errorf("%s batch game: %w", name, err)
	}
	fmt.Printf("%s batch: clients=%d, games=%d, joined=%s, total=%s\n",
		name, count, count/2, joinTime.Round(time.Millisecond), time.Since(started).Round(time.Millisecond))
	return nil
}

func joinBatch(addr string, count, parallel int) ([]*loadClient, error) {
	clients := make([]*loadClient, count)
	limit := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	errors := make(chan error, count)
	for i := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			limit <- struct{}{}
			conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
			<-limit
			if err != nil {
				errors <- fmt.Errorf("client %d dial: %w", i+1, err)
				return
			}
			client := &loadClient{conn: conn, decoder: json.NewDecoder(conn), encoder: json.NewEncoder(conn)}
			clients[i] = client
			for {
				msg, err := client.read()
				if err != nil {
					errors <- fmt.Errorf("client %d join: %w", i+1, err)
					return
				}
				if msg.Type == "waiting" {
					continue
				}
				if msg.Type != "state" || msg.Board == nil || *msg.Board != ([9]string{}) ||
					(msg.Symbol != "X" && msg.Symbol != "O") || msg.Turn != "X" || msg.Finished {
					errors <- fmt.Errorf("client %d received invalid starting state: %+v", i+1, msg)
					return
				}
				client.symbol = msg.Symbol
				return
			}
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		matched := 0
		for _, client := range clients {
			if client != nil && client.symbol != "" {
				matched++
			}
		}
		closeClients(clients)
		return nil, fmt.Errorf("matched %d/%d clients: %w", matched, count, err)
	}
	x, o := 0, 0
	for _, client := range clients {
		if client.symbol == "X" {
			x++
		} else {
			o++
		}
	}
	if x != count/2 || o != count/2 {
		closeClients(clients)
		return nil, fmt.Errorf("symbol counts X=%d O=%d, want %d each", x, o, count/2)
	}
	return clients, nil
}

func playBatch(clients []*loadClient) error {
	var board [9]string
	for _, move := range []struct {
		symbol   string
		position int
	}{
		{"X", 0}, {"O", 3}, {"X", 1}, {"O", 4}, {"X", 2},
	} {
		position := move.position
		if err := forEachClient(clients, func(client *loadClient) error {
			if client.symbol != move.symbol {
				return nil
			}
			client.conn.SetWriteDeadline(time.Now().Add(20 * time.Second))
			return client.encoder.Encode(protocol.Message{Type: "move", Position: &position})
		}); err != nil {
			return fmt.Errorf("send %s move %d: %w", move.symbol, position, err)
		}
		board[position] = move.symbol
		finished := position == 2
		turn := "X"
		if move.symbol == "X" {
			turn = "O"
		}
		if err := forEachClient(clients, func(client *loadClient) error {
			msg, err := client.read()
			if err != nil {
				return err
			}
			if msg.Type != "state" || msg.Board == nil || *msg.Board != board || msg.Finished != finished {
				return fmt.Errorf("unexpected state: %+v", msg)
			}
			if finished {
				if msg.Winner != "X" {
					return fmt.Errorf("winner = %q, want X", msg.Winner)
				}
			} else if msg.Turn != turn {
				return fmt.Errorf("turn = %q, want %s", msg.Turn, turn)
			}
			return nil
		}); err != nil {
			return fmt.Errorf("receive %s move %d: %w", move.symbol, position, err)
		}
	}
	return forEachClient(clients, func(client *loadClient) error {
		_, err := client.read()
		if !errors.Is(err, io.EOF) {
			return fmt.Errorf("after final state: got %v, want EOF", err)
		}
		return nil
	})
}

func forEachClient(clients []*loadClient, fn func(*loadClient) error) error {
	var wg sync.WaitGroup
	errors := make(chan error, len(clients))
	for i, client := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := fn(client); err != nil {
				errors <- fmt.Errorf("client %d: %w", i+1, err)
			}
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		return err
	}
	return nil
}

func (c *loadClient) read() (protocol.Message, error) {
	c.conn.SetReadDeadline(time.Now().Add(20 * time.Second))
	var msg protocol.Message
	err := c.decoder.Decode(&msg)
	return msg, err
}

func closeClients(clients []*loadClient) {
	for _, client := range clients {
		if client != nil {
			client.conn.Close()
		}
	}
}
