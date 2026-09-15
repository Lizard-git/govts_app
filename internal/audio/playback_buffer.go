package audio

import "sync"

type playbackBuffer struct {
	mu    sync.Mutex
	data  []byte
	limit int
}

func (b *playbackBuffer) Read(dst []byte) (int, error) {
	b.read(dst)
	return len(dst), nil
}

func (b *playbackBuffer) read(dst []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := copy(dst, b.data)
	clear(dst[n:])
	b.data = b.data[n:]
}

func (b *playbackBuffer) push(data []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(data) >= b.limit {
		b.data = append(b.data[:0], data[len(data)-b.limit:]...)
		return
	}
	if excess := len(b.data) + len(data) - b.limit; excess > 0 {
		b.data = b.data[excess:]
	}
	b.data = append(b.data, data...)
}

func (b *playbackBuffer) clear() {
	b.mu.Lock()
	b.data = nil
	b.mu.Unlock()
}
