package udp

import (
	"errors"
	"fmt"
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
	data, err := protocol.EncodePacket(packet)
	if err != nil {
		return err
	}
	if _, err := conn.Write(data); err != nil {
		return err
	}
	return nil
}

func ReceivePacket(conn *net.UDPConn) (protocol.VoicePacket, error) {
	// The extra byte lets DecodePacket distinguish an oversized datagram from a
	// valid datagram whose size is exactly MaxDatagramSize.
	buffer := make([]byte, protocol.MaxDatagramSize+1)
	n, err := conn.Read(buffer)
	if err != nil {
		if isMessageTooLong(err) {
			return protocol.VoicePacket{}, fmt.Errorf(
				"%w: %v",
				protocol.ErrPacketTooLarge,
				err,
			)
		}
		return protocol.VoicePacket{}, err
	}
	return protocol.DecodePacket(buffer[:n])
}
