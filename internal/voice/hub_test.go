package voice

import (
	"net"
	"sync"
	"testing"
	"time"
)

func TestHubReturnsIndependentSessionSnapshots(t *testing.T) {
	hub := NewHub()
	originalAddr := &net.UDPAddr{
		IP:   net.ParseIP("127.0.0.1"),
		Port: 5000,
	}

	created := hub.CreateSession("alice", originalAddr)
	originalAddr.Port = 6000
	created.Name = "changed outside Hub"
	created.Addr.IP[0]++

	first, ok := hub.Get(created.ID)
	if !ok {
		t.Fatalf("session %d not found", created.ID)
	}
	if first.Name != "alice" {
		t.Fatalf("session name = %q, want %q", first.Name, "alice")
	}
	if first.Addr.Port != 5000 || !first.Addr.IP.Equal(net.ParseIP("127.0.0.1")) {
		t.Fatalf("session address = %v, want 127.0.0.1:5000", first.Addr)
	}

	first.Channel = "changed snapshot"
	first.Addr.Port = 7000
	second, ok := hub.Get(created.ID)
	if !ok {
		t.Fatalf("session %d not found", created.ID)
	}
	if second.Channel != "" || second.Addr.Port != 5000 {
		t.Fatalf("mutating snapshot changed Hub session: %+v", second)
	}
}

func TestHubRemoveInactive(t *testing.T) {
	hub := NewHub()

	now := time.Now()

	active := hub.CreateSession("alice", nil)
	inactive := hub.CreateSession("bob", nil)

	if err := hub.touchAt(active.ID, now.Add(-5*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := hub.touchAt(inactive.ID, now.Add(-40*time.Second)); err != nil {
		t.Fatal(err)
	}

	removed := hub.RemoveInactive(
		now,
		30*time.Second,
	)

	if len(removed) != 1 {
		t.Fatalf(
			"expected 1 removed session, got %d",
			len(removed),
		)
	}

	if removed[0].ID != inactive.ID {
		t.Fatalf(
			"expected session %d to be removed, got %d",
			inactive.ID,
			removed[0].ID,
		)
	}

	if _, ok := hub.Get(inactive.ID); ok {
		t.Fatal("inactive session still exists")
	}

	if _, ok := hub.Get(active.ID); !ok {
		t.Fatal("active session was removed")
	}
}

func TestHubConcurrentJoinTouchRoutingAndCleanup(t *testing.T) {
	hub := NewHub()
	sender := hub.CreateSession("sender", nil)
	recipient := hub.CreateSession("recipient", nil)

	if err := hub.JoinChannel(sender.ID, "music"); err != nil {
		t.Fatal(err)
	}
	if err := hub.JoinChannel(recipient.ID, "music"); err != nil {
		t.Fatal(err)
	}

	const iterations = 100
	start := make(chan struct{})
	var wg sync.WaitGroup
	for worker := 0; worker < 5; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			<-start

			for i := 0; i < iterations; i++ {
				switch worker {
				case 0:
					if err := hub.JoinChannel(sender.ID, "music"); err != nil {
						t.Errorf("JoinChannel() error = %v", err)
						return
					}
				case 1:
					if err := hub.Touch(sender.ID); err != nil {
						t.Errorf("Touch() error = %v", err)
						return
					}
				case 2:
					recipients, err := hub.RecipientsFor(sender.ID)
					if err != nil {
						t.Errorf("RecipientsFor() error = %v", err)
						return
					}
					if len(recipients) != 1 {
						t.Errorf("RecipientsFor() count = %d, want 1", len(recipients))
						return
					}
				case 3:
					if removed := hub.RemoveInactive(time.Now(), time.Hour); len(removed) != 0 {
						t.Errorf("RemoveInactive() removed %d active sessions", len(removed))
						return
					}
				case 4:
					addr := &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 5000 + i}
					if err := hub.UpdateAddr(sender.ID, addr); err != nil {
						t.Errorf("UpdateAddr() error = %v", err)
						return
					}
				}
			}
		}(worker)
	}

	close(start)
	wg.Wait()
}
