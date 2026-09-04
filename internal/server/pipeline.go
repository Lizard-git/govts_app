package server

import (
	"context"
	"log"
	"time"

	"example.com/go-voice-mvp/internal/voice"
)

const (
	SessionTimeout  = 30 * time.Second
	CleanupInterval = 5 * time.Second
)

func CleanupLoop(
	ctx context.Context,
	hub *voice.Hub,
	timeout time.Duration,
	interval time.Duration,
) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()

		case now := <-ticker.C:
			removed := hub.RemoveInactive(now, timeout)

			for _, session := range removed {
				log.Printf(
					"session timed out: id=%d name=%q",
					session.ID,
					session.Name,
				)
			}
		}
	}
}
