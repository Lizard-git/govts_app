package udp

import (
	"errors"
	"net"
	"testing"
	"time"
	"uniclog.io/sonoryx/internal/protocol"
)

func TestVoiceDeadlineExpiresWhileWaitingForUDPWriter(t *testing.T) {
	raw, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	c := &packetConn{conn: raw}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	started := time.Now()
	done := make(chan error, 1)
	go func() {
		done <- c.write(protocol.DatagramContext{}, protocol.VoicePacket{}, func([]byte) error { t.Error("expired voice reached socket"); return nil }, started.Add(20*time.Millisecond))
	}()
	select {
	case err := <-done:
		if !errors.Is(err, ErrSendExpired) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("voice waited for control writer beyond its deadline")
	}
}

func TestExpiredVoiceDoesNotAcquireWriterOrEncode(t *testing.T) {
	c := &packetConn{}
	if err := c.write(protocol.DatagramContext{}, protocol.VoicePacket{}, nil, time.Now().Add(-time.Second)); !errors.Is(err, ErrSendExpired) {
		t.Fatal(err)
	}
	if !c.writeMu.LockBefore(time.Now().Add(time.Second)) {
		t.Fatal("expired send retained writer lock")
	}
	c.writeMu.Unlock()
}
