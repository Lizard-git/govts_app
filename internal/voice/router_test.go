package voice

import (
	"errors"
	"net"
	"testing"

	"example.com/go-voice-mvp/internal/protocol"
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

func TestFindRecipientsRejectsSenderWithoutChannel(t *testing.T) {
	hub := NewHub()
	sender := hub.CreateSession("alice", nil)

	_, err := FindRecipients(hub, protocol.VoicePacket{
		Type:      protocol.PacketVoice,
		SessionID: sender.ID,
	})
	if !errors.Is(err, ErrSessionNotInChannel) {
		t.Fatalf(
			"FindRecipients() error = %v, want %v",
			err,
			ErrSessionNotInChannel,
		)
	}
}

func TestFindRecipientsReturnsOnlySameChannel(t *testing.T) {
	hub := NewHub()
	sender := hub.CreateSession("alice", nil)
	sameChannel := hub.CreateSession("bob", nil)
	otherChannel := hub.CreateSession("carol", nil)

	if err := hub.JoinChannel(sender.ID, "music"); err != nil {
		t.Fatal(err)
	}
	if err := hub.JoinChannel(sameChannel.ID, "music"); err != nil {
		t.Fatal(err)
	}
	if err := hub.JoinChannel(otherChannel.ID, "gaming"); err != nil {
		t.Fatal(err)
	}

	recipients, err := FindRecipients(hub, protocol.VoicePacket{
		Type:      protocol.PacketVoice,
		SessionID: sender.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(recipients) != 1 {
		t.Fatalf("FindRecipients() returned %d sessions, want 1", len(recipients))
	}
	if recipients[0].ID != sameChannel.ID {
		t.Fatalf(
			"FindRecipients() returned session %d, want %d",
			recipients[0].ID,
			sameChannel.ID,
		)
	}
}
