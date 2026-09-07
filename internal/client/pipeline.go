package client

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"example.com/go-voice-mvp/internal/audio"
	"example.com/go-voice-mvp/internal/protocol"
	"example.com/go-voice-mvp/internal/transport/udp"
)

const frameDuration = 20 * time.Millisecond

type State struct {
	mu sync.RWMutex

	sessionID uint64
	name      string
	channel   string

	nextRequestID atomic.Uint32

	pending map[uint32]chan ControlResponse
}

type ControlResponse struct {
	Type      uint8
	RequestID uint32
	Payload   []byte
}

func NewState(
	sessionID uint64,
	name string,
	channel string,
) *State {
	return &State{
		sessionID: sessionID,
		name:      name,
		channel:   channel,
		pending:   make(map[uint32]chan ControlResponse),
	}
}

func (s *State) Channel() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.channel
}

func (s *State) SetChannel(channel string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.channel = channel
}

func (s *State) SessionID() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.sessionID
}

func (s *State) RegisterRequest(
	requestID uint32,
) <-chan ControlResponse {
	s.mu.Lock()
	defer s.mu.Unlock()

	ch := make(chan ControlResponse, 1)
	s.pending[requestID] = ch

	return ch
}

func (s *State) CompleteRequest(
	response ControlResponse,
) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	ch, ok := s.pending[response.RequestID]
	if !ok {
		return false
	}

	delete(s.pending, response.RequestID)

	ch <- response
	close(ch)

	return true
}

func (s *State) CancelRequest(requestID uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.pending, requestID)
}

func (s *State) NextRequestID() uint32 {
	return s.nextRequestID.Add(1)
}

func EncodeLoop(
	ctx context.Context,
	encoder audio.Encoder,
	pcmCh <-chan audio.PCMFrame,
	audioCh chan<- audio.Frame,
) error {
	defer close(audioCh)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case pcmFrame, ok := <-pcmCh:
			if !ok {
				return nil
			}
			buffer, err := encoder.Encode(pcmFrame.Samples)
			if err != nil {
				return err
			}
			frame := audio.Frame{
				Data:     buffer,
				Duration: pcmFrame.Duration,
			}
			select {
			case audioCh <- frame:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func SendLoop(ctx context.Context, conn *net.UDPConn, sessionID uint64, audioCh <-chan audio.Frame) error {
	sequence := uint32(1)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case frame, ok := <-audioCh:
			if !ok {
				return nil
			}
			sent := protocol.NewVoicePacket(sessionID, sequence, frame.Data)
			if err := udp.SendPacket(conn, sent); err != nil {
				return err
			}
			sequence++
		}
	}
}

const receivePollInterval = 500 * time.Millisecond

func ReceiveLoop(
	ctx context.Context,
	conn *net.UDPConn,
	encodedCh chan<- audio.Frame,
	controlCh chan<- protocol.VoicePacket,
) error {
	defer close(encodedCh)
	defer close(controlCh)

	for {
		if err := conn.SetReadDeadline(
			time.Now().Add(receivePollInterval),
		); err != nil {
			return err
		}

		packet, err := udp.ReceivePacket(conn)
		if err != nil {
			var netErr net.Error
			if errors.As(err, &netErr) && netErr.Timeout() {
				select {
				case <-ctx.Done():
					return ctx.Err()
				default:
					continue
				}
			}
			return err
		}

		switch packet.Type {
		case protocol.PacketVoice:
			frame := audio.Frame{
				Data:     packet.Payload,
				Duration: frameDuration,
			}

			select {
			case encodedCh <- frame:
			case <-ctx.Done():
				return ctx.Err()
			}

		default:
			select {
			case controlCh <- packet:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func ControlLoop(
	ctx context.Context,
	state *State,
	controlCh <-chan protocol.VoicePacket,
) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case packet, ok := <-controlCh:
			if !ok {
				return nil
			}
			if packet.SessionID != state.SessionID() {
				log.Printf("ignoring control packet for session %d", packet.SessionID)
				continue
			}
			switch packet.Type {
			case protocol.PacketJoinChannelAck:
				response := ControlResponse{
					Type:      packet.Type,
					RequestID: packet.RequestID,
					Payload:   packet.Payload,
				}
				if !state.CompleteRequest(response) {
					log.Printf("response for unknown request: request_id=%d", packet.RequestID)
					continue
				}
				channel := string(packet.Payload)
				state.SetChannel(channel)
				log.Printf("join ack: channel=%s request_id=%d", channel, packet.RequestID)

			default:
				log.Printf(
					"unhandled control packet: type=%d",
					packet.Type,
				)
			}
		}
	}
}

func DecodeLoop(
	ctx context.Context,
	decoder audio.Decoder,
	encodedCh <-chan audio.Frame,
	pcmOutCh chan<- audio.PCMFrame,
) error {
	defer close(pcmOutCh)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case frame, ok := <-encodedCh:
			if !ok {
				return nil
			}
			samples, err := decoder.Decode(frame.Data)
			if err != nil {
				return err
			}
			pcmFrame := audio.PCMFrame{
				Samples:  samples,
				Duration: frame.Duration,
			}
			select {
			case pcmOutCh <- pcmFrame:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func PlaybackLoop(ctx context.Context, player audio.Player, pcmOutCh <-chan audio.PCMFrame) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case frame, ok := <-pcmOutCh:
			if !ok {
				return nil
			}

			if err := player.Write(frame.Samples); err != nil {
				return err
			}
			// fmt.Printf("decoded PCM: samples=%d duration=%s", len(frame.Samples), frame.Duration)
		}
	}
}

func RecordLoop(
	ctx context.Context,
	recorder audio.Recorder,
	pcmCh chan<- audio.PCMFrame,
	samplesPerFrame int,
) error {
	defer close(pcmCh)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		samples := make([]int16, samplesPerFrame)

		n, err := recorder.Read(samples)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}

		frame := audio.PCMFrame{
			Samples:  samples[:n],
			Duration: frameDuration,
		}

		select {
		case pcmCh <- frame:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func PerformHandshake(
	conn *net.UDPConn,
	name string,
) (uint64, error) {
	hello := protocol.VoicePacket{
		Type:      protocol.PacketHello,
		SessionID: 0,
		Payload:   []byte(name),
	}

	if err := conn.SetReadDeadline(
		time.Now().Add(3 * time.Second),
	); err != nil {
		return 0, err
	}

	defer conn.SetReadDeadline(time.Time{})

	if err := udp.SendPacket(conn, hello); err != nil {
		return 0, err
	}

	ack, err := udp.ReceivePacket(conn)
	if err != nil {
		return 0, err
	}

	if ack.Type != protocol.PacketHelloAck {
		return 0, fmt.Errorf(
			"expected hello ack, got packet type %d",
			ack.Type,
		)
	}

	if ack.SessionID == 0 {
		return 0, fmt.Errorf("server returned invalid session ID")
	}

	return ack.SessionID, nil
}

func HeartbeatLoop(
	ctx context.Context,
	conn *net.UDPConn,
	sessionID uint64,
) error {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case <-ticker.C:
			packet := protocol.VoicePacket{
				Type:      protocol.PacketHeartbeat,
				SessionID: sessionID,
			}

			if err := udp.SendPacket(conn, packet); err != nil {
				return err
			}
		}
	}
}

func Disconnect(conn *net.UDPConn, sessionID uint64) error {
	packet := protocol.VoicePacket{
		Type:      protocol.PacketDisconnect,
		SessionID: sessionID,
	}
	return udp.SendPacket(conn, packet)
}

func JoinChannel(
	conn *net.UDPConn,
	state *State,
	channel string,
) error {
	/*packet := protocol.VoicePacket{
		Type:      protocol.PacketJoinChannel,
		SessionID: sessionID,
		Payload:   []byte(channel),
	}*/
	ch := make([]string, 1)
	ch[0] = channel
	handleJoin(conn, state, ch)
	return nil
}

func CommandLoop(
	conn *net.UDPConn,
	state *State,
	cancel context.CancelFunc,
) {
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		parts := strings.Fields(scanner.Text())
		if len(parts) == 0 {
			continue
		}
		switch parts[0] {
		case "/join":
			handleJoin(conn, state, parts[1:])
		case "/quit":
			cancel()
			return

		default:
			log.Printf(
				"unknown command: %s",
				parts[0],
			)
		}
	}
}

func handleJoin(conn *net.UDPConn, state *State, parts []string) {
	if len(parts) != 1 {
		log.Printf("usage: /join <channel>")
		return
	}

	channel := parts[0]

	requestID := state.NextRequestID()
	resultCh := state.RegisterRequest(requestID)

	packet := protocol.VoicePacket{
		Type:      protocol.PacketJoinChannel,
		SessionID: state.SessionID(),
		RequestID: requestID,
		Payload:   []byte(channel),
	}

	if err := udp.SendPacket(conn, packet); err != nil {
		state.CancelRequest(requestID)
		log.Printf("join channel: %v", err)
		return
	}

	log.Printf("join requested: channel=%s request_id=%d", channel, requestID)

	select {
	case response := <-resultCh:
		if response.Type != protocol.PacketJoinChannelAck {
			log.Printf("unexpected response type: %d", response.Type)
			return
		}

		joinedChannel := string(response.Payload)

		log.Printf(
			"join confirmed: channel=%s request_id=%d",
			joinedChannel,
			response.RequestID,
		)

	case <-time.After(3 * time.Second):
		state.CancelRequest(requestID)

		log.Printf(
			"join timeout: channel=%s request_id=%d",
			channel,
			requestID,
		)
	}
}
