package udp

import (
	"errors"
	"net"
)

func ConnectUDP(host string, port int) (*net.UDPConn, error) {
	ip := net.ParseIP(host)
	if ip == nil {
		return nil, errors.New("invalid host")
	}
	return net.DialUDP("udp", nil, &net.UDPAddr{IP: ip, Port: port})
}
