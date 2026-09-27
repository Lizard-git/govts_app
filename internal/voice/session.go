package voice

import (
	"net"
	"time"

	"uniclog.io/govts/internal/domain"
)

type Session struct {
	ID              uint64
	Name            string
	ChannelID       domain.ChannelID
	Addr            *net.UDPAddr
	LastSeen        time.Time
	MediaCredential [32]byte
	voiceSeen       map[uint32]struct{}
	voiceArrivals   []voiceSample
}

type voiceSample struct {
	sequence uint32
	at       time.Time
}
