package main

import (
	"fmt"
	"log"

	"example.com/go-voice-mvp/internal/protocol"
	"example.com/go-voice-mvp/internal/transport/udp"
)

func main() {
	conn, err := udp.ConnectUDP("127.0.0.1", 9000)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()

	hello := protocol.VoicePacket{Type: protocol.PacketHello, SessionID: 43}
	if err := udp.SendPacket(conn, hello); err != nil {
		log.Fatal(err)
	}

	for {
		packet, err := udp.ReceivePacket(conn)
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
func produceTestAudio(
	ctx context.Context,
	pcmCh chan<- audio.PCMFrame,
) {
	defer close(pcmCh)

	ticker := time.NewTicker(frameDuration)
	defer ticker.Stop()

	const frequency = 440.0
	const amplitude = 10000.0
	phase := 0.0
	phaseStep := 2 * math.Pi * frequency / float64(sampleRate)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			samples := make([]int16, samplesPerFrame)

			for i := range samples {
				samples[i] = int16(amplitude * math.Sin(phase))

				phase += phaseStep

				if phase >= 2*math.Pi {
					phase -= 2 * math.Pi
				}
			}

			pcmFrame := audio.PCMFrame{
				Samples:  samples,
				Duration: frameDuration,
			}
			select {
			case pcmCh <- pcmFrame:
			case <-ctx.Done():
				return
			}
		}
	}
}
