package client

import (
	"sync"
	"sync/atomic"

	"example.com/go-voice-mvp/internal/domain"
)

type State struct {
	mu sync.RWMutex

	sessionID uint64
	name      string
	channelID domain.ChannelID
	snapshot  domain.ServerSnapshot

	nextRequestID atomic.Uint32
	pending       map[uint32]chan ControlResponse
}

type ControlResponse struct {
	Type      uint8
	RequestID uint32
	Payload   []byte
}

func NewState(sessionID uint64, name string) *State {
	return &State{
		sessionID: sessionID,
		name:      name,
		pending:   make(map[uint32]chan ControlResponse),
	}
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
