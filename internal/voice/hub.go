package voice

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

var (
	ErrSessionNotFound     = errors.New("session not found")
	ErrSessionNotInChannel = errors.New("session has not joined a channel")
)

type sessionIDGenerator func() (uint64, error)

type Hub struct {
	mu           sync.RWMutex
	sessions     map[uint64]*Session
	newSessionID sessionIDGenerator
}

func NewHub() *Hub {
	return newHub(randomSessionID)
}

func newHub(newSessionID sessionIDGenerator) *Hub {
	if newSessionID == nil {
		panic("session ID generator is required")
	}

	return &Hub{
		sessions:     make(map[uint64]*Session),
		newSessionID: newSessionID,
	}
}

func (h *Hub) Add(s *Session) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.sessions[s.ID] = cloneSession(s)
}

func (h *Hub) Remove(id uint64) (Session, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	session, ok := h.sessions[id]
	if !ok {
		return Session{}, false
	}
	delete(h.sessions, id)
	return *cloneSession(session), true
}

func (h *Hub) Get(id uint64) (Session, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	s, ok := h.sessions[id]
	if !ok {
		return Session{}, false
	}
	return *cloneSession(s), true
}

func (h *Hub) JoinChannel(id uint64, channel string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	session, ok := h.sessions[id]
	if !ok {
		return ErrSessionNotFound
	}
	session.Channel = channel
	fmt.Printf("session %d joined channel %s\n", id, channel)
	return nil
}

func (h *Hub) Rename(id uint64, name string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	session, ok := h.sessions[id]
	if !ok {
		return ErrSessionNotFound
	}
	session.Name = name
	return nil
}

func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return len(h.sessions)
}

func (h *Hub) Members(channel string) []string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	members := make([]string, 0)
	for _, session := range h.sessions {
		if session.Channel == channel {
			members = append(members, session.Name)
		}
	}
	return members
}

func (h *Hub) SessionsInChannel(channel string) []Session {
	h.mu.RLock()
	defer h.mu.RUnlock()

	sessions := make([]Session, 0)
	for _, session := range h.sessions {
		if session.Channel == channel {
			sessions = append(sessions, *cloneSession(session))
		}
	}
	return sessions
}

func (h *Hub) Recipients(channel string, senderID uint64) []Session {
	h.mu.RLock()
	defer h.mu.RUnlock()

	sessions := make([]Session, 0)
	for _, session := range h.sessions {
		if session.Channel == channel && session.ID != senderID {
			sessions = append(sessions, *cloneSession(session))
		}
	}
	return sessions
}

func (h *Hub) RecipientsFor(senderID uint64) ([]Session, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	sender, ok := h.sessions[senderID]
	if !ok {
		return nil, fmt.Errorf("session %d not found", senderID)
	}
	if sender.Channel == "" {
		return nil, fmt.Errorf("session %d: %w", senderID, ErrSessionNotInChannel)
	}

	recipients := make([]Session, 0)
	for _, session := range h.sessions {
		if session.Channel == sender.Channel && session.ID != senderID {
			recipients = append(recipients, *cloneSession(session))
		}
	}
	return recipients, nil
}

func (h *Hub) CreateSession(name string, addr *net.UDPAddr) (Session, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	id, err := h.availableSessionID()
	if err != nil {
		return Session{}, err
	}
	session := &Session{
		ID:       id,
		Name:     name,
		Addr:     cloneUDPAddr(addr),
		LastSeen: time.Now(),
	}
	h.sessions[id] = session
	return *cloneSession(session), nil
}

func (h *Hub) availableSessionID() (uint64, error) {
	for {
		id, err := h.newSessionID()
		if err != nil {
			return 0, fmt.Errorf("generate session ID: %w", err)
		}
		if id == 0 {
			continue
		}
		if _, exists := h.sessions[id]; !exists {
			return id, nil
		}
	}
}

func randomSessionID() (uint64, error) {
	var data [8]byte
	if _, err := rand.Read(data[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint64(data[:]), nil
}

func (h *Hub) Touch(sessionID uint64) error {
	return h.touchAt(sessionID, time.Now())
}

func (h *Hub) touchAt(sessionID uint64, now time.Time) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	session, ok := h.sessions[sessionID]
	if !ok {
		return fmt.Errorf("session %d not found", sessionID)
	}

	session.LastSeen = now
	return nil
}

func (h *Hub) UpdateAddr(sessionID uint64, addr *net.UDPAddr) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	session, ok := h.sessions[sessionID]
	if !ok {
		return fmt.Errorf("session %d not found", sessionID)
	}

	session.Addr = cloneUDPAddr(addr)
	return nil
}

func (h *Hub) RemoveInactive(now time.Time, timeout time.Duration) []Session {
	h.mu.Lock()
	defer h.mu.Unlock()

	var removed []Session
	for id, session := range h.sessions {
		if now.Sub(session.LastSeen) < timeout {
			continue
		}
		delete(h.sessions, id)
		removed = append(removed, *cloneSession(session))
	}
	return removed
}

func cloneSession(session *Session) *Session {
	if session == nil {
		return nil
	}

	clone := *session
	clone.Addr = cloneUDPAddr(session.Addr)
	return &clone
}

func cloneUDPAddr(addr *net.UDPAddr) *net.UDPAddr {
	if addr == nil {
		return nil
	}

	clone := *addr
	clone.IP = append(net.IP(nil), addr.IP...)
	return &clone
}
