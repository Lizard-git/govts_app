//go:build !windows

package apppopup

type noopBackend struct{}

func newBackend() (backend, error) { return noopBackend{}, nil }

func (noopBackend) show(title, body string) error { return nil }
func (noopBackend) close()                        {}
