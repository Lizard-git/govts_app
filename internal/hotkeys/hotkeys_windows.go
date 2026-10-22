//go:build windows

package hotkeys

import (
	"errors"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	modAlt      = 0x0001
	modControl  = 0x0002
	modShift    = 0x0004
	modNoRepeat = 0x4000

	wmQuit   = 0x0012
	wmHotkey = 0x0312
	wmApply  = 0x8000 // WM_APP

	pmNoRemove = 0

	errorHotkeyAlreadyRegistered = windows.Errno(1409)
)

var (
	user32                 = windows.NewLazySystemDLL("user32.dll")
	procRegisterHotKey     = user32.NewProc("RegisterHotKey")
	procUnregisterHotKey   = user32.NewProc("UnregisterHotKey")
	procGetMessageW        = user32.NewProc("GetMessageW")
	procPeekMessageW       = user32.NewProc("PeekMessageW")
	procPostThreadMessageW = user32.NewProc("PostThreadMessageW")
)

type message struct {
	hwnd     uintptr
	message  uint32
	wParam   uintptr
	lParam   uintptr
	time     uint32
	x, y     int32
	lPrivate uint32
}

type applyRequest struct {
	bindings []Binding
	reply    chan map[string]error
}

// windowsBackend registers thread hotkeys without a window: WM_HOTKEY is
// posted to the queue of the thread that called RegisterHotKey, so the
// registrations and the message loop share one locked OS thread.
type windowsBackend struct {
	thread   uint32
	requests chan applyRequest
	done     chan struct{}
}

func startBackend(dispatch func(action string)) (backend, error) {
	b := &windowsBackend{requests: make(chan applyRequest, 1), done: make(chan struct{})}
	ready := make(chan uint32)
	go b.run(dispatch, ready)
	b.thread = <-ready
	return b, nil
}

func (b *windowsBackend) run(dispatch func(action string), ready chan<- uint32) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(b.done)
	var msg message
	// The first peek creates the thread's message queue; a post sent before
	// that would be lost.
	procPeekMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0, pmNoRemove)
	ready <- windows.GetCurrentThreadId()
	registered := make(map[uintptr]string)
	defer unregisterAll(registered)
	for {
		result, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(result) <= 0 {
			return
		}
		switch msg.message {
		case wmHotkey:
			if action, ok := registered[msg.wParam]; ok {
				dispatch(action)
			}
		case wmApply:
			select {
			case request := <-b.requests:
				unregisterAll(registered)
				request.reply <- registerAll(registered, request.bindings)
			default:
			}
		}
	}
}

func registerAll(registered map[uintptr]string, bindings []Binding) map[string]error {
	failed := make(map[string]error)
	for i, binding := range bindings {
		if len(binding.Key) != 1 {
			failed[binding.Action] = errors.New("invalid hotkey key")
			continue
		}
		// Letters and digits have the virtual-key code of their upper-case
		// ASCII character in every keyboard layout.
		key := uintptr(binding.Key[0])
		id := uintptr(i*2 + 1)
		result, _, err := procRegisterHotKey.Call(0, id, modControl|modAlt|modNoRepeat, key)
		if result == 0 {
			if errors.Is(err, errorHotkeyAlreadyRegistered) {
				err = ErrBusy
			}
			failed[binding.Action] = err
			continue
		}
		registered[id] = binding.Action
		// Shift does not change the shortcut, the same way Caps Lock does not.
		// Another program owning only the Shift variant is not an error.
		if result, _, _ := procRegisterHotKey.Call(0, id+1, modControl|modAlt|modShift|modNoRepeat, key); result != 0 {
			registered[id+1] = binding.Action
		}
	}
	return failed
}

func unregisterAll(registered map[uintptr]string) {
	for id := range registered {
		procUnregisterHotKey.Call(0, id)
		delete(registered, id)
	}
}

func (b *windowsBackend) apply(bindings []Binding) map[string]error {
	reply := make(chan map[string]error, 1)
	b.requests <- applyRequest{bindings: bindings, reply: reply}
	if result, _, err := procPostThreadMessageW.Call(uintptr(b.thread), wmApply, 0, 0); result == 0 {
		<-b.requests
		return failAll(bindings, err)
	}
	select {
	case failed := <-reply:
		return failed
	case <-b.done:
		return failAll(bindings, errors.New("hotkey listener stopped"))
	}
}

func (b *windowsBackend) close() {
	procPostThreadMessageW.Call(uintptr(b.thread), wmQuit, 0, 0)
	<-b.done
}

func failAll(bindings []Binding, err error) map[string]error {
	failed := make(map[string]error, len(bindings))
	for _, binding := range bindings {
		failed[binding.Action] = err
	}
	return failed
}
