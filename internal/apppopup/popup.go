// Package apppopup provides Sonoryx notification windows independently of the
// feature that requests them. Native popups are currently supported on Windows;
// other platforms accept notifications without displaying a window.
package apppopup

// Popup owns a single silent notification window. It does not activate or take
// keyboard focus. A new message replaces the current text and restarts the
// 2.8-second dismissal timer. Clicking the window dismisses it early.
type Popup struct {
	backend backend
}

// New creates a hidden popup. Call it after the application has configured DPI
// awareness, and Close it during shutdown to release its window and thread.
func New() (*Popup, error) {
	b, err := newBackend()
	if err != nil {
		return nil, err
	}
	return &Popup{backend: b}, nil
}

// Show queues plain notification text without waiting for a UI thread.
// Calls are safe from multiple goroutines; only the latest pending text is kept.
func (p *Popup) Show(title, body string) error {
	return p.backend.show(title, body)
}

// Close releases the popup's resources. Repeated calls are harmless.
func (p *Popup) Close() {
	p.backend.close()
}

type backend interface {
	show(title, body string) error
	close()
}
