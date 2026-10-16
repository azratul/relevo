//go:build windows

package adapter

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/azratul/relevo/internal/protocol"
)

// win injects with SendInput and verifies focus with GetForegroundWindow.
// UIPI blocks input into processes with a higher integrity level (e.g. an
// application run as administrator) unless relevo-host runs at that level too;
// we do not elevate (ADR-0007).
type win struct{}

var (
	user32                       = windows.NewLazySystemDLL("user32.dll")
	procSendInput                = user32.NewProc("SendInput")
	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	procGetWindowTextW           = user32.NewProc("GetWindowTextW")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procMapVirtualKeyW           = user32.NewProc("MapVirtualKeyW")
)

func init() {
	Register(BackendInfo{Name: "windows", Description: "Windows SendInput, checks the foreground window", Status: "compiles"},
		func() (KeyBackend, error) { return &win{}, nil })
}

func (w *win) Name() string          { return "windows" }
func (w *win) Platform() string      { return "windows" }
func (w *win) FocusCheck() string    { return protocol.FocusVerified }
func (w *win) Confirmation() string  { return protocol.ConfirmInputInjected }
func (w *win) Permissions() []string { return nil }
func (w *win) Check(context.Context) error {
	return procSendInput.Find()
}

var vk = map[string]uint16{
	"Left": 0x25, "Up": 0x26, "Right": 0x27, "Down": 0x28, "Page_Up": 0x21, "Page_Down": 0x22, "End": 0x23, "Home": 0x24,
	"space": 0x20, "Return": 0x0D, "Escape": 0x1B, "BackSpace": 0x08, "Tab": 0x09, "period": 0xBE,
	"ctrl": 0x11, "alt": 0x12, "shift": 0x10, "super": 0x5B,
}

var extended = map[uint16]bool{0x21: true, 0x22: true, 0x23: true, 0x24: true, 0x25: true, 0x26: true, 0x27: true, 0x28: true, 0x5B: true}

func init() {
	for i := 0; i < 12; i++ {
		vk[fmt.Sprintf("F%d", i+1)] = 0x70 + uint16(i)
	}
	for c := 'a'; c <= 'z'; c++ {
		vk[string(c)] = uint16(c - 'a' + 'A')
	}
	for c := '0'; c <= '9'; c++ {
		vk[string(c)] = uint16(c)
	}
}

type keybdInput struct {
	wVk, wScan  uint16
	dwFlags     uint32
	time        uint32
	dwExtraInfo uintptr
}

type input struct {
	typ uint32
	ki  keybdInput
	_   [8]byte // union padding to sizeof(MOUSEINPUT)
}

const (
	inputKeyboard   = 1
	keyeventfExtKey = 0x0001
	keyeventfKeyUp  = 0x0002
)

func keyInput(code uint16, up bool) input {
	scan, _, _ := procMapVirtualKeyW.Call(uintptr(code), 0)
	f := uint32(0)
	if extended[code] {
		f |= keyeventfExtKey
	}
	if up {
		f |= keyeventfKeyUp
	}
	return input{typ: inputKeyboard, ki: keybdInput{wVk: code, wScan: uint16(scan), dwFlags: f}}
}

func sendInputs(in []input) (int, error) {
	n, _, err := procSendInput.Call(uintptr(len(in)), uintptr(unsafe.Pointer(&in[0])), unsafe.Sizeof(in[0]))
	if int(n) != len(in) {
		return int(n), fmt.Errorf("SendInput inserted %d of %d events (blocked by UIPI or another process?): %v", n, len(in), err)
	}
	return int(n), nil
}

func (w *win) Send(ctx context.Context, _ Window, c Chord) error {
	code, ok := vk[c.Key]
	if !ok {
		return Reject(protocol.HostNotReady, "key %s not mapped on Windows", c.Key)
	}
	var down, up []input
	for _, m := range c.Mods {
		down = append(down, keyInput(vk[m], false))
	}
	down = append(down, keyInput(code, false))
	up = append(up, keyInput(code, true))
	for i := len(c.Mods) - 1; i >= 0; i-- {
		up = append(up, keyInput(vk[c.Mods[i]], true))
	}
	all := append(down, up...)
	n, err := sendInputs(all)
	if err == nil {
		return nil
	}
	if n == 0 {
		return err // nothing was inserted
	}
	// Partial: release everything we may have pressed.
	_, _ = sendInputs(up)
	return fmt.Errorf("%w: %v", ErrUncertain, err)
}

func windowInfo(h windows.HWND) Window {
	out := Window{ID: fmt.Sprintf("0x%x", uintptr(h))}
	buf := make([]uint16, 512)
	n, _, _ := procGetWindowTextW.Call(uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	out.Title = syscall.UTF16ToString(buf[:n])
	var pid uint32
	procGetWindowThreadProcessId.Call(uintptr(h), uintptr(unsafe.Pointer(&pid)))
	if ph, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid); err == nil {
		defer windows.CloseHandle(ph)
		name := make([]uint16, windows.MAX_PATH)
		size := uint32(len(name))
		if windows.QueryFullProcessImageName(ph, 0, &name[0], &size) == nil {
			out.Class = strings.ToLower(filepath.Base(syscall.UTF16ToString(name[:size])))
		}
	}
	return out
}

func (w *win) Active(context.Context) (Window, error) {
	h, _, _ := procGetForegroundWindow.Call()
	if h == 0 {
		return Window{}, errors.New("no foreground window")
	}
	return windowInfo(windows.HWND(h)), nil
}

func (w *win) Windows(context.Context) ([]Window, error) {
	var out []Window
	cb := syscall.NewCallback(func(h uintptr, _ uintptr) uintptr {
		if v, _, _ := procIsWindowVisible.Call(h); v != 0 {
			if wi := windowInfo(windows.HWND(h)); wi.Title != "" {
				out = append(out, wi)
			}
		}
		return 1
	})
	procEnumWindows.Call(cb, 0)
	return out, nil
}
