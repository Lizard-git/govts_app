package udp

import (
	"net"
)

func ListenUDP(port int) (*net.UDPConn, error) {
	addr := &net.UDPAddr{
		Port: port,
		IP:   net.ParseIP("0.0.0.0"),
	}
	return net.ListenUDP("udp", addr)
}
