package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type runningServer struct {
	id   int
	addr string
	cmd  *exec.Cmd
	done chan error
}

type runningClient struct {
	id       int
	serverID int
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	events   chan string
	done     chan error
	alive    atomic.Bool
}

type harness struct {
	servers     []*runningServer
	clients     []*runningClient
	serverPath  string
	clientPath  string
	tempDir     string
	printMu     sync.Mutex
	clientWG    sync.WaitGroup
	interactive bool
}

func main() {
	serverCount := flag.Int("servers", 1, "number of independent server instances")
	clientCount := flag.Int("clients", 4, "initial clients per server")
	laterCount := flag.Int("later", 2, "clients to add later per server in automatic mode")
	interactive := flag.Bool("interactive", false, "control clients manually instead of playing games automatically")
	flag.Parse()
	if *serverCount < 1 || *clientCount < 0 || *laterCount < 0 {
		fmt.Fprintln(os.Stderr, "servers must be at least 1 and client counts cannot be negative")
		os.Exit(1)
	}
	if !*interactive && (*clientCount+*laterCount < 2 || (*clientCount+*laterCount)%2 != 0) {
		fmt.Fprintln(os.Stderr, "automatic mode needs an even total of at least two clients per server; use -later to pair an odd initial count")
		os.Exit(1)
	}

	h := &harness{interactive: *interactive}
	startingClients := 0
	if *interactive {
		startingClients = *clientCount
	}
	if err := h.start(*serverCount, startingClients); err != nil {
		fmt.Fprintln(os.Stderr, err)
		h.close()
		os.Exit(1)
	}
	if *interactive {
		defer h.close()
		h.printHelp()
		h.commands()
		return
	}
	if err := h.runAutomatic(*clientCount, *laterCount); err != nil {
		fmt.Fprintln(os.Stderr, err)
		h.close()
		os.Exit(1)
	}
	h.close()
}

func (h *harness) start(serverCount, clientCount int) error {
	tempDir, err := os.MkdirTemp("", "tic-tac-toe-test-")
	if err != nil {
		return err
	}
	h.tempDir = tempDir
	h.serverPath = filepath.Join(tempDir, "server")
	h.clientPath = filepath.Join(tempDir, "client")
	for _, binary := range []struct{ path, pkg string }{
		{h.serverPath, "./cmd/server"},
		{h.clientPath, "./cmd/client"},
	} {
		build := exec.Command("go", "build", "-o", binary.path, binary.pkg)
		if output, err := build.CombinedOutput(); err != nil {
			return fmt.Errorf("build %s: %w\n%s", binary.pkg, err, output)
		}
	}

	for i := 1; i <= serverCount; i++ {
		cmd := exec.Command(h.serverPath, "-addr", "127.0.0.1:0")
		ready := make(chan string, 1)
		output := &lineWriter{
			prefix:  fmt.Sprintf("[server %d] ", i),
			printMu: &h.printMu,
			onLine: func(line string) {
				if at := strings.LastIndex(line, "listening on "); at >= 0 {
					select {
					case ready <- strings.TrimSpace(line[at+len("listening on "):]):
					default:
					}
				}
			},
		}
		cmd.Stdout = output
		cmd.Stderr = output
		if err := cmd.Start(); err != nil {
			return fmt.Errorf("start server %d: %w", i, err)
		}
		s := &runningServer{
			id:   i,
			cmd:  cmd,
			done: make(chan error, 1),
		}
		go func() {
			s.done <- cmd.Wait()
			output.flush()
		}()
		select {
		case s.addr = <-ready:
		case err := <-s.done:
			return fmt.Errorf("server %d exited during startup: %v", i, err)
		case <-time.After(5 * time.Second):
			cmd.Process.Kill()
			<-s.done
			return fmt.Errorf("server %d did not start within five seconds", i)
		}
		h.servers = append(h.servers, s)
		fmt.Printf("server %d listening on %s\n", s.id, s.addr)
	}

	for _, s := range h.servers {
		if clientCount > 0 {
			if err := h.addClients(s.id, clientCount); err != nil {
				return err
			}
		}
	}
	return nil
}

func (h *harness) addClients(serverID, count int) error {
	if serverID < 1 || serverID > len(h.servers) {
		return fmt.Errorf("server must be between 1 and %d", len(h.servers))
	}
	if count < 1 {
		return fmt.Errorf("client count must be at least 1")
	}
	for i := 0; i < count; i++ {
		id := len(h.clients) + 1
		cmd := exec.Command(h.clientPath, "-addr", h.servers[serverID-1].addr)
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return err
		}
		events := make(chan string, 128)
		output := &lineWriter{
			prefix:  fmt.Sprintf("[client %d] ", id),
			printMu: &h.printMu,
			show:    h.interactive,
			onLine: func(line string) {
				select {
				case events <- strings.TrimSpace(line):
				default:
				}
			},
		}
		cmd.Stdout = output
		cmd.Stderr = output
		if err := cmd.Start(); err != nil {
			stdin.Close()
			return fmt.Errorf("start client %d: %w", id, err)
		}
		client := &runningClient{id: id, serverID: serverID, cmd: cmd, stdin: stdin, events: events, done: make(chan error, 1)}
		client.alive.Store(true)
		h.clients = append(h.clients, client)
		h.clientWG.Go(func() {
			err := cmd.Wait()
			client.alive.Store(false)
			output.flush()
			client.done <- err
			if err != nil {
				h.printf("client %d stopped: %v\n", id, err)
			}
		})
		if h.interactive {
			fmt.Printf("client %d connected to server %d\n", id, serverID)
		}
	}
	return nil
}

type autoPair struct {
	serverID int
	x        *runningClient
	o        *runningClient
}

func (h *harness) runAutomatic(initial, later int) error {
	pending := make(map[int]*runningClient)
	firstBatch, err := h.addAutomaticClients(initial, pending)
	if err != nil {
		return err
	}
	if err := h.playPairs(firstBatch); err != nil {
		return err
	}
	fmt.Printf("initial batch: passed games=%d\n", len(firstBatch))

	secondBatch, err := h.addAutomaticClients(later, pending)
	if err != nil {
		return err
	}
	if err := h.playPairs(secondBatch); err != nil {
		return err
	}
	for serverID, client := range pending {
		if client != nil {
			return fmt.Errorf("server %d still has an unmatched client %d", serverID, client.id)
		}
	}
	fmt.Printf("later batch: passed games=%d\n", len(secondBatch))
	fmt.Printf("PASS: completed games=%d, servers=%d, clients per server=%d\n",
		len(firstBatch)+len(secondBatch), len(h.servers), initial+later)
	return nil
}

func (h *harness) addAutomaticClients(count int, pending map[int]*runningClient) ([]autoPair, error) {
	var pairs []autoPair
	for _, server := range h.servers {
		for i := 0; i < count; i++ {
			if err := h.addClients(server.id, 1); err != nil {
				return nil, err
			}
			client := h.clients[len(h.clients)-1]
			if pending[server.id] == nil {
				if err := waitForEvent(client, "waiting for another player"); err != nil {
					return nil, err
				}
				pending[server.id] = client
				continue
			}
			first := pending[server.id]
			if err := waitForEvent(first, "you are X; your turn (1-9):"); err != nil {
				return nil, err
			}
			if err := waitForEvent(client, "you are O; waiting for X"); err != nil {
				return nil, err
			}
			pairs = append(pairs, autoPair{serverID: server.id, x: first, o: client})
			pending[server.id] = nil
		}
	}
	return pairs, nil
}

func (h *harness) playPairs(pairs []autoPair) error {
	errors := make(chan error, len(pairs))
	var wg sync.WaitGroup
	for _, pair := range pairs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := playPair(pair); err != nil {
				errors <- err
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

func playPair(pair autoPair) error {
	moves := []struct {
		client   *runningClient
		position int
		next     *runningClient
		prompt   string
	}{
		{pair.x, 1, pair.o, "you are O; your turn (1-9):"},
		{pair.o, 4, pair.x, "you are X; your turn (1-9):"},
		{pair.x, 2, pair.o, "you are O; your turn (1-9):"},
		{pair.o, 5, pair.x, "you are X; your turn (1-9):"},
	}
	for _, move := range moves {
		if _, err := fmt.Fprintln(move.client.stdin, move.position); err != nil {
			return fmt.Errorf("server %d client %d move %d: %w", pair.serverID, move.client.id, move.position, err)
		}
		if err := waitForEvent(move.next, move.prompt); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(pair.x.stdin, 3); err != nil {
		return fmt.Errorf("server %d client %d final move: %w", pair.serverID, pair.x.id, err)
	}
	for _, client := range []*runningClient{pair.x, pair.o} {
		if err := waitForEvent(client, "X | X | X"); err != nil {
			return err
		}
	}
	if err := waitForEvent(pair.x, "you won"); err != nil {
		return err
	}
	if err := waitForEvent(pair.o, "you lost"); err != nil {
		return err
	}
	for _, client := range []*runningClient{pair.x, pair.o} {
		select {
		case err := <-client.done:
			if err != nil {
				return fmt.Errorf("server %d client %d exited: %w", pair.serverID, client.id, err)
			}
		case <-time.After(10 * time.Second):
			return fmt.Errorf("server %d client %d did not exit after the game", pair.serverID, client.id)
		}
	}
	return nil
}

func waitForEvent(client *runningClient, expected string) error {
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	last := ""
	for {
		select {
		case line := <-client.events:
			if line == expected {
				return nil
			}
			if line == "it is not your turn" || line == "opponent disconnected" || strings.HasPrefix(line, "connection closed:") {
				return fmt.Errorf("client %d: %s while waiting for %q", client.id, line, expected)
			}
			if line != "" {
				last = line
			}
		case <-timer.C:
			return fmt.Errorf("client %d timed out waiting for %q (last output %q, running=%v)",
				client.id, expected, last, client.alive.Load())
		}
	}
}

func (h *harness) commands() {
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("> ")
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil {
				fmt.Fprintf(os.Stderr, "read command: %v\n", err)
			}
			return
		}
		args := strings.Fields(scanner.Text())
		if len(args) == 0 {
			continue
		}
		switch args[0] {
		case "add":
			if len(args) < 2 || len(args) > 3 {
				fmt.Println("usage: add <server> [count]")
				continue
			}
			serverID, err := strconv.Atoi(args[1])
			if err != nil {
				fmt.Println("server must be a number")
				continue
			}
			count := 1
			if len(args) == 3 {
				count, err = strconv.Atoi(args[2])
				if err != nil {
					fmt.Println("count must be a number")
					continue
				}
			}
			if err := h.addClients(serverID, count); err != nil {
				fmt.Println(err)
			}
		case "move":
			if len(args) != 3 {
				fmt.Println("usage: move <client> <position 1-9>")
				continue
			}
			clientID, err1 := strconv.Atoi(args[1])
			position, err2 := strconv.Atoi(args[2])
			if err1 != nil || err2 != nil || position < 1 || position > 9 {
				fmt.Println("enter a client number and a position from 1 to 9")
				continue
			}
			if clientID < 1 || clientID > len(h.clients) || !h.clients[clientID-1].alive.Load() {
				fmt.Println("client is not running")
				continue
			}
			if _, err := fmt.Fprintln(h.clients[clientID-1].stdin, position); err != nil {
				fmt.Printf("send move: %v\n", err)
			}
		case "list":
			for _, s := range h.servers {
				fmt.Printf("server %d: %s\n", s.id, s.addr)
			}
			for _, c := range h.clients {
				status := "stopped"
				if c.alive.Load() {
					status = "running"
				}
				fmt.Printf("client %d: server %d, %s\n", c.id, c.serverID, status)
			}
		case "help":
			h.printHelp()
		case "quit", "exit":
			return
		default:
			fmt.Println("unknown command; type help")
		}
	}
}

func (h *harness) printHelp() {
	fmt.Println("Commands: add <server> [count], move <client> <position 1-9>, list, help, quit")
}

func (h *harness) close() {
	for _, c := range h.clients {
		if c.alive.Load() {
			fmt.Fprintln(c.stdin, "quit")
		}
		c.stdin.Close()
	}
	clientsDone := make(chan struct{})
	go func() {
		h.clientWG.Wait()
		close(clientsDone)
	}()
	select {
	case <-clientsDone:
	case <-time.After(2 * time.Second):
		for _, c := range h.clients {
			if c.alive.Load() {
				c.cmd.Process.Kill()
			}
		}
		<-clientsDone
	}
	for _, s := range h.servers {
		s.cmd.Process.Signal(os.Interrupt)
		select {
		case err := <-s.done:
			if err != nil {
				fmt.Fprintf(os.Stderr, "server %d stopped: %v\n", s.id, err)
			}
		case <-time.After(3 * time.Second):
			s.cmd.Process.Kill()
			<-s.done
		}
	}
	if h.tempDir != "" {
		os.RemoveAll(h.tempDir)
	}
}

func (h *harness) printf(format string, args ...any) {
	h.printMu.Lock()
	defer h.printMu.Unlock()
	fmt.Printf(format, args...)
}

type lineWriter struct {
	mu      sync.Mutex
	printMu *sync.Mutex
	prefix  string
	pending []byte
	onLine  func(string)
	show    bool
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.pending = append(w.pending, p...)
	for {
		end := bytes.IndexByte(w.pending, '\n')
		if end < 0 {
			break
		}
		if w.show {
			w.printMu.Lock()
			fmt.Printf("%s%s\n", w.prefix, w.pending[:end])
			w.printMu.Unlock()
		}
		if w.onLine != nil {
			w.onLine(string(w.pending[:end]))
		}
		w.pending = w.pending[end+1:]
	}
	return len(p), nil
}

func (w *lineWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.pending) > 0 {
		if w.show {
			w.printMu.Lock()
			fmt.Printf("%s%s\n", w.prefix, w.pending)
			w.printMu.Unlock()
		}
		w.pending = nil
	}
}
