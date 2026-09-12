package main

import (
	"errors"
	"net"
	"sync"
	"time"

	voiceclient "example.com/go-voice-mvp/internal/client"
)

func shouldReplaceClientSocket(err error) bool {
	return errors.Is(err, net.ErrClosed)
}

type reconnectBackoff struct{ next time.Duration }

func newReconnectBackoff() *reconnectBackoff { return &reconnectBackoff{next: time.Second} }
func (backoff *reconnectBackoff) Next() time.Duration {
	current := backoff.next
	if backoff.next < 4*time.Second {
		backoff.next *= 2
		if backoff.next > 4*time.Second {
			backoff.next = 4 * time.Second
		}
	}
	return current
}
func (backoff *reconnectBackoff) Reset() { backoff.next = time.Second }

type channelPreference struct {
	mu       sync.RWMutex
	selector string
	locator  voiceclient.ChannelLocator
}

func newChannelPreference(selector string) *channelPreference {
	return &channelPreference{selector: selector}
}
func (preference *channelPreference) Get() (string, voiceclient.ChannelLocator) {
	preference.mu.RLock()
	defer preference.mu.RUnlock()
	return preference.selector, append(voiceclient.ChannelLocator(nil), preference.locator...)
}
func (preference *channelPreference) Set(locator voiceclient.ChannelLocator) {
	preference.mu.Lock()
	defer preference.mu.Unlock()
	preference.selector = ""
	preference.locator = append(voiceclient.ChannelLocator(nil), locator...)
}
