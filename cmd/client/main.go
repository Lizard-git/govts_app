package main

import (
	"fmt"
	"log"

	"example.com/go-voice-mvp/internal/protocol"
)

func main() {
	conn, err := protocol.ConnectUDP("127.0.0.1", 9000)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	hello := protocol.VoicePacket{Type: protocol.PacketHello, SessionID: 43}
	if err := protocol.SendPacket(conn, hello); err != nil {
		log.Fatal(err)
	}

	for {
		packet, err := protocol.ReceivePacket(conn)
		if err != nil {
			log.Printf("receive error: %v", err)
			continue
		}

		fmt.Printf(
			"type=%d session=%d sequence=%d payload=%s\n",
			packet.Type,
			packet.SessionID,
			packet.Sequence,
			packet.Payload,
		)
	}
}
