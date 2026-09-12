package domain

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

type StateEventKind uint8

const (
	ParticipantJoined StateEventKind = iota + 1
	ParticipantLeft
	ParticipantMoved
)

// Participant is populated only for Joined; SessionID/ChannelID only for Left/Moved.
type StateEvent struct {
	Kind        StateEventKind
	Revision    StateRevision
	Participant Participant
	SessionID   uint64
	ChannelID   ChannelID
}

func (e StateEvent) Validate() error {
	if e.Revision == 0 {
		return errors.New("zero event revision")
	}
	switch e.Kind {
	case ParticipantJoined:
		p := e.Participant
		if e.SessionID != 0 || e.ChannelID != 0 || p.SessionID == 0 || !utf8.ValidString(p.DisplayName) || p.DisplayName == "" || len(p.DisplayName) > MaxParticipantNameBytes || strings.TrimSpace(p.DisplayName) != p.DisplayName || strings.ContainsFunc(p.DisplayName, func(r rune) bool { return unicode.IsControl(r) || r == '\u2028' || r == '\u2029' }) {
			return errors.New("invalid joined event")
		}
	case ParticipantLeft, ParticipantMoved:
		if e.Participant != (Participant{}) || e.SessionID == 0 || (e.Kind == ParticipantLeft && e.ChannelID != 0) || (e.Kind == ParticipantMoved && e.ChannelID == 0) {
			return errors.New("invalid left/moved event")
		}
	default:
		return errors.New("unknown event kind")
	}
	return nil
}
