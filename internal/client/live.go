package client

import (
	"context"
	"fmt"
	"io"
	"sort"
	"time"

	"example.com/go-voice-mvp/internal/domain"
)

type ClientViewState struct {
	ConnectionStatus ConnectionStatus
	ServerInfo       domain.ServerInfo
	Revision         domain.StateRevision
	Channels         []domain.Channel
	Participants     []domain.Participant
	SessionID        uint64
	ChannelID        domain.ChannelID
	SnapshotFresh    bool
	Muted, Deafened  bool
	Speaking         map[uint64]bool
}

func (s *State) ConfirmChannel(generation uint64, channel domain.ChannelID, revision domain.StateRevision) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation != generation {
		return false
	}
	if s.syncedGeneration == generation && s.snapshot.Revision >= revision {
		return true
	}
	s.channelID = channel
	if revision > s.observedRevision {
		s.observedRevision = revision
	}
	clear(s.speaking)
	s.requestResyncLocked()
	return true
}

func (s *State) SnapshotView() ClientViewState {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snapshot := s.snapshot.Clone()
	muted, deafened, _ := s.Audio.Snapshot()
	v := ClientViewState{ConnectionStatus: s.status, ServerInfo: snapshot.Info, Revision: snapshot.Revision,
		Channels: snapshot.Channels, Participants: snapshot.Participants, SessionID: s.sessionID, ChannelID: s.channelID,
		SnapshotFresh: s.snapshotFresh, Muted: muted, Deafened: deafened, Speaking: make(map[uint64]bool)}
	// The view's local channel belongs to the same snapshot as its participants.
	// A newer Join ACK marks it stale until that revision is synchronized.
	v.ChannelID = 0
	if s.syncedGeneration == s.generation {
		for _, p := range snapshot.Participants {
			if p.SessionID == s.sessionID {
				v.ChannelID = p.ChannelID
				break
			}
		}
	}
	for id := range s.speaking {
		v.Speaking[id] = true
	}
	return v
}

func (s *State) notifyLocked() {
	for ch := range s.subscribers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Only State closes subscriptions; consumers unsubscribe instead of closing.
func (s *State) Subscribe(ctx context.Context) (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	s.mu.Lock()
	s.subscribers[ch] = struct{}{}
	ch <- struct{}{}
	s.mu.Unlock()
	unsubscribe := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if _, ok := s.subscribers[ch]; ok {
			delete(s.subscribers, ch)
			close(ch)
		}
	}
	stop := context.AfterFunc(ctx, unsubscribe)
	return ch, func() { stop(); unsubscribe() }
}

func (s *State) noticeLocked(text string) {
	select {
	case s.notices <- text:
	default:
	} // Console history is deliberately best effort.
}

func (s *State) requestResyncLocked() {
	s.snapshotFresh = false
	select {
	case s.resync <- struct{}{}:
	default:
	}
	s.notifyLocked()
}

func (s *State) RequestResync(generation uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.generation == generation {
		s.requestResyncLocked()
	}
}

func (s *State) ApplyEvent(generation uint64, e domain.StateEvent) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if generation != s.generation {
		return false
	}
	if err := e.Validate(); err != nil {
		s.requestResyncLocked()
		return false
	}
	if e.Revision > s.observedRevision {
		s.observedRevision = e.Revision
	}
	if s.syncedGeneration == generation && e.Revision <= s.snapshot.Revision {
		return false
	}
	if !s.snapshotFresh || s.syncedGeneration != generation || e.Revision != s.snapshot.Revision+1 {
		s.requestResyncLocked()
		return false
	}
	next := s.snapshot.Clone()
	id := e.SessionID
	if e.Kind == domain.ParticipantJoined {
		id = e.Participant.SessionID
	}
	index := -1
	for i, p := range next.Participants {
		if p.SessionID == id {
			index = i
			break
		}
	}
	channelName := func(id domain.ChannelID) string {
		for _, c := range next.Channels {
			if c.ID == id {
				return terminalText(c.Name)
			}
		}
		return "Unjoined"
	}
	var line string
	switch e.Kind {
	case domain.ParticipantJoined:
		if index >= 0 || len(next.Participants) >= MaxSnapshotParticipants {
			s.requestResyncLocked()
			return false
		}
		next.Participants = append(next.Participants, e.Participant)
		sort.Slice(next.Participants, func(i, j int) bool { return next.Participants[i].SessionID < next.Participants[j].SessionID })
		line = fmt.Sprintf("+ %s connected (%s)", terminalText(e.Participant.DisplayName), channelName(e.Participant.ChannelID))
	case domain.ParticipantLeft:
		if index < 0 {
			s.requestResyncLocked()
			return false
		}
		line = fmt.Sprintf("- %s left", terminalText(next.Participants[index].DisplayName))
		next.Participants = append(next.Participants[:index], next.Participants[index+1:]...)
	case domain.ParticipantMoved:
		if index < 0 {
			s.requestResyncLocked()
			return false
		}
		p := &next.Participants[index]
		line = fmt.Sprintf("→ %s moved %s → %s", terminalText(p.DisplayName), channelName(p.ChannelID), channelName(e.ChannelID))
		p.ChannelID = e.ChannelID
	}
	next.Revision = e.Revision
	if err := validateServerSnapshot(next); err != nil {
		s.requestResyncLocked()
		return false
	}
	s.snapshot = next
	if id == s.sessionID {
		if e.Kind == domain.ParticipantMoved {
			s.channelID = e.ChannelID
		}
		if e.Kind == domain.ParticipantLeft {
			s.channelID = 0
		}
		clear(s.speaking)
	} else {
		delete(s.speaking, id)
	}
	s.noticeLocked(line)
	s.notifyLocked()
	return true
}

func (s *State) audioChanged() {
	muted, _, _ := s.Audio.Snapshot()
	s.mu.Lock()
	defer s.mu.Unlock()
	if muted {
		delete(s.speaking, s.sessionID)
	}
	s.notifyLocked()
}

// Notices are separate from coalesced view notifications: intermediate console
// events can be displayed, but an overloaded console never blocks network work.
func ConsoleStateLoop(ctx context.Context, state *State, output io.Writer) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case line := <-state.notices:
			if _, err := fmt.Fprintln(output, line); err != nil {
				return err
			}
		}
	}
}

func (s *State) clearSpeakingAt(now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	changed := false
	for id, until := range s.speaking {
		if !now.Before(until) {
			delete(s.speaking, id)
			changed = true
		}
	}
	if changed {
		s.notifyLocked()
	}
}
