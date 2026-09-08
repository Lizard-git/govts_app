package udp

import (
	"fmt"
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
	// The extra byte makes every oversized datagram fail protocol validation,
	// even though ReadFromUDP does not return the datagram's original size.
	buffer := make([]byte, protocol.MaxDatagramSize+1)
	n, addr, err := conn.ReadFromUDP(buffer)
	if err != nil {
		if isMessageTooLong(err) {
			return nil, addr, fmt.Errorf(
				"%w: %v",
				protocol.ErrPacketTooLarge,
				err,
			)
		}
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
	data, err := protocol.EncodePacket(packet)
	if err != nil {
		return err
	}
	if _, err := conn.WriteToUDP(data, addr); err != nil {
		return err
	}
	return nil
}
