package media

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"example.com/go-voice-mvp/internal/domain"
	"example.com/go-voice-mvp/internal/voice"
	"github.com/pion/interceptor"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

var (
	ErrPublisherExists    = errors.New("publisher already exists")
	ErrStreamNotReady     = errors.New("screen stream is not ready")
	ErrSubscriptionDenied = errors.New("screen stream subscription denied")
)

const subscriberQueueSize = 128

type Manager struct {
	mu           sync.RWMutex
	hub          *voice.Hub
	api          *webrtc.API
	publishers   map[domain.StreamID]*publisher
	ownerStreams map[uint64]domain.StreamID
	done         chan struct{}
	closeOnce    sync.Once
}

type Config struct {
	MinUDPPort   uint16
	MaxUDPPort   uint16
	AdvertisedIP string
}

type publisher struct {
	id          domain.StreamID
	ownerID     uint64
	pc          *webrtc.PeerConnection
	mu          sync.RWMutex
	codec       webrtc.RTPCodecCapability
	ssrc        webrtc.SSRC
	ready       bool
	starting    bool
	subscribers map[string]*subscriber
	closed      chan struct{}
	closeOnce   sync.Once
	createdAt   time.Time
}

type subscriber struct {
	id        string
	sessionID uint64
	pc        *webrtc.PeerConnection
	track     *webrtc.TrackLocalStaticRTP
	packets   chan *rtp.Packet
	closed    chan struct{}
	closeOnce sync.Once
}

type SessionDescription struct {
	Type string `json:"type"`
	SDP  string `json:"sdp"`
}

type PublishResult struct {
	StreamID domain.StreamID
	Answer   SessionDescription
}

type SubscribeResult struct {
	Answer SessionDescription
}

func NewManager(hub *voice.Hub) (*Manager, error) {
	return NewManagerWithConfig(hub, Config{})
}

func NewManagerWithConfig(hub *voice.Hub, config Config) (*Manager, error) {
	if hub == nil {
		return nil, errors.New("hub is required")
	}
	mediaEngine := &webrtc.MediaEngine{}
	if err := mediaEngine.RegisterCodec(webrtc.RTPCodecParameters{RTPCodecCapability: webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeVP8, ClockRate: 90000, RTCPFeedback: []webrtc.RTCPFeedback{{Type: "nack"}, {Type: "nack", Parameter: "pli"}, {Type: "goog-remb"}}}, PayloadType: 96}, webrtc.RTPCodecTypeVideo); err != nil {
		return nil, err
	}
	registry := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(mediaEngine, registry); err != nil {
		return nil, err
	}
	settingEngine := webrtc.SettingEngine{}
	if config.MinUDPPort != 0 || config.MaxUDPPort != 0 {
		if config.MinUDPPort == 0 || config.MaxUDPPort < config.MinUDPPort {
			return nil, errors.New("invalid media UDP port range")
		}
		if err := settingEngine.SetEphemeralUDPPortRange(config.MinUDPPort, config.MaxUDPPort); err != nil {
			return nil, err
		}
	}
	if config.AdvertisedIP != "" {
		settingEngine.SetNAT1To1IPs([]string{config.AdvertisedIP}, webrtc.ICECandidateTypeHost)
	}
	m := &Manager{hub: hub, api: webrtc.NewAPI(webrtc.WithMediaEngine(mediaEngine), webrtc.WithInterceptorRegistry(registry), webrtc.WithSettingEngine(settingEngine)), publishers: make(map[domain.StreamID]*publisher), ownerStreams: make(map[uint64]domain.StreamID), done: make(chan struct{})}
	go m.reconcileLoop()
	return m, nil
}

func (m *Manager) Publish(ctx context.Context, ownerID uint64, offer SessionDescription) (PublishResult, error) {
	if !m.sessionInChannel(ownerID) {
		return PublishResult{}, voice.ErrSessionNotInChannel
	}
	m.mu.Lock()
	if _, exists := m.ownerStreams[ownerID]; exists {
		m.mu.Unlock()
		return PublishResult{}, ErrPublisherExists
	}
	streamID, err := newStreamID(m.publishers)
	if err != nil {
		m.mu.Unlock()
		return PublishResult{}, err
	}
	pc, err := m.api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		m.mu.Unlock()
		return PublishResult{}, err
	}
	p := &publisher{id: streamID, ownerID: ownerID, pc: pc, subscribers: make(map[string]*subscriber), closed: make(chan struct{}), createdAt: time.Now()}
	m.publishers[streamID] = p
	m.ownerStreams[ownerID] = streamID
	m.mu.Unlock()

	cleanup := func() { m.StopPublisher(ownerID, streamID) }
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("screen publisher state: stream=%d owner=%d state=%s", streamID, ownerID, state)
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
			cleanup()
		}
	})
	pc.OnTrack(func(remote *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		p.mu.Lock()
		if p.ready || p.starting {
			p.mu.Unlock()
			return
		}
		p.codec = remote.Codec().RTPCodecCapability
		p.ssrc = remote.SSRC()
		p.starting = true
		p.mu.Unlock()
		log.Printf("screen publisher track: stream=%d owner=%d codec=%s ssrc=%d", streamID, ownerID, remote.Codec().MimeType, remote.SSRC())
		if _, startErr := m.hub.StartScreenShare(ownerID, streamID); startErr != nil {
			cleanup()
			return
		}
		p.mu.Lock()
		p.ready = true
		p.starting = false
		p.mu.Unlock()
		go m.forward(p, remote)
	})
	if _, err = pc.AddTransceiverFromKind(webrtc.RTPCodecTypeVideo, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
		cleanup()
		return PublishResult{}, err
	}
	answer, err := acceptOffer(ctx, pc, offer)
	if err != nil {
		cleanup()
		return PublishResult{}, err
	}
	return PublishResult{StreamID: streamID, Answer: answer}, nil
}

func (m *Manager) Subscribe(ctx context.Context, sessionID uint64, streamID domain.StreamID, subscriberID string, offer SessionDescription) (SubscribeResult, error) {
	if subscriberID == "" {
		return SubscribeResult{}, errors.New("subscriber ID is required")
	}
	if !m.hub.CanSubscribeScreen(sessionID, streamID) {
		return SubscribeResult{}, ErrSubscriptionDenied
	}
	m.mu.RLock()
	p := m.publishers[streamID]
	m.mu.RUnlock()
	if p == nil {
		return SubscribeResult{}, ErrStreamNotReady
	}
	p.mu.Lock()
	if !p.ready {
		p.mu.Unlock()
		return SubscribeResult{}, ErrStreamNotReady
	}
	if _, exists := p.subscribers[subscriberID]; exists {
		p.mu.Unlock()
		return SubscribeResult{}, errors.New("subscriber already exists")
	}
	pc, err := m.api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		p.mu.Unlock()
		return SubscribeResult{}, err
	}
	track, err := webrtc.NewTrackLocalStaticRTP(p.codec, "screen", fmt.Sprintf("screen-%d", streamID))
	if err != nil {
		p.mu.Unlock()
		_ = pc.Close()
		return SubscribeResult{}, err
	}
	sender, err := pc.AddTrack(track)
	if err != nil {
		p.mu.Unlock()
		_ = pc.Close()
		return SubscribeResult{}, err
	}
	s := &subscriber{id: subscriberID, sessionID: sessionID, pc: pc, track: track, packets: make(chan *rtp.Packet, subscriberQueueSize), closed: make(chan struct{})}
	p.subscribers[subscriberID] = s
	p.mu.Unlock()
	go drainRTCP(sender)
	go runSubscriber(s)
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		log.Printf("screen subscriber state: stream=%d session=%d subscriber=%q state=%s", streamID, sessionID, subscriberID, state)
		if state == webrtc.PeerConnectionStateFailed || state == webrtc.PeerConnectionStateClosed {
			m.Unsubscribe(sessionID, streamID, subscriberID)
		}
	})
	answer, err := acceptOffer(ctx, pc, offer)
	if err != nil {
		m.Unsubscribe(sessionID, streamID, subscriberID)
		return SubscribeResult{}, err
	}
	requestKeyframe(p)
	go requestStartupKeyframes(p, s)
	return SubscribeResult{Answer: answer}, nil
}

func requestKeyframe(p *publisher) {
	p.mu.RLock()
	ssrc := p.ssrc
	p.mu.RUnlock()
	if ssrc != 0 {
		_ = p.pc.WriteRTCP([]rtcp.Packet{&rtcp.PictureLossIndication{MediaSSRC: uint32(ssrc)}})
	}
}

// A browser may not act on the first PLI while the subscriber ICE/DTLS path is
// still becoming writable. A short bounded retry window avoids a permanently
// black late-join view without creating an ongoing keyframe storm.
func requestStartupKeyframes(p *publisher, s *subscriber) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for attempts := 0; attempts < 3; attempts++ {
		select {
		case <-p.closed:
			return
		case <-s.closed:
			return
		case <-ticker.C:
			requestKeyframe(p)
		}
	}
}

func (m *Manager) Unsubscribe(sessionID uint64, streamID domain.StreamID, subscriberID string) {
	m.mu.RLock()
	p := m.publishers[streamID]
	m.mu.RUnlock()
	if p == nil {
		return
	}
	p.mu.Lock()
	s := p.subscribers[subscriberID]
	if s != nil && s.sessionID == sessionID {
		delete(p.subscribers, subscriberID)
	} else {
		s = nil
	}
	p.mu.Unlock()
	if s != nil {
		s.close()
	}
}

func (m *Manager) StopPublisher(ownerID uint64, streamID domain.StreamID) {
	m.mu.Lock()
	p := m.publishers[streamID]
	if p == nil || p.ownerID != ownerID {
		m.mu.Unlock()
		return
	}
	delete(m.publishers, streamID)
	delete(m.ownerStreams, ownerID)
	m.mu.Unlock()
	p.close()
	_ = m.hub.StopScreenShare(ownerID, streamID)
}

func (m *Manager) Close() {
	m.closeOnce.Do(func() { close(m.done) })
	m.mu.RLock()
	ids := make([]domain.StreamID, 0, len(m.publishers))
	owners := make([]uint64, 0, len(m.publishers))
	for id, p := range m.publishers {
		ids = append(ids, id)
		owners = append(owners, p.ownerID)
	}
	m.mu.RUnlock()
	for i := range ids {
		m.StopPublisher(owners[i], ids[i])
	}
}

func (m *Manager) reconcileLoop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.done:
			return
		case <-ticker.C:
			m.reconcile()
		}
	}
}

func (m *Manager) reconcile() {
	m.mu.RLock()
	publishers := make([]*publisher, 0, len(m.publishers))
	for _, p := range m.publishers {
		publishers = append(publishers, p)
	}
	m.mu.RUnlock()
	for _, p := range publishers {
		p.mu.RLock()
		ready := p.ready
		p.mu.RUnlock()
		if !ready {
			if time.Since(p.createdAt) > 30*time.Second {
				m.StopPublisher(p.ownerID, p.id)
			}
			continue
		}
		stream, ok := m.hub.ScreenStream(p.id)
		if !ok || stream.OwnerSessionID != p.ownerID {
			m.StopPublisher(p.ownerID, p.id)
			continue
		}
		p.mu.RLock()
		invalid := make([]*subscriber, 0)
		for _, s := range p.subscribers {
			if !m.hub.CanSubscribeScreen(s.sessionID, p.id) {
				invalid = append(invalid, s)
			}
		}
		p.mu.RUnlock()
		for _, s := range invalid {
			m.Unsubscribe(s.sessionID, p.id, s.id)
		}
	}
}

func (m *Manager) forward(p *publisher, remote *webrtc.TrackRemote) {
	defer m.StopPublisher(p.ownerID, p.id)
	for {
		packet, _, err := remote.ReadRTP()
		if err != nil {
			return
		}
		p.mu.RLock()
		for _, s := range p.subscribers {
			clone := packet.Clone()
			select {
			case s.packets <- clone:
			default:
			}
		}
		p.mu.RUnlock()
	}
}

func (m *Manager) sessionInChannel(id uint64) bool {
	session, ok := m.hub.Get(id)
	return ok && session.ChannelID != 0
}

func (p *publisher) close() {
	p.closeOnce.Do(func() {
		close(p.closed)
		p.mu.Lock()
		subscribers := p.subscribers
		p.subscribers = make(map[string]*subscriber)
		p.mu.Unlock()
		for _, s := range subscribers {
			s.close()
		}
		_ = p.pc.Close()
	})
}
func (s *subscriber) close() { s.closeOnce.Do(func() { close(s.closed); _ = s.pc.Close() }) }
func runSubscriber(s *subscriber) {
	for {
		select {
		case <-s.closed:
			return
		case packet := <-s.packets:
			if err := s.track.WriteRTP(packet); err != nil {
				s.close()
				return
			}
		}
	}
}
func drainRTCP(sender *webrtc.RTPSender) {
	buffer := make([]byte, 1500)
	for {
		if _, _, err := sender.Read(buffer); err != nil {
			return
		}
	}
}

func acceptOffer(ctx context.Context, pc *webrtc.PeerConnection, offer SessionDescription) (SessionDescription, error) {
	if offer.Type != "offer" || offer.SDP == "" {
		return SessionDescription{}, errors.New("invalid WebRTC offer")
	}
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offer.SDP}); err != nil {
		return SessionDescription{}, err
	}
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return SessionDescription{}, err
	}
	gathering := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		return SessionDescription{}, err
	}
	select {
	case <-ctx.Done():
		return SessionDescription{}, ctx.Err()
	case <-gathering:
	}
	local := pc.LocalDescription()
	if local == nil {
		return SessionDescription{}, errors.New("missing local description")
	}
	return SessionDescription{Type: "answer", SDP: local.SDP}, nil
}

func newStreamID(existing map[domain.StreamID]*publisher) (domain.StreamID, error) {
	for i := 0; i < 8; i++ {
		var b [8]byte
		if _, err := rand.Read(b[:]); err != nil {
			return 0, err
		}
		id := domain.StreamID(binary.BigEndian.Uint64(b[:]))
		if id != 0 {
			if _, ok := existing[id]; !ok {
				return id, nil
			}
		}
	}
	return 0, errors.New("cannot allocate screen stream ID")
}
