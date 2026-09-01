package voice

import "net"

type Session struct {
	ID      uint64
	Name    string
	Channel string
	Addr    *net.UDPAddr
}

func (s *Session) JoinChannel(name string) {
	s.Channel = name
}
