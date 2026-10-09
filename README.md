# Network Tic-Tac-Toe

A TCP tic-tac-toe game written in Go. One server pairs clients in the order they connect. The first player is X, the second is O. The server keeps the board and checks every move.

A completed game ends both connections. After a win or draw, the server sends the final state and players can reconnect for another game. If someone leaves during a game, the remaining player goes back to matchmaking on the same connection.

## Build

```sh
mkdir -p bin
go build -o bin/server ./cmd/server
go build -o bin/client ./cmd/client
```

## Run

Go 1.27 is used in `go.mod`. Start these in three terminals:

```sh
go run ./cmd/server -addr :8080
go run ./cmd/client -addr 127.0.0.1:8080
go run ./cmd/client -addr 127.0.0.1:8080
```

Enter a square from 1 to 9. Type `quit` to disconnect. Empty squares show their number.

## Tests

```sh
go test ./...
go test -race ./...
go run ./scripts/test.go -servers 2 -clients 10 -later 2
go run ./scripts/load.go -clients 1000 -later 2 -parallel 100
```

- `go test` uses the standard Go testing package. The server tests use loopback TCP connections.
- `scripts/test.go` builds the server and client binaries, runs complete games, then adds more clients while the servers are still running. The flags are per server.
- `scripts/load.go` starts one server process and keeps 1,000 lightweight TCP clients connected together. They play 500 games; a later pair plays game 501. This checks connections and game results without starting 1,000 client processes.

To control the client binaries yourself:

```sh
go run ./scripts/test.go -interactive -servers 1 -clients 2
```

Use `add 1 2` to add two clients to server 1, `move 1 5` to send square 5 from client 1, `list` to see clients, and `quit` to stop. `-clients 0` is allowed in interactive mode.

## Project Layout

| Path | Responsibility |
| --- | --- |
| `game/` | Board, turns, move validation, wins, draws. |
| `protocol/` | JSON message used by server and client. |
| `server/` | TCP connections, matchmaking, game ownership, disconnect handling. |
| `cmd/server/` | Server executable and shutdown signals. |
| `cmd/client/` | Terminal client. |
| `scripts/` | Process test and 1,000-client load test. |

Messages are JSON lines over TCP. Clients send `move` with a position from 0 to 8; the terminal shows 1 to 9. The server sends `waiting`, `state`, `error`, or `opponent_left`.

## Docs

- [Architecture](docs/architecture.md): flow, ownership, design choices, alternatives, and limits.
- [Performance](docs/performance.md): benchmark, pprof commands, and measured results.
