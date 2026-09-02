package voice

import "errors"

var ErrSessionNotFound = errors.New("session not found")

type Hub struct {
	sessions map[uint64]*Session
}

func NewHub() *Hub {
	return &Hub{
		sessions: make(map[uint64]*Session),
	}
}

func (h *Hub) Add(s *Session) {
	h.sessions[s.ID] = s
}

func (h *Hub) Remove(id uint64) {
	delete(h.sessions, id)
}

func (h *Hub) Get(id uint64) (*Session, bool) {
	s, ok := h.sessions[id]
	return s, ok
}

func (h *Hub) JoinChannel(id uint64, channel string) error {
	session, ok := h.Get(id)
	if !ok {
		return ErrSessionNotFound
	}
	session.JoinChannel(channel)
	return nil
}

func (h *Hub) Rename(id uint64, name string) error {
	session, ok := h.Get(id)
	if !ok {
		return ErrSessionNotFound
	}
	session.Name = name
	return nil
}

func (h *Hub) Count() int {
	return len(h.sessions)
}

func (h *Hub) Members(channel string) []string {
	members := make([]string, 0)
	for _, session := range h.sessions {
		if session.Channel == channel {
			members = append(members, session.Name)
		}
	}
	return members
}

func (h *Hub) SessionsInChannel(channel string) []*Session {
	sessions := make([]*Session, 0)
	for _, session := range h.sessions {
		if session.Channel == channel {
			sessions = append(sessions, session)
		}
	}
	return sessions
}

/*
	func (h *Hub) Recipients(channel string, senderID uint64) []*Session {
		sender, ok := h.Get(senderID)
		if !ok || sender.Channel != channel || sender.Addr == nil {
			return nil
		}

		return []*Session{sender}
	}
*/
func (h *Hub) Recipients(channel string, senderID uint64) []*Session {
	sessions := make([]*Session, 0)
	sender, ok := h.Get(senderID)

	if !ok {
		return nil
	}

	for _, session := range h.SessionsInChannel(channel) {
		if session.ID == sender.ID {
			// continue
		}
		sessions = append(sessions, session)
	}
	return sessions
}
