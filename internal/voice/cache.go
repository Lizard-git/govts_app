package voice

import (
	"sync"

	"example.com/go-voice-mvp/internal/protocol"
)

type requestKey struct {
	SessionID uint64
	RequestID uint32
}
type RequestCache struct {
	mu sync.Mutex

	responses map[requestKey]protocol.VoicePacket
}

func NewRequestCache() *RequestCache {
	return &RequestCache{
		responses: make(
			map[requestKey]protocol.VoicePacket,
		),
	}
}

func (c *RequestCache) Get(
	sessionID uint64,
	requestID uint32,
) (protocol.VoicePacket, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	packet, ok := c.responses[requestKey{
		sessionID,
		requestID,
	}]
	return packet, ok
}

func (c *RequestCache) Put(
	sessionID uint64,
	requestID uint32,
	response protocol.VoicePacket,
) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.responses[requestKey{
		SessionID: sessionID,
		RequestID: requestID,
	}] = response
}
