package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strconv"
	"strings"

	"tictactoe/protocol"
)

func main() {
	address := flag.String("addr", "127.0.0.1:8080", "server address")
	flag.Parse()

	conn, err := net.Dial("tcp", *address)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	go readInput(conn)
	decoder := json.NewDecoder(conn)
	for {
		var msg protocol.Message
		if err := decoder.Decode(&msg); err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
				log.Printf("connection closed: %v", err)
			}
			return
		}
		switch msg.Type {
		case "waiting", "opponent_left", "error":
			fmt.Println(msg.Detail)
		case "state":
			if msg.Board == nil {
				log.Print("invalid state: missing board")
				return
			}
			printState(msg)
			if msg.Finished {
				return
			}
		}
	}
}

func readInput(conn net.Conn) {
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(conn)
	for scanner.Scan() {
		input := strings.TrimSpace(scanner.Text())
		if input == "quit" || input == "exit" {
			conn.Close()
			return
		}
		position, err := strconv.Atoi(input)
		if err != nil || position < 1 || position > 9 {
			fmt.Println("enter a position from 1 to 9, or 'quit'")
			continue
		}
		position--
		if err := encoder.Encode(protocol.Message{Type: "move", Position: &position}); err != nil {
			conn.Close()
			return
		}
	}
	if err := scanner.Err(); err != nil {
		log.Printf("failed to read from stdin: %v", err)
	}
	conn.Close()
}

func printState(msg protocol.Message) {
	cells := *msg.Board
	for i := range cells {
		if cells[i] == "" {
			cells[i] = strconv.Itoa(i + 1)
		}
	}
	fmt.Printf("\n %s | %s | %s\n---+---+---\n %s | %s | %s\n---+---+---\n %s | %s | %s\n\n",
		cells[0], cells[1], cells[2], cells[3], cells[4], cells[5], cells[6], cells[7], cells[8])
	if msg.Finished {
		switch msg.Winner {
		case "":
			fmt.Println("draw")
		case msg.Symbol:
			fmt.Println("you won")
		default:
			fmt.Println("you lost")
		}
		return
	}
	if msg.Turn == msg.Symbol {
		fmt.Printf("you are %s; your turn (1-9):\n", msg.Symbol)
	} else {
		fmt.Printf("you are %s; waiting for %s\n", msg.Symbol, msg.Turn)
	}
}
