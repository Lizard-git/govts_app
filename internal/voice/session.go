package voice

import (
	"net"
	"time"

	"example.com/go-voice-mvp/internal/domain"
)

type Session struct {
	ID              uint64
	Name            string
	ChannelID       domain.ChannelID
	Addr            *net.UDPAddr
	LastSeen        time.Time
	MediaCredential [32]byte
}
