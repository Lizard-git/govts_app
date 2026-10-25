package protocol

import (
	"encoding/binary"
	"errors"
)

const (
	VoiceControlVersion        = 1
	VoiceStateFull        byte = 1 << 0
	VoiceStateOwnMuted    byte = 1 << 1
	VoiceStateOwnDeafened byte = 1 << 2
	VoiceDirectionUp      byte = 1 << 0
	VoiceDirectionDown    byte = 1 << 1
	VoiceSourceMuted      byte = 1 << 0
	VoiceSourceRemoved    byte = 1 << 1
	MaxVoiceStateEntries       = 16
	MaxVoiceStatePages         = 256
	MaxVoiceSources            = MaxVoiceStateEntries * MaxVoiceStatePages
	VoiceStateHeaderSize       = 60
	VoiceStateSourceSize       = 21
)

var ErrInvalidVoiceState = errors.New("invalid voice state")

type VoiceSourceState struct {
	SenderID    uint64
	SourceEpoch uint32
	ChannelID   uint64
	Flags       byte
}

// VoiceStatePage is installed only after all pages of one immutable revision
// have arrived. Revision and OwnControlRevision have different owners: changes
// to another speaker must not invalidate the recipient's own control command.
type VoiceStatePage struct {
	Flags                byte
	Revision             uint64
	BaseRevision         uint64
	OwnControlRevision   uint64
	OwnSourceEpoch       uint32
	OwnReceiverEpoch     uint32
	OwnChannelID         uint64
	AudioProfileRevision uint64
	DownstreamBarrier    uint32
	EnabledDirections    byte
	PageIndex            uint16
	PageCount            uint16
	Sources              []VoiceSourceState
}

func validateVoiceStatePage(page VoiceStatePage) error {
	if page.Flags & ^byte(VoiceStateFull|VoiceStateOwnMuted|VoiceStateOwnDeafened) != 0 || page.EnabledDirections & ^byte(VoiceDirectionUp|VoiceDirectionDown) != 0 ||
		page.Revision == 0 || page.OwnControlRevision == 0 || page.OwnSourceEpoch == 0 || page.OwnReceiverEpoch == 0 ||
		page.OwnChannelID == 0 || page.AudioProfileRevision == 0 ||
		page.PageCount == 0 || page.PageCount > MaxVoiceStatePages || page.PageIndex >= page.PageCount || len(page.Sources) > MaxVoiceStateEntries {
		return ErrInvalidVoiceState
	}
	if page.Flags&VoiceStateFull != 0 {
		if page.BaseRevision != 0 {
			return ErrInvalidVoiceState
		}
	} else if page.BaseRevision == 0 || page.BaseRevision >= page.Revision {
		return ErrInvalidVoiceState
	}
	if len(page.Sources) == 0 && page.PageCount != 1 {
		return ErrInvalidVoiceState
	}
	for i, source := range page.Sources {
		if source.SenderID == 0 || source.SourceEpoch == 0 || source.ChannelID == 0 ||
			source.Flags & ^byte(VoiceSourceMuted|VoiceSourceRemoved) != 0 ||
			(page.Flags&VoiceStateFull != 0 && source.Flags&VoiceSourceRemoved != 0) {
			return ErrInvalidVoiceState
		}
		for _, previous := range page.Sources[:i] {
			if previous.SenderID == source.SenderID {
				return ErrInvalidVoiceState
			}
		}
	}
	return nil
}

func EncodeVoiceState(page VoiceStatePage) ([]byte, error) {
	if err := validateVoiceStatePage(page); err != nil {
		return nil, err
	}
	payload := make([]byte, 0, VoiceStateHeaderSize+VoiceStateSourceSize*len(page.Sources))
	payload = append(payload, VoiceControlVersion, page.Flags)
	payload = binary.BigEndian.AppendUint64(payload, page.Revision)
	payload = binary.BigEndian.AppendUint64(payload, page.BaseRevision)
	payload = binary.BigEndian.AppendUint64(payload, page.OwnControlRevision)
	payload = binary.BigEndian.AppendUint32(payload, page.OwnSourceEpoch)
	payload = binary.BigEndian.AppendUint32(payload, page.OwnReceiverEpoch)
	payload = binary.BigEndian.AppendUint64(payload, page.OwnChannelID)
	payload = binary.BigEndian.AppendUint64(payload, page.AudioProfileRevision)
	payload = binary.BigEndian.AppendUint32(payload, page.DownstreamBarrier)
	payload = append(payload, page.EnabledDirections)
	payload = binary.BigEndian.AppendUint16(payload, page.PageIndex)
	payload = binary.BigEndian.AppendUint16(payload, page.PageCount)
	payload = append(payload, byte(len(page.Sources)))
	for _, source := range page.Sources {
		payload = binary.BigEndian.AppendUint64(payload, source.SenderID)
		payload = binary.BigEndian.AppendUint32(payload, source.SourceEpoch)
		payload = binary.BigEndian.AppendUint64(payload, source.ChannelID)
		payload = append(payload, source.Flags)
	}
	return payload, nil
}

func DecodeVoiceState(payload []byte) (VoiceStatePage, error) {
	if len(payload) < VoiceStateHeaderSize || payload[0] != VoiceControlVersion {
		return VoiceStatePage{}, ErrInvalidVoiceState
	}
	count := int(payload[59])
	if count > MaxVoiceStateEntries || len(payload) != VoiceStateHeaderSize+VoiceStateSourceSize*count {
		return VoiceStatePage{}, ErrInvalidVoiceState
	}
	page := VoiceStatePage{
		Flags:                payload[1],
		Revision:             binary.BigEndian.Uint64(payload[2:10]),
		BaseRevision:         binary.BigEndian.Uint64(payload[10:18]),
		OwnControlRevision:   binary.BigEndian.Uint64(payload[18:26]),
		OwnSourceEpoch:       binary.BigEndian.Uint32(payload[26:30]),
		OwnReceiverEpoch:     binary.BigEndian.Uint32(payload[30:34]),
		OwnChannelID:         binary.BigEndian.Uint64(payload[34:42]),
		AudioProfileRevision: binary.BigEndian.Uint64(payload[42:50]),
		DownstreamBarrier:    binary.BigEndian.Uint32(payload[50:54]),
		EnabledDirections:    payload[54],
		PageIndex:            binary.BigEndian.Uint16(payload[55:57]),
		PageCount:            binary.BigEndian.Uint16(payload[57:59]),
		Sources:              make([]VoiceSourceState, 0, count),
	}
	for rest := payload[VoiceStateHeaderSize:]; len(rest) > 0; rest = rest[VoiceStateSourceSize:] {
		page.Sources = append(page.Sources, VoiceSourceState{
			SenderID:    binary.BigEndian.Uint64(rest[:8]),
			SourceEpoch: binary.BigEndian.Uint32(rest[8:12]),
			ChannelID:   binary.BigEndian.Uint64(rest[12:20]),
			Flags:       rest[20],
		})
	}
	if err := validateVoiceStatePage(page); err != nil {
		return VoiceStatePage{}, err
	}
	return page, nil
}

func encodeVoiceRevision(revision uint64) []byte {
	return binary.BigEndian.AppendUint64([]byte{VoiceControlVersion}, revision)
}

func decodeVoiceRevision(payload []byte) (uint64, error) {
	if len(payload) != 9 || payload[0] != VoiceControlVersion {
		return 0, ErrInvalidVoiceState
	}
	return binary.BigEndian.Uint64(payload[1:]), nil
}

func EncodeVoiceStateAck(revision uint64) ([]byte, error) {
	if revision == 0 {
		return nil, ErrInvalidVoiceState
	}
	return encodeVoiceRevision(revision), nil
}

func DecodeVoiceStateAck(payload []byte) (uint64, error) {
	revision, err := decodeVoiceRevision(payload)
	if err != nil || revision == 0 {
		return 0, ErrInvalidVoiceState
	}
	return revision, nil
}

// A zero revision requests the initial full snapshot.
func EncodeVoiceStateRequest(revision uint64) []byte { return encodeVoiceRevision(revision) }

func DecodeVoiceStateRequest(payload []byte) (uint64, error) { return decodeVoiceRevision(payload) }
