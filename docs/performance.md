# Performance and Load Tests

Results below were taken on 2026-10-09 with Go 1.27.0 on an Apple M4 Pro (Darwin arm64). These are local measurements.

## Join Benchmark

[`server/benchmark_test.go`](../server/benchmark_test.go) runs one server and connects 10 or 100 clients in two batches. Clients in each batch join concurrently. All of them must receive a `state` message with a board and X/O assignment.

The timed part includes connection setup, server accept, pairing, and state delivery. Closing the connections and waiting for server cleanup are outside the timer. One operation is one complete batch.

The benchmark uses Unix sockets. Repeating thousands of short-lived TCP connections exhausted local ephemeral ports during development. Unix sockets still run through `Accept`, the connection handler, matchmaking, and JSON encoding. This benchmark does not include TCP latency or the terminal client binary.

```sh
go test ./server -run '^$' -bench '^BenchmarkClientsJoin$' -benchtime=2s -count=3
```

| Clients | First / later | Time per batch (3 runs) | Median clients/s | Median B/op | Median allocs/op |
| ---: | ---: | ---: | ---: | ---: | ---: |
| 10 | 4 / 6 | 0.432, 0.421, 0.380 ms | 23,736 | 60,125 | 454 |
| 100 | 50 / 50 | 2.075, 2.072, 2.083 ms | 48,190 | 592,744 | 4,479 |

`B/op` and `allocs/op` include both the test clients and server during the timed join path. `clients/s` is calculated from the number of clients in each batch. Run `-benchtime=100x -count=3` for a quicker check.

## CPU and Allocation Profile

Profile the 100-client benchmark:

```sh
go test ./server -run '^$' -bench '^BenchmarkClientsJoin/clients_100$' \
  -benchtime=1000x -count=1 \
  -cpuprofile=/tmp/tictactoe-join.cpu.pprof \
  -memprofile=/tmp/tictactoe-join.mem.pprof \
  -o /tmp/tictactoe-server.test

go tool pprof -top /tmp/tictactoe-server.test /tmp/tictactoe-join.cpu.pprof
go tool pprof -top -sample_index=alloc_space \
  /tmp/tictactoe-server.test /tmp/tictactoe-join.mem.pprof
```

The profiled run was 2.170 ms per 100-client batch. The CPU profile showed `syscall.rawsyscalln` at 66.4% and `runtime.kevent` at 20.2% of samples. Socket and runtime work dominated this run; the game rules did not appear as a join-path hotspot.

The allocation profile sampled 632.7 MB over 1,000 batches. The largest application allocation site was the 16-message `outgoing` channel in `Server.handleConn`, about 177.8 MB across 100,000 joined clients. That queue gives each connection some room for pending updates. A smaller queue would use less memory per player but would disconnect slow readers sooner.

Pprof sees the full test process, including clients and untimed cleanup. The benchmark numbers time only the join path. These profiles do not isolate server CPU or represent production capacity.

## Real Client Process Test

`scripts/test.go` builds and runs the actual server and terminal client binaries:

```sh
go run ./scripts/test.go -servers 1 -clients 100 -later 2
```

Result: 50 initial games and one later game passed with 102 client processes. It checks the final board, both players' result, and client exit. Build and process startup are included, so this is a correctness test rather than a throughput benchmark.

## 1,000-Client TCP Load Test

[`scripts/load.go`](../scripts/load.go) starts the server binary as a separate process. It creates lightweight TCP protocol clients so 1,000 connections can stay open together. `-parallel` limits how many dial attempts start at once; it does not limit the number of connected clients.

```sh
go run ./scripts/load.go -clients 1000 -later 2 -parallel 100
```

Result: 1,000 clients formed 500 games and all completed. Two more clients joined the same running server and completed game 501. Every board update, winner, and final EOF was checked. The first batch took 44 ms to join and 76 ms to finish on this machine; the later pair took 2 ms.

These times come from a local load check with client and server processes on the same host. They exclude building the server binary and are separate from the benchmark above. An earlier version with load clients and server in one process reset some connections; the separate-process run passed. To test an already running server, pass `-addr host:port`.
