package protocol

import (
	"encoding/binary"
	"errors"
	"uniclog.io/sonoryx/internal/domain"
)

const (
	VoiceControlOK      byte = 0
	VoiceControlStale   byte = 1
	VoiceControlAckSize      = 43
)

var ErrInvalidVoiceControlAck = errors.New("invalid voice control acknowledgement")

// VoiceRevision may be zero before the initial VoiceStateRequest. The response
// describes authority, but does not replace profile installation or VoiceState.
type VoiceControlAck struct {
	Status             byte
	OwnControlRevision uint64
	VoiceRevision      uint64
	DomainRevision     domain.StateRevision
	ChannelID          domain.ChannelID
	SourceEpoch        uint32
	ReceiverEpoch      uint32
	Muted, Deafened    bool
}

func EncodeVoiceControlAck(a VoiceControlAck) ([]byte, error) {
	if a.Status > VoiceControlStale || a.OwnControlRevision == 0 || a.DomainRevision == 0 || a.SourceEpoch == 0 || a.ReceiverEpoch == 0 {
		return nil, ErrInvalidVoiceControlAck
	}
	p := []byte{VoiceControlVersion, a.Status}
	for _, n := range []uint64{a.OwnControlRevision, a.VoiceRevision, uint64(a.DomainRevision), uint64(a.ChannelID)} {
		p = binary.BigEndian.AppendUint64(p, n)
	}
	p = binary.BigEndian.AppendUint32(p, a.SourceEpoch)
	p = binary.BigEndian.AppendUint32(p, a.ReceiverEpoch)
	return append(p, audioStateFlags(a.Muted, a.Deafened)), nil
}

func DecodeVoiceControlAck(p []byte) (VoiceControlAck, error) {
	if len(p) != VoiceControlAckSize || p[0] != VoiceControlVersion {
		return VoiceControlAck{}, ErrInvalidVoiceControlAck
	}
	muted, deafened, err := audioStateFromFlags(p[42])
	if err != nil {
		return VoiceControlAck{}, ErrInvalidVoiceControlAck
	}
	a := VoiceControlAck{Status: p[1], OwnControlRevision: binary.BigEndian.Uint64(p[2:10]), VoiceRevision: binary.BigEndian.Uint64(p[10:18]), DomainRevision: domain.StateRevision(binary.BigEndian.Uint64(p[18:26])), ChannelID: domain.ChannelID(binary.BigEndian.Uint64(p[26:34])), SourceEpoch: binary.BigEndian.Uint32(p[34:38]), ReceiverEpoch: binary.BigEndian.Uint32(p[38:42]), Muted: muted, Deafened: deafened}
	if a.Status > VoiceControlStale || a.OwnControlRevision == 0 || a.DomainRevision == 0 || a.SourceEpoch == 0 || a.ReceiverEpoch == 0 {
		return VoiceControlAck{}, ErrInvalidVoiceControlAck
	}
	return a, nil
}
