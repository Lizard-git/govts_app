package voice

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"example.com/go-voice-mvp/internal/domain"
)

var (
	ErrSessionNotFound      = errors.New("session not found")
	ErrSessionNotInChannel  = errors.New("session has not joined a channel")
	ErrChannelNotFound      = errors.New("channel not found")
	ErrChannelFull          = errors.New("channel is full")
	ErrChannelNameTaken     = errors.New("channel name is already in use")
	ErrChannelNameAmbiguous = errors.New("channel name is ambiguous")
	ErrInvalidChannel       = errors.New("invalid channel")
)

const (
	DefaultChannelID   domain.ChannelID = 1
	DefaultChannelName                  = "default"
)

type sessionIDGenerator func() (uint64, error)

type Hub struct {
	mu            sync.RWMutex
	sessions      map[uint64]*Session
	channels      map[domain.ChannelID]*domain.Channel
	newSessionID  sessionIDGenerator
	nextChannelID domain.ChannelID
	revision      domain.StateRevision
}

func NewHub() *Hub {
	return newHub(randomSessionID)
}

func newHub(newSessionID sessionIDGenerator) *Hub {
	if newSessionID == nil {
		panic("session ID generator is required")
	}

	defaultChannel := domain.Channel{
		ID:    DefaultChannelID,
		Name:  DefaultChannelName,
		Type:  domain.ChannelTypePermanent,
		Audio: domain.DefaultAudioProfile(),
	}

	return &Hub{
		sessions:      make(map[uint64]*Session),
		channels:      map[domain.ChannelID]*domain.Channel{DefaultChannelID: &defaultChannel},
		newSessionID:  newSessionID,
		nextChannelID: DefaultChannelID + 1,
		revision:      1,
	}
}

func (h *Hub) Add(s *Session) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.sessions[s.ID] = cloneSession(s)
	h.revision++
}

func (h *Hub) Remove(id uint64) (Session, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	session, ok := h.sessions[id]
	if !ok {
		return Session{}, false
	}
	delete(h.sessions, id)
	h.revision++
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

func (h *Hub) JoinChannel(id uint64, channelID domain.ChannelID) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	return h.joinChannelLocked(id, channelID)
}

func (h *Hub) JoinChannelByName(id uint64, name string) (domain.Channel, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	channel, err := h.findChannelByNameLocked(name)
	if err != nil {
		return domain.Channel{}, err
	}
	if err := h.joinChannelLocked(id, channel.ID); err != nil {
		return domain.Channel{}, err
	}
	return *channel, nil
}

func (h *Hub) joinChannelLocked(id uint64, channelID domain.ChannelID) error {
	session, ok := h.sessions[id]
	if !ok {
		return ErrSessionNotFound
	}
	channel, ok := h.channels[channelID]
	if !ok {
		return fmt.Errorf("%w: %d", ErrChannelNotFound, channelID)
	}
	if session.ChannelID == channelID {
		return nil
	}
	if channel.MaxUsers > 0 && h.channelMemberCountLocked(channelID) >= channel.MaxUsers {
		return fmt.Errorf("%w: %q", ErrChannelFull, channel.Name)
	}

	session.ChannelID = channelID
	h.revision++
	return nil
}

func (h *Hub) Rename(id uint64, name string) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	session, ok := h.sessions[id]
	if !ok {
		return ErrSessionNotFound
	}
	if session.Name == name {
		return nil
	}
	session.Name = name
	h.revision++
	return nil
}

func (h *Hub) Count() int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return len(h.sessions)
}

func (h *Hub) Revision() domain.StateRevision {
	h.mu.RLock()
	defer h.mu.RUnlock()

	return h.revision
}

func (h *Hub) CreateChannel(channel domain.Channel) (domain.Channel, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if channel.ID != 0 {
		return domain.Channel{}, fmt.Errorf(
			"%w: channel ID is assigned by the hub",
			ErrInvalidChannel,
		)
	}
	if channel.Type == 0 {
		channel.Type = domain.ChannelTypePermanent
	}
	if channel.Audio == (domain.AudioProfile{}) {
		channel.Audio = domain.DefaultAudioProfile()
	}
	if err := validateChannel(channel); err != nil {
		return domain.Channel{}, err
	}
	if err := h.validateChannelParentLocked(channel.ParentID); err != nil {
		return domain.Channel{}, err
	}
	if h.channelNameExistsLocked(channel.ParentID, channel.Name) {
		return domain.Channel{}, fmt.Errorf(
			"%w: %q",
			ErrChannelNameTaken,
			channel.Name,
		)
	}

	id, err := h.nextChannelIDLocked()
	if err != nil {
		return domain.Channel{}, err
	}
	channel.ID = id
	h.channels[id] = cloneChannel(channel)
	h.revision++
	return channel, nil
}

func (h *Hub) GetChannel(id domain.ChannelID) (domain.Channel, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	channel, ok := h.channels[id]
	if !ok {
		return domain.Channel{}, false
	}
	return *cloneChannel(*channel), true
}

func (h *Hub) ListChannels() []domain.Channel {
	h.mu.RLock()
	defer h.mu.RUnlock()

	channels := make([]domain.Channel, 0, len(h.channels))
	for _, channel := range h.channels {
		channels = append(channels, *cloneChannel(*channel))
	}
	sort.Slice(channels, func(i int, j int) bool {
		if channels[i].ParentID != channels[j].ParentID {
			return channels[i].ParentID < channels[j].ParentID
		}
		if channels[i].Position != channels[j].Position {
			return channels[i].Position < channels[j].Position
		}
		return channels[i].ID < channels[j].ID
	})
	return channels
}

func (h *Hub) FindChannelByName(name string) (domain.Channel, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	channel, err := h.findChannelByNameLocked(name)
	if err != nil {
		return domain.Channel{}, err
	}
	return *cloneChannel(*channel), nil
}

func (h *Hub) Participants() []domain.Participant {
	h.mu.RLock()
	defer h.mu.RUnlock()

	participants := make([]domain.Participant, 0, len(h.sessions))
	for _, session := range h.sessions {
		participants = append(participants, domain.Participant{
			SessionID:   session.ID,
			DisplayName: session.Name,
			ChannelID:   session.ChannelID,
		})
	}
	sort.Slice(participants, func(i int, j int) bool {
		return participants[i].SessionID < participants[j].SessionID
	})
	return participants
}

func (h *Hub) Members(channelID domain.ChannelID) []string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	members := make([]string, 0)
	for _, session := range h.sessions {
		if session.ChannelID == channelID {
			members = append(members, session.Name)
		}
	}
	sort.Strings(members)
	return members
}

func (h *Hub) SessionsInChannel(channelID domain.ChannelID) []Session {
	h.mu.RLock()
	defer h.mu.RUnlock()

	sessions := make([]Session, 0)
	for _, session := range h.sessions {
		if session.ChannelID == channelID {
			sessions = append(sessions, *cloneSession(session))
		}
	}
	sortSessionsByID(sessions)
	return sessions
}

func (h *Hub) Recipients(channelID domain.ChannelID, senderID uint64) []Session {
	h.mu.RLock()
	defer h.mu.RUnlock()

	sessions := make([]Session, 0)
	for _, session := range h.sessions {
		if session.ChannelID == channelID && session.ID != senderID {
			sessions = append(sessions, *cloneSession(session))
		}
	}
	sortSessionsByID(sessions)
	return sessions
}

func (h *Hub) RecipientsFor(senderID uint64) ([]Session, error) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	sender, ok := h.sessions[senderID]
	if !ok {
		return nil, fmt.Errorf("session %d not found", senderID)
	}
	if sender.ChannelID == 0 {
		return nil, fmt.Errorf("session %d: %w", senderID, ErrSessionNotInChannel)
	}

	recipients := make([]Session, 0)
	for _, session := range h.sessions {
		if session.ChannelID == sender.ChannelID && session.ID != senderID {
			recipients = append(recipients, *cloneSession(session))
		}
	}
	sortSessionsByID(recipients)
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
	h.revision++
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
		h.revision++
		removed = append(removed, *cloneSession(session))
	}
	sortSessionsByID(removed)
	return removed
}

func validateChannel(channel domain.Channel) error {
	if !validChannelName(channel.Name) {
		return fmt.Errorf(
			"%w: channel name must be valid UTF-8, trimmed, and 1..%d bytes",
			ErrInvalidChannel,
			domain.MaxChannelNameBytes,
		)
	}
	if !utf8.ValidString(channel.Topic) || len(channel.Topic) > domain.MaxChannelTopicBytes {
		return fmt.Errorf(
			"%w: channel topic exceeds %d bytes or is not valid UTF-8",
			ErrInvalidChannel,
			domain.MaxChannelTopicBytes,
		)
	}
	if !utf8.ValidString(channel.Description) ||
		len(channel.Description) > domain.MaxChannelDescriptionBytes {
		return fmt.Errorf(
			"%w: channel description exceeds %d bytes or is not valid UTF-8",
			ErrInvalidChannel,
			domain.MaxChannelDescriptionBytes,
		)
	}
	if channel.Type != domain.ChannelTypePermanent {
		return fmt.Errorf("%w: unsupported channel type %d", ErrInvalidChannel, channel.Type)
	}
	if channel.Audio != domain.DefaultAudioProfile() {
		return fmt.Errorf("%w: unsupported audio profile", ErrInvalidChannel)
	}
	return nil
}

func validChannelName(name string) bool {
	return utf8.ValidString(name) &&
		name != "" &&
		len(name) <= domain.MaxChannelNameBytes &&
		strings.TrimSpace(name) == name
}

func (h *Hub) validateChannelParentLocked(parentID domain.ChannelID) error {
	depth := 1
	seen := make(map[domain.ChannelID]struct{})
	for parentID != 0 {
		if _, duplicate := seen[parentID]; duplicate {
			return fmt.Errorf("%w: channel hierarchy contains a cycle", ErrInvalidChannel)
		}
		seen[parentID] = struct{}{}

		parent, ok := h.channels[parentID]
		if !ok {
			return fmt.Errorf("%w: parent %d", ErrChannelNotFound, parentID)
		}
		depth++
		if depth > domain.MaxChannelDepth {
			return fmt.Errorf(
				"%w: channel depth exceeds %d",
				ErrInvalidChannel,
				domain.MaxChannelDepth,
			)
		}
		parentID = parent.ParentID
	}
	return nil
}

func (h *Hub) channelNameExistsLocked(parentID domain.ChannelID, name string) bool {
	for _, channel := range h.channels {
		if channel.ParentID == parentID && strings.EqualFold(channel.Name, name) {
			return true
		}
	}
	return false
}

func (h *Hub) findChannelByNameLocked(name string) (*domain.Channel, error) {
	var found *domain.Channel
	for _, channel := range h.channels {
		if !strings.EqualFold(channel.Name, name) {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("%w: %q", ErrChannelNameAmbiguous, name)
		}
		found = channel
	}
	if found == nil {
		return nil, fmt.Errorf("%w: %q", ErrChannelNotFound, name)
	}
	return found, nil
}

func (h *Hub) channelMemberCountLocked(channelID domain.ChannelID) uint32 {
	var count uint32
	for _, session := range h.sessions {
		if session.ChannelID == channelID {
			count++
		}
	}
	return count
}

func (h *Hub) nextChannelIDLocked() (domain.ChannelID, error) {
	for h.nextChannelID != 0 {
		id := h.nextChannelID
		h.nextChannelID++
		if _, exists := h.channels[id]; !exists {
			return id, nil
		}
	}
	return 0, errors.New("channel ID space exhausted")
}

func cloneChannel(channel domain.Channel) *domain.Channel {
	clone := channel
	return &clone
}

func sortSessionsByID(sessions []Session) {
	sort.Slice(sessions, func(i int, j int) bool {
		return sessions[i].ID < sessions[j].ID
	})
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
