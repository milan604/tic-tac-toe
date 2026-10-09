package main

import (
	"context"
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"tictactoe/server"
)

func main() {
	address := flag.String("addr", ":8080", "address to listen on")
	flag.Parse()

	listener, err := net.Listen("tcp", *address)
	if err != nil {
		log.Fatal(err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Printf("listening on %s", listener.Addr())
	if err := server.New().Serve(ctx, listener); err != nil {
		log.Fatal(err)
	}
}
