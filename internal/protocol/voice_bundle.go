package protocol

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// VoiceBundleVersion is the payload schema of PacketVoiceBundle:
//
//	u8  version
//	u8  frame_count (1..MaxVoiceBundleFrames)
//	repeat frame_count times:
//	    u32 sequence
//	    u16 payload_length
//	    u8  opus_payload[payload_length]
//
// Frames are ordered by ascending sequence: the redundant previous frame first,
// then the current one.
const VoiceBundleVersion = 1

const (
	MaxVoiceBundleFrames  = 2
	voiceBundleHeaderSize = 2
	voiceBundleFrameSize  = 6
)

var ErrInvalidVoiceBundle = errors.New("invalid voice bundle")

type VoiceBundleFrame struct {
	Sequence uint32
	Payload  []byte
}

// VoiceBundleSize returns the encoded payload size for frames.
func VoiceBundleSize(frames []VoiceBundleFrame) int {
	size := voiceBundleHeaderSize
	for _, frame := range frames {
		size += voiceBundleFrameSize + len(frame.Payload)
	}
	return size
}

func EncodeVoiceBundle(frames []VoiceBundleFrame) ([]byte, error) {
	if len(frames) == 0 || len(frames) > MaxVoiceBundleFrames {
		return nil, fmt.Errorf("%w: %d frames", ErrInvalidVoiceBundle, len(frames))
	}
	size := VoiceBundleSize(frames)
	if size > MaxPayloadSize {
		return nil, fmt.Errorf("%w: got %d bytes, max %d", ErrPayloadTooLarge, size, MaxPayloadSize)
	}
	payload := make([]byte, 0, size)
	payload = append(payload, VoiceBundleVersion, uint8(len(frames)))
	for i, frame := range frames {
		if len(frame.Payload) == 0 {
			return nil, fmt.Errorf("%w: empty frame", ErrInvalidVoiceBundle)
		}
		if i > 0 && int32(frame.Sequence-frames[i-1].Sequence) <= 0 {
			return nil, fmt.Errorf("%w: frames out of order", ErrInvalidVoiceBundle)
		}
		payload = binary.BigEndian.AppendUint32(payload, frame.Sequence)
		payload = binary.BigEndian.AppendUint16(payload, uint16(len(frame.Payload)))
		payload = append(payload, frame.Payload...)
	}
	return payload, nil
}

func DecodeVoiceBundle(payload []byte) ([]VoiceBundleFrame, error) {
	if len(payload) < voiceBundleHeaderSize || len(payload) > MaxPayloadSize {
		return nil, fmt.Errorf("%w: size %d", ErrInvalidVoiceBundle, len(payload))
	}
	if payload[0] != VoiceBundleVersion {
		return nil, fmt.Errorf("%w: version %d", ErrInvalidVoiceBundle, payload[0])
	}
	count := int(payload[1])
	if count == 0 || count > MaxVoiceBundleFrames {
		return nil, fmt.Errorf("%w: %d frames", ErrInvalidVoiceBundle, count)
	}
	frames := make([]VoiceBundleFrame, 0, count)
	rest := payload[voiceBundleHeaderSize:]
	for i := 0; i < count; i++ {
		if len(rest) < voiceBundleFrameSize {
			return nil, fmt.Errorf("%w: truncated frame header", ErrInvalidVoiceBundle)
		}
		sequence := binary.BigEndian.Uint32(rest[0:4])
		length := int(binary.BigEndian.Uint16(rest[4:6]))
		rest = rest[voiceBundleFrameSize:]
		if length == 0 || len(rest) < length {
			return nil, fmt.Errorf("%w: invalid frame length %d", ErrInvalidVoiceBundle, length)
		}
		if i > 0 && int32(sequence-frames[i-1].Sequence) <= 0 {
			return nil, fmt.Errorf("%w: frames out of order", ErrInvalidVoiceBundle)
		}
		frames = append(frames, VoiceBundleFrame{Sequence: sequence, Payload: rest[:length]})
		rest = rest[length:]
	}
	if len(rest) != 0 {
		return nil, fmt.Errorf("%w: %d trailing bytes", ErrInvalidVoiceBundle, len(rest))
	}
	return frames, nil
}
