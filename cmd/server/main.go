package main

import (
	"log"

	"example.com/go-voice-mvp/internal/transport/udp"
	"example.com/go-voice-mvp/internal/voice"
)

func main() {
	conn, err := udp.ListenUDP(9000)
	if err != nil {
		log.Fatalf("listen UDP: %v", err)
	}
	defer conn.Close()
	log.Println("voice server listening on :9000")

	hub := voice.NewHub()
	hub.Add(&voice.Session{
		ID:      42,
		Name:    "Alice",
		Channel: "Lobby",
	})
	//	hub.Add(&voice.Session{
	//		ID:      43,
	//		Name:    "Bob",
	//		Channel: "Lobby",
	//	})

	if err := voice.ServeUDP(conn, hub); err != nil {
		log.Fatalf("serve UDP: %v", err)
	}
}
