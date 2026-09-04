package voice

import (
	"testing"
	"time"
)

func TestHubRemoveInactive(t *testing.T) {
	hub := NewHub()

	now := time.Now()

	active := hub.CreateSession("alice", nil)
	inactive := hub.CreateSession("bob", nil)

	active.LastSeen = now.Add(-5 * time.Second)
	inactive.LastSeen = now.Add(-40 * time.Second)

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
