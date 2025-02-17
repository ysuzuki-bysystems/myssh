//go:build windows

package tty

import (
	"syscall"
	"unsafe"
)

var kernel32 = syscall.NewLazyDLL("kernel32.dll")

var (
	procReadConsoleInput = kernel32.NewProc("ReadConsoleInputW")
)

const (
	keyEvent              = 0x1
	windowBufferSizeEvent = 0x4

	vkMenu = 0x12
)

type wchar uint16
type dword uint32
type word uint16

type inputRecord struct {
	eventType word
	_         [2]byte
	event     [16]byte
}

type keyEventRecord struct {
	keyDown         int32
	repeatCount     word
	virtualKeyCode  word
	virtualScanCode word
	unicodeChar     wchar
	controlKeyState dword
}

// REF https://github.com/elves/elvish/blob/master/pkg/sys/ewindows/console.go
func readConsoleInput(h uintptr, buf []inputRecord) (int, error) {
	var nr uintptr
	r, _, err := procReadConsoleInput.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&nr)))
	if r != 0 {
		err = nil
	}
	return int(nr), err
}

