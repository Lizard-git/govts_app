package protocol

import (
	"encoding/binary"
	"errors"

	"example.com/go-voice-mvp/internal/domain"
)

const StateEventVersion = 1

func EncodeStateEvent(e domain.StateEvent) ([]byte, error) {
	if err := e.Validate(); err != nil {
		return nil, err
	}
	b := []byte{StateEventVersion, byte(e.Kind)}
	b = binary.BigEndian.AppendUint64(b, uint64(e.Revision))
	switch e.Kind {
	case domain.ParticipantJoined:
		return appendParticipant(b, e.Participant)
	case domain.ParticipantLeft:
		b = binary.BigEndian.AppendUint64(b, e.SessionID)
	case domain.ParticipantMoved:
		b = binary.BigEndian.AppendUint64(b, e.SessionID)
		b = binary.BigEndian.AppendUint64(b, uint64(e.ChannelID))
	}
	return b, nil
}

func DecodeStateEvent(b []byte) (domain.StateEvent, error) {
	var e domain.StateEvent
	if len(b) < 10 || len(b) > MaxPayloadSize || b[0] != StateEventVersion {
		return e, errors.New("invalid event header")
	}
	e.Kind, e.Revision = domain.StateEventKind(b[1]), domain.StateRevision(binary.BigEndian.Uint64(b[2:10]))
	b = b[10:]
	switch e.Kind {
	case domain.ParticipantJoined:
		var err error
		e.Participant, b, err = takeParticipant(b)
		if err != nil {
			return e, err
		}
	case domain.ParticipantLeft:
		if len(b) != 8 {
			return e, errors.New("invalid left payload")
		}
		e.SessionID = binary.BigEndian.Uint64(b)
		b = nil
	case domain.ParticipantMoved:
		if len(b) != 16 {
			return e, errors.New("invalid moved payload")
		}
		e.SessionID = binary.BigEndian.Uint64(b[:8])
		e.ChannelID = domain.ChannelID(binary.BigEndian.Uint64(b[8:]))
		b = nil
	default:
		return e, errors.New("unknown event kind")
	}
	if len(b) != 0 {
		return e, errors.New("trailing event data")
	}
	return e, e.Validate()
}
