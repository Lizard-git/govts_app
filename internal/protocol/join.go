package protocol

import (
	"encoding/binary"
	"errors"

	"uniclog.io/sonoryx/internal/domain"
)

const (
	JoinChannelRequestSize = 17
)

func EncodeJoinChannelRequest(channelID domain.ChannelID, expected uint64) ([]byte, error) {
	if channelID == 0 || expected == 0 {
		return nil, errors.New("channel ID and control revision must not be zero")
	}
	payload := make([]byte, JoinChannelRequestSize)
	payload[0] = VoiceControlVersion
	binary.BigEndian.PutUint64(payload[1:9], expected)
	binary.BigEndian.PutUint64(payload[9:17], uint64(channelID))
	return payload, nil
}

func DecodeJoinChannelRequest(payload []byte) (domain.ChannelID, uint64, error) {
	if len(payload) != JoinChannelRequestSize || payload[0] != VoiceControlVersion {
		return 0, 0, errors.New("invalid join request size or version")
	}
	id := domain.ChannelID(binary.BigEndian.Uint64(payload[9:17]))
	expected := binary.BigEndian.Uint64(payload[1:9])
	if id == 0 || expected == 0 {
		return 0, 0, errors.New("channel ID and control revision must not be zero")
	}
	return id, expected, nil
}
