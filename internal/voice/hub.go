package voice

import (
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

type Hub struct {
	mu       sync.RWMutex
	sessions map[uint64]*Session
	nextID   uint64
}

func NewHub() *Hub {
	return &Hub{
		sessions: make(map[uint64]*Session),
		nextID:   1,
	}
}

func (h *Hub) Add(s *Session) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.sessions[s.ID] = s
}

func (h *Hub) Remove(id uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	delete(h.sessions, id)
}

func (h *Hub) Get(id uint64) (*Session, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	s, ok := h.sessions[id]
	return s, ok
}

func (h *Hub) JoinChannel(id uint64, channel string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	session, ok := h.sessions[id]
	if !ok {
		return ErrSessionNotFound
	}
	session.JoinChannel(channel)
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

func (h *Hub) SessionsInChannel(channel string) []*Session {
	h.mu.RLock()
	defer h.mu.RUnlock()

	sessions := make([]*Session, 0)
	for _, session := range h.sessions {
		if session.Channel == channel {
			sessions = append(sessions, session)
		}
	}
	return sessions
}

func (h *Hub) Recipients(channel string, senderID uint64) []*Session {
	h.mu.RLock()
	defer h.mu.RUnlock()

	sessions := make([]*Session, 0)
	for _, session := range h.sessions {
		if session.Channel == channel && session.ID != senderID {
			sessions = append(sessions, session)
		}
	}
	return sessions
}

func (h *Hub) CreateSession(name string, addr *net.UDPAddr) *Session {
	h.mu.Lock()
	defer h.mu.Unlock()

	id := h.nextID
	h.nextID++
	session := &Session{
		ID:       id,
		Name:     name,
		Addr:     addr,
		LastSeen: time.Now(),
	}
	h.sessions[id] = session
	return session
}

func (h *Hub) Touch(sessionID uint64) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	session, ok := h.sessions[sessionID]
	if !ok {
		return fmt.Errorf("session %d not found", sessionID)
	}

	session.LastSeen = time.Now()
	return nil
}

func (h *Hub) RemoveInactive(now time.Time, timeout time.Duration) []*Session {
	h.mu.Lock()
	defer h.mu.Unlock()

	var removed []*Session
	for id, session := range h.sessions {
		if now.Sub(session.LastSeen) < timeout {
			continue
		}
		delete(h.sessions, id)
		removed = append(removed, session)
	}
	return removed
}
