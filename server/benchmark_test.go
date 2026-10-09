package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"tictactoe/protocol"
)

func BenchmarkClientsJoin(b *testing.B) {
	if runtime.GOOS == "windows" {
		b.Skip("Unix sockets are not available on Windows")
	}
	for _, count := range []int{10, 100} {
		b.Run(fmt.Sprintf("clients_%d", count), func(b *testing.B) {
			b.StopTimer()
			dir, err := os.MkdirTemp("", "ttt-bench-")
			if err != nil {
				b.Fatal(err)
			}
			defer os.RemoveAll(dir)
			listener, err := net.Listen("unix", filepath.Join(dir, "join.sock"))
			if err != nil {
				b.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			s := New()
			done := make(chan error, 1)
			go func() { done <- s.Serve(ctx, listener) }()
			defer func() {
				cancel()
				if err := <-done; err != nil {
					b.Errorf("stop server: %v", err)
				}
			}()

			initial := count / 2
			if initial%2 != 0 {
				initial--
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				clients := make([]net.Conn, count)
				b.StartTimer()
				firstErr := joinClients(listener.Addr().String(), clients[:initial])
				if firstErr == nil {
					firstErr = joinClients(listener.Addr().String(), clients[initial:])
				}
				b.StopTimer()
				for _, conn := range clients {
					if unix, ok := conn.(*net.UnixConn); ok {
						unix.CloseWrite()
					}
				}
				cleanupErr := waitForNoPlayers(s)
				for _, conn := range clients {
					if conn != nil {
						conn.SetReadDeadline(time.Now().Add(5 * time.Second))
						io.Copy(io.Discard, conn)
						conn.Close()
					}
				}
				if firstErr != nil {
					b.Fatal(firstErr)
				}
				if cleanupErr != nil {
					b.Fatal(cleanupErr)
				}
			}
			b.ReportMetric(float64(count*b.N)/b.Elapsed().Seconds(), "clients/s")
		})
	}
}

func joinClients(addr string, clients []net.Conn) error {
	var wg sync.WaitGroup
	errors := make(chan error, len(clients))
	for i := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			conn, err := net.DialTimeout("unix", addr, 5*time.Second)
			if err != nil {
				errors <- err
				return
			}
			clients[i] = conn
			if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
				errors <- err
				return
			}
			decoder := json.NewDecoder(conn)
			for {
				var msg protocol.Message
				if err := decoder.Decode(&msg); err != nil {
					errors <- err
					return
				}
				if msg.Type == "state" {
					if msg.Board == nil || (msg.Symbol != "X" && msg.Symbol != "O") {
						errors <- fmt.Errorf("incomplete match state: %+v", msg)
					}
					return
				}
				if msg.Type != "waiting" {
					errors <- fmt.Errorf("unexpected message: %+v", msg)
					return
				}
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

func waitForNoPlayers(s *Server) error {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		count := len(s.players)
		s.mu.Unlock()
		if count == 0 {
			return nil
		}
		time.Sleep(time.Millisecond)
	}
	return fmt.Errorf("server still has clients after cleanup")
}
