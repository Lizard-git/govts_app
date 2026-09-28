package media

import (
	"crypto/rand"
	"encoding/binary"
	"errors"

	"github.com/pion/webrtc/v4"
	"uniclog.io/govts/internal/domain"
)

func (m *Manager) forward(p *publisher, remote *webrtc.TrackRemote) {
	defer m.StopPublisher(p.ownerID, p.id)
	for {
		packet, _, err := remote.ReadRTP()
		if err != nil {
			return
		}
		p.inBytes.Add(uint64(packet.MarshalSize()))
		p.inPackets.Add(1)
		var needsRecovery []*subscriber
		p.mu.RLock()
		for _, s := range p.subscribers {
			if !m.hub.CanSubscribeScreen(s.sessionID, p.id) {
				continue
			}
			clone := packet.Clone()
			select {
			case s.packets <- clone:
			default:
				s.drops.Add(1)
				needsRecovery = append(needsRecovery, s)
			}
		}
		p.mu.RUnlock()
		for _, s := range needsRecovery {
			requestRecoveryKeyframe(p, s)
		}
	}
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
