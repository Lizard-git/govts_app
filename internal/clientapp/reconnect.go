package clientapp

import (
	"errors"
	"net"
	"time"
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
