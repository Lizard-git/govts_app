package client

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"example.com/go-voice-mvp/internal/domain"
)

type State struct {
	mu               sync.RWMutex
	syncMu           sync.Mutex
	syncSerial       uint64
	syncedGeneration uint64
	observedRevision domain.StateRevision
	resync           chan struct{}
	subscribers      map[chan struct{}]struct{}
	notices          chan string
	speaking         map[uint64]time.Time
	Audio            *AudioControlState

	sessionID        uint64
	name             string
	channelID        domain.ChannelID
	snapshot         domain.ServerSnapshot
	generation       uint64
	status           ConnectionStatus
	snapshotFresh    bool
	lastHeartbeatAck time.Time

	nextRequestID atomic.Uint32
	pending       map[uint32]chan ControlResponse
}

type ConnectionStatus uint8

const (
	ConnectionConnecting ConnectionStatus = iota + 1
	ConnectionConnected
	ConnectionReconnecting
	ConnectionDisconnected
)

type ControlResponse struct {
	Type      uint8
	RequestID uint32
	Payload   []byte
}

func NewState(sessionID uint64, name string) *State {
	s := &State{
		sessionID:   sessionID,
		name:        name,
		generation:  1,
		status:      ConnectionConnecting,
		pending:     make(map[uint32]chan ControlResponse),
		resync:      make(chan struct{}, 1),
		subscribers: make(map[chan struct{}]struct{}),
		notices:     make(chan string, 64),
		speaking:    make(map[uint64]time.Time),
	}
	s.Audio = NewAudioControlState(func() { s.audioChanged() })
	return s
}

func (s *State) ChannelID() domain.ChannelID {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.channelID
}

func (s *State) SetChannelID(channelID domain.ChannelID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.channelID = channelID
	s.notifyLocked()
}

func (s *State) Generation() uint64 { s.mu.RLock(); defer s.mu.RUnlock(); return s.generation }
func (s *State) ConnectionStatus() ConnectionStatus {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}
func (s *State) SnapshotFresh() bool { s.mu.RLock(); defer s.mu.RUnlock(); return s.snapshotFresh }

func (s *State) LastHeartbeatAck() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastHeartbeatAck
}

func (s *State) MarkHeartbeatAck(at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastHeartbeatAck = at
}

func (s *State) SetConnectionStatus(status ConnectionStatus) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status = status
	s.notifyLocked()
}
func (s *State) InvalidateSession(status ConnectionStatus) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.generation++
	s.status = status
	s.snapshotFresh = false
	s.lastHeartbeatAck = time.Time{}
	s.channelID = 0
	s.sessionID = 0
	s.observedRevision = 0
drainNotices:
	for {
		select {
		case <-s.notices:
		default:
			break drainNotices
		}
	}
	clear(s.speaking)
	s.notifyLocked()
	return s.generation
}

func (s *State) StartSession(sessionID uint64) (uint64, error) {
	if sessionID == 0 {
		return 0, errors.New("session ID must not be zero")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.pending) != 0 {
		return 0, errors.New("cannot start session with pending requests")
	}
	s.generation++
	s.sessionID = sessionID
	s.channelID = 0
	s.snapshotFresh = false
	s.lastHeartbeatAck = time.Time{}
	s.status = ConnectionConnecting
	s.observedRevision = 0
	clear(s.speaking)
	s.notifyLocked()
	return s.generation, nil
}

func (s *State) SetChannelIDForGeneration(generation uint64, channelID domain.ChannelID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation != generation {
		return false
	}
	s.channelID = channelID
	clear(s.speaking)
	s.notifyLocked()
	return true
}

func (s *State) Snapshot() domain.ServerSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snapshot.Clone()
}
func (s *State) ReplaceSnapshot(snapshot domain.ServerSnapshot) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snapshot = snapshot.Clone()
	s.snapshotFresh = true
	s.syncedGeneration = s.generation
	s.notifyLocked()
}

func (s *State) ReplaceSnapshotForGeneration(generation uint64, snapshot domain.ServerSnapshot) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation != generation {
		return false
	}
	if s.syncedGeneration == generation && snapshot.Revision < s.snapshot.Revision {
		return true
	}
	if snapshot.Revision < s.observedRevision {
		s.requestResyncLocked()
		return true
	}
	s.snapshot = snapshot.Clone()
	s.syncedGeneration = generation
	s.snapshotFresh = snapshot.Revision >= s.observedRevision
	clear(s.speaking)
	for _, p := range snapshot.Participants {
		if p.SessionID == s.sessionID {
			s.channelID = p.ChannelID
		}
	}
	if !s.snapshotFresh {
		s.requestResyncLocked()
	}
	s.notifyLocked()
	return true
}

func (s *State) SessionID() uint64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessionID
}

func (s *State) RegisterRequest(requestID uint32) <-chan ControlResponse {
	s.mu.Lock()
	defer s.mu.Unlock()

	ch := make(chan ControlResponse, 1)
	s.pending[requestID] = ch
	return ch
}

func (s *State) CompleteRequest(response ControlResponse) bool {
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
	for {
		requestID := s.nextRequestID.Add(1)
		if requestID != 0 {
			return requestID
		}
	}
}
