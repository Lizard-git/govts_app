// Package hotkeys registers system-wide Ctrl+Alt shortcuts. They fire while
// the window is hidden in the tray, regardless of keyboard layout, Caps Lock
// or Shift, because they bind physical keys rather than typed characters.
package hotkeys

import (
	"errors"
	"sync"
)

// ErrBusy means another program already owns the shortcut.
var ErrBusy = errors.New("hotkey is registered by another program")

// Binding is Ctrl+Alt plus Key. Key is an upper-case Latin letter or a digit.
type Binding struct {
	Action string
	Key    string
}

// Manager owns the registered shortcuts and calls the handler for each press.
// Presses run one at a time, in order, off the thread that receives them.
type Manager struct {
	mu      sync.Mutex
	closed  bool
	actions chan string
	done    chan struct{}
	backend backend
}

// Start begins listening. Close must be called to release the shortcuts.
func Start(handler func(action string)) (*Manager, error) {
	m := &Manager{actions: make(chan string, 8), done: make(chan struct{})}
	backend, err := startBackend(m.dispatch)
	if err != nil {
		return nil, err
	}
	m.backend = backend
	go func() {
		defer close(m.done)
		for action := range m.actions {
			handler(action)
		}
	}()
	return m, nil
}

// dispatch drops a press when earlier ones are still queued; a held or
// hammered shortcut must not replay long after the keys are released.
func (m *Manager) dispatch(action string) {
	select {
	case m.actions <- action:
	default:
	}
}

// Apply replaces every registered shortcut. The result lists bindings that
// could not be registered; the others stay active.
func (m *Manager) Apply(bindings []Binding) map[string]error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return nil
	}
	return m.backend.apply(bindings)
}

func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	m.mu.Unlock()
	m.backend.close()
	close(m.actions)
	<-m.done
}

type backend interface {
	apply(bindings []Binding) map[string]error
	// close returns after the last dispatch call.
	close()
}
