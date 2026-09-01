package udp

import (
	"net"

	"example.com/go-voice-mvp/internal/protocol"
)

func ListenUDP(port int) (*net.UDPConn, error) {
	addr := &net.UDPAddr{
		Port: port,
		IP:   net.ParseIP("0.0.0.0"),
	}
	return net.ListenUDP("udp", addr)
}

func ReadPacket(conn *net.UDPConn) ([]byte, *net.UDPAddr, error) {
	buffer := make([]byte, 1500)
	n, addr, err := conn.ReadFromUDP(buffer)
	if err != nil {
		return nil, nil, err
	}
	return buffer[:n], addr, nil
}

func ReadVoicePacket(conn *net.UDPConn) (protocol.VoicePacket, *net.UDPAddr, error) {
	payload, addr, err := ReadPacket(conn)
	if err != nil {
		return protocol.VoicePacket{}, nil, err
	}
	packet, err := protocol.DecodePacket(payload)
	if err != nil {
		return protocol.VoicePacket{}, addr, err
	}
	return packet, addr, nil
}

func WriteVoicePacket(conn *net.UDPConn, addr *net.UDPAddr, packet protocol.VoicePacket) error {
	data := protocol.EncodePacket(packet)
	if _, err := conn.WriteToUDP(data, addr); err != nil {
		return err
	}
	return nil
}
