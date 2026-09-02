package voice

import (
	"net"
	"testing"
)

func TestValidateSessionAddr(t *testing.T) {
	hub := NewHub()

	originalAddr := &net.UDPAddr{
		IP:   net.ParseIP("127.0.0.1"),
		Port: 5000,
	}

	session := hub.CreateSession("alice", originalAddr)

	t.Run("same address", func(t *testing.T) {
		addr := &net.UDPAddr{
			IP:   net.ParseIP("127.0.0.1"),
			Port: 5000,
		}

		err := ValidateSessionAddr(hub, session.ID, addr)
		if err != nil {
			t.Fatalf("expected valid address, got %v", err)
		}
	})

	t.Run("different port", func(t *testing.T) {
		addr := &net.UDPAddr{
			IP:   net.ParseIP("127.0.0.1"),
			Port: 6000,
		}

		err := ValidateSessionAddr(hub, session.ID, addr)
		if err == nil {
			t.Fatal("expected address mismatch")
		}
	})

	t.Run("unknown session", func(t *testing.T) {
		err := ValidateSessionAddr(
			hub,
			999,
			originalAddr,
		)

		if err == nil {
			t.Fatal("expected unknown session error")
		}
	})
}
