package main

import (
	"log"

	"example.com/go-voice-mvp/internal/protocol"
)

func main() {
	conn, err := protocol.ListenUDP(9000)
	if err != nil {
		log.Fatalf("listen UDP: %v", err)
	}
	defer conn.Close()
	log.Println("voice server listening on :9000")

	hub := protocol.NewHub()
	hub.Add(&protocol.Session{
		ID:      42,
		Name:    "Alice",
		Channel: "Lobby",
	})
	//	hub.Add(&protocol.Session{
	//		ID:      43,
	//		Name:    "Bob",
	//		Channel: "Lobby",
	//	})

	if err := protocol.ServeUDP(conn, hub); err != nil {
		log.Fatalf("serve UDP: %v", err)
	}
}
