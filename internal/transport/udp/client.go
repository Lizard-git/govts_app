package udp

import (
	"errors"
	"net"

	"example.com/go-voice-mvp/internal/protocol"
)

func ConnectUDP(host string, port int) (*net.UDPConn, error) {
	ip := net.ParseIP(host)
	if ip == nil {
		return nil, errors.New("invalid host")
	}
	return net.DialUDP("udp", nil, &net.UDPAddr{IP: ip, Port: port})
}

func SendPacket(conn *net.UDPConn, packet protocol.VoicePacket) error {
	data := protocol.EncodePacket(packet)
	if _, err := conn.Write(data); err != nil {
		return err
	}
	return nil
}

func ReceivePacket(conn *net.UDPConn) (protocol.VoicePacket, error) {
	buffer := make([]byte, 1500)
	n, err := conn.Read(buffer)
	if err != nil {
		return protocol.VoicePacket{}, err
	}
	return protocol.DecodePacket(buffer[:n])
}
