//go:build windows

package apppopup

import (
	"errors"
	"log"
	"runtime"
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/wailsapp/wails/v3/pkg/w32"
	"golang.org/x/sys/windows"
)

const (
	wmPopupUpdate   = w32.WM_APP + 1
	popupTimerID    = 1
	defaultLifetime = 2800 * time.Millisecond
)

var (
	popupClassName = windows.StringToUTF16Ptr("SonoryxAppPopup")
	popupWndProc   = windows.NewCallback(popupWindowProc)
	popupWindows   sync.Map // HWND -> *window; no Go pointers in Win32 userdata.
	popupClassOnce sync.Once
	popupClassErr  error
	popupGDI       = windows.NewLazySystemDLL("gdi32.dll")
	popupUser32    = windows.NewLazySystemDLL("user32.dll")
	createRoundRgn = popupGDI.NewProc("CreateRoundRectRgn")
	setWindowRgn   = popupUser32.NewProc("SetWindowRgn")
)

type popupText struct{ title, body string }

// window owns the native window and its message-loop thread.
type window struct {
	mu      sync.Mutex
	closed  bool
	pending bool
	latest  popupText
	hwnd    w32.HWND
	done    chan struct{}

	// The following fields belong exclusively to the popup thread.
	current  popupText
	dpi      int
	deadline time.Time
}

func newBackend() (backend, error) {
	p := &window{done: make(chan struct{}), dpi: 96}
	ready := make(chan error, 1)
	go p.run(ready)
	if err := <-ready; err != nil {
		<-p.done
		return nil, err
	}
	return p, nil
}

func (p *window) run(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(p.done)
	instance := w32.GetModuleHandle("")
	popupClassOnce.Do(func() {
		class := w32.WNDCLASSEX{
			Size:    uint32(unsafe.Sizeof(w32.WNDCLASSEX{})),
			WndProc: popupWndProc, Instance: instance, ClassName: popupClassName,
			Cursor: w32.LoadCursorWithResourceID(0, w32.IDC_ARROW),
		}
		if w32.RegisterClassEx(&class) == 0 {
			popupClassErr = errors.New("register app popup window class failed")
		}
	})
	if popupClassErr != nil {
		ready <- popupClassErr
		return
	}
	p.hwnd = w32.CreateWindowEx(
		w32.WS_EX_NOACTIVATE|w32.WS_EX_TOOLWINDOW|w32.WS_EX_TOPMOST,
		popupClassName, windows.StringToUTF16Ptr("Sonoryx"), w32.WS_POPUP,
		0, 0, 360, 128, 0, 0, instance, nil,
	)
	if p.hwnd == 0 {
		ready <- errors.New("create app popup window failed")
		return
	}
	popupWindows.Store(p.hwnd, p)
	defer popupWindows.Delete(p.hwnd)
	defer w32.DestroyWindow(p.hwnd)
	ready <- nil
	var msg w32.MSG
	for {
		// GetMessage returns a signed 32-bit BOOL, even in a 64-bit process.
		result := int32(w32.GetMessage(&msg, 0, 0, 0))
		if result <= 0 {
			if result < 0 {
				log.Printf("app popup message loop failed")
			}
			return
		}
		w32.TranslateMessage(&msg)
		w32.DispatchMessage(&msg)
	}
}

func (p *window) show(title, body string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	select {
	case <-p.done:
		return errors.New("app popup stopped")
	default:
	}
	if p.closed {
		return nil
	}
	p.latest = popupText{title, body}
	if !p.pending {
		if !w32.PostMessage(p.hwnd, wmPopupUpdate, 0, 0) {
			return errors.New("post app popup update failed")
		}
		p.pending = true
	}
	return nil
}

func (p *window) close() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	posted := w32.PostMessage(p.hwnd, w32.WM_CLOSE, 0, 0)
	p.mu.Unlock()
	if posted {
		<-p.done
	}
}

func popupWindowProc(hwnd uintptr, msg uint32, wp, lp uintptr) uintptr {
	handle := w32.HWND(hwnd)
	value, ok := popupWindows.Load(handle)
	if !ok {
		return w32.DefWindowProc(handle, msg, wp, lp)
	}
	p := value.(*window)
	switch msg {
	case wmPopupUpdate:
		p.mu.Lock()
		p.current, p.pending = p.latest, false
		closed := p.closed
		p.mu.Unlock()
		if !closed {
			p.display()
		}
		return 0
	case w32.WM_TIMER:
		if wp == popupTimerID && !time.Now().Before(p.deadline) {
			p.hide()
		}
		return 0
	case w32.WM_LBUTTONUP:
		p.hide()
		return 0
	case w32.WM_MOUSEACTIVATE:
		return 3 // MA_NOACTIVATE: clicking dismisses without stealing focus.
	case w32.WM_PAINT:
		p.paint()
		return 0
	case w32.WM_ERASEBKGND:
		return 1 // paint fills the complete client area.
	case w32.WM_CLOSE:
		p.hide()
		w32.PostQuitMessage(0)
		return 0
	}
	return w32.DefWindowProc(handle, msg, wp, lp)
}

func (p *window) px(value int) int { return (value*p.dpi + 48) / 96 }

func (p *window) display() {
	// Follow the monitor the user is working on; respect taskbars on any edge.
	foreground := w32.GetForegroundWindow()
	monitor := w32.MonitorFromWindow(foreground, w32.MONITOR_DEFAULTTOPRIMARY)
	info := w32.MONITORINFO{CbSize: uint32(unsafe.Sizeof(w32.MONITORINFO{}))}
	if !w32.GetMonitorInfo(monitor, &info) {
		return
	}
	p.dpi = int(w32.GetDpiForWindow(foreground))
	if p.dpi == 0 {
		p.dpi = 96
	}
	width := min(p.px(360), int(info.RcWork.Right-info.RcWork.Left))
	height := min(p.px(128), int(info.RcWork.Bottom-info.RcWork.Top))
	margin := p.px(20)
	x := max(int(info.RcWork.Left), int(info.RcWork.Right)-width-margin)
	y := max(int(info.RcWork.Top), int(info.RcWork.Bottom)-height-margin)
	w32.SetWindowPos(p.hwnd, w32.HWND_TOPMOST, x, y, width, height, w32.SWP_NOACTIVATE)
	rgn, _, _ := createRoundRgn.Call(0, 0, uintptr(width+1), uintptr(height+1), uintptr(p.px(18)), uintptr(p.px(18)))
	if rgn != 0 {
		// On success the window owns the region; on failure we still own it.
		if result, _, _ := setWindowRgn.Call(uintptr(p.hwnd), rgn, 1); result == 0 {
			w32.DeleteObject(w32.HGDIOBJ(rgn))
		}
	}
	p.deadline = time.Now().Add(defaultLifetime)
	if w32.SetTimer(p.hwnd, popupTimerID, uint32(defaultLifetime/time.Millisecond), 0) == 0 {
		// Never leave an undismissed popup if the timer could not be created.
		p.hide()
		log.Printf("app popup timer failed")
		return
	}
	w32.InvalidateRect(p.hwnd, nil, false)
	w32.ShowWindow(p.hwnd, w32.SW_SHOWNOACTIVATE)
	w32.UpdateWindow(p.hwnd)
}

func (p *window) hide() {
	w32.KillTimer(p.hwnd, popupTimerID)
	w32.ShowWindow(p.hwnd, w32.SW_HIDE)
}

func (p *window) paint() {
	var ps w32.PAINTSTRUCT
	dc := w32.BeginPaint(p.hwnd, &ps)
	defer w32.EndPaint(p.hwnd, &ps)
	if dc == 0 {
		return
	}
	rect := w32.GetClientRect(p.hwnd)
	border := w32.CreateSolidBrush(w32.COLORREF(w32.RGB(59, 75, 102)))
	background := w32.CreateSolidBrush(w32.COLORREF(w32.RGB(28, 37, 57)))
	accent := w32.CreateSolidBrush(w32.COLORREF(w32.RGB(126, 199, 186)))
	defer w32.DeleteObject(w32.HGDIOBJ(border))
	defer w32.DeleteObject(w32.HGDIOBJ(background))
	defer w32.DeleteObject(w32.HGDIOBJ(accent))
	w32.FillRect(dc, rect, border)
	inner := w32.RECT{Left: 1, Top: 1, Right: rect.Right - 1, Bottom: rect.Bottom - 1}
	w32.FillRect(dc, &inner, background)
	bar := w32.RECT{Left: int32(p.px(15)), Top: int32(p.px(18)), Right: int32(p.px(18)), Bottom: rect.Bottom - int32(p.px(18))}
	w32.FillRect(dc, &bar, accent)
	w32.SetBkMode(dc, w32.TRANSPARENT)
	left, right := int32(p.px(30)), rect.Right-int32(p.px(22))
	p.drawText(dc, "SONORYX", 11, w32.FW_SEMIBOLD, w32.RGB(126, 199, 186),
		w32.RECT{Left: left, Top: int32(p.px(15)), Right: right, Bottom: int32(p.px(31))}, w32.DT_SINGLELINE)
	p.drawText(dc, p.current.title, 16, w32.FW_SEMIBOLD, w32.RGB(239, 244, 250),
		w32.RECT{Left: left, Top: int32(p.px(38)), Right: right, Bottom: int32(p.px(83))}, w32.DT_WORDBREAK)
	p.drawText(dc, p.current.body, 12, w32.FW_NORMAL, w32.RGB(166, 183, 205),
		w32.RECT{Left: left, Top: int32(p.px(98)), Right: right, Bottom: int32(p.px(116))}, w32.DT_SINGLELINE)
}

func (p *window) drawText(dc w32.HDC, text string, size, weight int, colour uint32, rect w32.RECT, flags uint32) {
	fontSpec := w32.LOGFONT{Height: -int32(p.px(size)), Weight: int32(weight), Quality: w32.CLEARTYPE_QUALITY}
	copy(fontSpec.FaceName[:], utf16.Encode([]rune("Segoe UI")))
	font := w32.CreateFontIndirect(&fontSpec)
	if font != 0 {
		previous := w32.SelectObject(dc, w32.HGDIOBJ(font))
		defer w32.DeleteObject(w32.HGDIOBJ(font))
		defer w32.SelectObject(dc, previous)
	}
	w32.SetTextColor(dc, w32.COLORREF(colour))
	// Text goes directly to GDI, without an HTML template or script interpreter.
	encoded := utf16.Encode([]rune(text))
	if len(encoded) != 0 {
		w32.DrawText(dc, encoded, len(encoded), &rect, flags|w32.DT_NOPREFIX|w32.DT_END_ELLIPSIS)
	}
}
