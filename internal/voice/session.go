package voice

import (
	"net"
	"time"
)

type Session struct {
	ID       uint64
	Name     string
	Channel  string
	Addr     *net.UDPAddr
	LastSeen time.Time
}
