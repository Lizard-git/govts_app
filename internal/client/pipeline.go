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

const joinRequestTimeout = 3 * time.Second

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

// CompleteRequest LOCK
// │
// ├─ найти pending
// ├─ удалить pending
// │
//
//	UNLOCK
//
// │
// ├─ отправить response
// └─ close
func (s *State) CompleteRequest(
	response ControlResponse,
) bool {
	s.mu.Lock()

	ch, ok := s.pending[response.RequestID]
	if ok {
		delete(s.pending, response.RequestID)
	}

	s.mu.Unlock()

	if !ok {
		return false
	}

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
				}

			case protocol.PacketError:
				response := ControlResponse{
					Type:      packet.Type,
					RequestID: packet.RequestID,
					Payload:   packet.Payload,
				}
				if !state.CompleteRequest(response) {
					log.Printf("error response for unknown request: request_id=%d", packet.RequestID)
				}

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
	ctx context.Context,
	conn *net.UDPConn,
	state *State,
	channel string,
) error {
	return joinChannelWithTimeout(
		ctx,
		conn,
		state,
		channel,
		joinRequestTimeout,
	)
}

func joinChannelWithTimeout(
	ctx context.Context,
	conn *net.UDPConn,
	state *State,
	channel string,
	timeout time.Duration,
) error {
	if channel == "" {
		return errors.New("channel name is required")
	}

	packet := protocol.VoicePacket{
		Type:    protocol.PacketJoinChannel,
		Payload: []byte(channel),
	}

	response, err := DoRequest(
		ctx,
		conn,
		state,
		packet,
		timeout,
	)
	if err != nil {
		return fmt.Errorf("join channel %q: %w", channel, err)
	}

	if err := validateJoinResponse(response, channel); err != nil {
		return err
	}

	state.SetChannel(channel)
	return nil
}

func validateJoinResponse(response ControlResponse, requestedChannel string) error {
	if response.Type != protocol.PacketJoinChannelAck {
		return fmt.Errorf("unexpected join response: type=%d", response.Type)
	}
	confirmedChannel := string(response.Payload)
	if confirmedChannel != requestedChannel {
		return fmt.Errorf("join response channel mismatch: got %q, want %q", confirmedChannel, requestedChannel)
	}
	return nil
}

func CommandLoop(
	ctx context.Context,
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
			handleJoin(ctx, conn, state, parts[1:])
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

func handleJoin(
	ctx context.Context,
	conn *net.UDPConn,
	state *State,
	parts []string,
) {
	if len(parts) != 1 {
		log.Printf("usage: /join <channel>")
		return
	}

	channel := parts[0]
	if err := JoinChannel(ctx, conn, state, channel); err != nil {
		log.Printf("join channel: %v", err)
		return
	}

	log.Printf("join confirmed: %s", channel)
}

func DoRequest(
	ctx context.Context,
	conn *net.UDPConn,
	state *State,
	packet protocol.VoicePacket,
	timeout time.Duration,
) (ControlResponse, error) {
	requestID := state.NextRequestID()

	packet.SessionID = state.SessionID()
	packet.RequestID = requestID

	responseCh := state.RegisterRequest(requestID)
	defer state.CancelRequest(requestID)

	const attempts = 3

	for attempt := 1; attempt <= attempts; attempt++ {
		if err := udp.SendPacket(conn, packet); err != nil {
			return ControlResponse{}, fmt.Errorf("send request %d: %w", requestID, err)
		}

		timer := time.NewTimer(timeout)

		select {
		case response := <-responseCh:
			timer.Stop()
			if response.Type == protocol.PacketError {
				return ControlResponse{}, fmt.Errorf("server error: %s", string(response.Payload))
			}
			return response, nil

		case <-timer.C:
			if attempt == attempts {
				return ControlResponse{}, fmt.Errorf("request %d timed out after %d attempts", requestID, attempts)
			}

		case <-ctx.Done():
			timer.Stop()
			return ControlResponse{}, ctx.Err()
		}
	}
	return ControlResponse{}, fmt.Errorf("request %d failed", requestID)
}
