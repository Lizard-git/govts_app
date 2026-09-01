package protocol

import (
	"errors"
	"log"
	"net"
)

func ServeUDP(conn *net.UDPConn, hub *Hub) error {
	for {
		packet, addr, err := readVoicePacket(conn)
		if err != nil {
			log.Printf("bad packet: %v", err)
			continue
		}
		log.Printf(
			"recv type=%d session=%d sequence=%d bytes=%d",
			packet.Type,
			packet.SessionID,
			packet.Sequence,
			len(packet.Payload),
		)
		if err := HandlePacket(conn, hub, packet, addr); err != nil {
			log.Printf("cannot handle packet: %v", err)
			continue
		}
	}
}

func ConnectUDP(host string, port int) (*net.UDPConn, error) {
	ip := net.ParseIP(host)
	if ip == nil {
		return nil, errors.New("invalid host")
	}
	return net.DialUDP("udp", nil, &net.UDPAddr{IP: ip, Port: port})
}

func SendPacket(conn *net.UDPConn, packet VoicePacket) error {
	data := EncodePacket(packet)
	if _, err := conn.Write(data); err != nil {
		return err
	}
	return nil
}

func ReceivePacket(conn *net.UDPConn) (VoicePacket, error) {
	buffer := make([]byte, 1500)
	n, err := conn.Read(buffer)
	if err != nil {
		return VoicePacket{}, err
	}
	return decodePacket(buffer[:n])
}

func ListenUDP(port int) (*net.UDPConn, error) {
	addr := &net.UDPAddr{
		Port: port,
		IP:   net.ParseIP("0.0.0.0"),
	}
	return net.ListenUDP("udp", addr)
}

func readPacket(conn *net.UDPConn) ([]byte, *net.UDPAddr, error) {
	buffer := make([]byte, 1500)
	n, addr, err := conn.ReadFromUDP(buffer)
	if err != nil {
		return nil, nil, err
	}
	return buffer[:n], addr, nil
}

func readVoicePacket(conn *net.UDPConn) (VoicePacket, *net.UDPAddr, error) {
	payload, addr, err := readPacket(conn)
	if err != nil {
		return VoicePacket{}, nil, err
	}
	packet, err := decodePacket(payload)
	if err != nil {
		return VoicePacket{}, addr, err
	}
	return packet, addr, nil
}

func WriteVoicePacket(conn *net.UDPConn, addr *net.UDPAddr, packet VoicePacket) error {
	data := EncodePacket(packet)
	if _, err := conn.WriteToUDP(data, addr); err != nil {
		return err
	}
	return nil
}
