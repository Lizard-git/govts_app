package protocol

import "net/netip"

type PacketDirection uint8

const (
	DirectionClientToServer PacketDirection = iota + 1
	DirectionServerToClient
)

type DatagramContext struct {
	Direction  PacketDirection
	KeyOwnerID uint64
	Endpoint   netip.AddrPort
}

// DatagramCodec converts logical packets to and from their wire
// representation. Implementations may add framing, authentication or
// encryption without changing the UDP transport or packet-processing loops.
type DatagramCodec interface {
	Encode(ctx DatagramContext, packet VoicePacket) ([]byte, error)
	Decode(ctx DatagramContext, datagram []byte) (VoicePacket, error)
}
