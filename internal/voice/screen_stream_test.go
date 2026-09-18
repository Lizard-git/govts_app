package voice

import (
	"errors"
	"net"
	"testing"

	"example.com/go-voice-mvp/internal/domain"
)

func TestScreenStreamsAllowMultipleAuthorsAndOnePerOwner(t *testing.T) {
	hub := NewHub()
	alice := &Session{ID: 10, Name: "alice", Addr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 10010}}
	bob := &Session{ID: 20, Name: "bob", Addr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 10020}}
	hub.Add(alice)
	hub.Add(bob)
	if err := hub.JoinChannel(alice.ID, DefaultChannelID); err != nil {
		t.Fatal(err)
	}
	if err := hub.JoinChannel(bob.ID, DefaultChannelID); err != nil {
		t.Fatal(err)
	}
	if _, err := hub.StartScreenShare(alice.ID, 101); err != nil {
		t.Fatal(err)
	}
	if _, err := hub.StartScreenShare(bob.ID, 202); err != nil {
		t.Fatal(err)
	}
	if _, err := hub.StartScreenShare(alice.ID, 303); !errors.Is(err, ErrScreenStreamExists) {
		t.Fatalf("second stream error = %v", err)
	}
	streams := hub.ScreenStreams()
	if len(streams) != 2 || streams[0].ID != 101 || streams[1].ID != 202 {
		t.Fatalf("streams = %+v", streams)
	}
	if !hub.CanSubscribeScreen(alice.ID, domain.StreamID(202)) {
		t.Fatal("channel member cannot subscribe")
	}
}

func TestScreenStreamStopsOnMoveAndRemove(t *testing.T) {
	hub := NewHub()
	other := mustCreateChannel(t, hub, domain.Channel{Name: "other"})
	alice := &Session{ID: 10, Name: "alice", Addr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 10010}}
	hub.Add(alice)
	if err := hub.JoinChannel(alice.ID, DefaultChannelID); err != nil {
		t.Fatal(err)
	}
	if _, err := hub.StartScreenShare(alice.ID, 101); err != nil {
		t.Fatal(err)
	}
	if err := hub.JoinChannel(alice.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := hub.ScreenStream(101); ok {
		t.Fatal("stream survived channel move")
	}
	if _, err := hub.StartScreenShare(alice.ID, 102); err != nil {
		t.Fatal(err)
	}
	hub.Remove(alice.ID)
	if _, ok := hub.ScreenStream(102); ok {
		t.Fatal("stream survived session removal")
	}
}

func TestScreenSubscriptionRequiresSameChannel(t *testing.T) {
	hub := NewHub()
	other := mustCreateChannel(t, hub, domain.Channel{Name: "other"})
	alice := &Session{ID: 10, Name: "alice", Addr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 10010}}
	bob := &Session{ID: 20, Name: "bob", Addr: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 10020}}
	hub.Add(alice)
	hub.Add(bob)
	if err := hub.JoinChannel(alice.ID, DefaultChannelID); err != nil {
		t.Fatal(err)
	}
	if err := hub.JoinChannel(bob.ID, other.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := hub.StartScreenShare(alice.ID, 101); err != nil {
		t.Fatal(err)
	}
	if hub.CanSubscribeScreen(bob.ID, 101) {
		t.Fatal("other channel can subscribe")
	}
}
