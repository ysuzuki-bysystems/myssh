//go:build windows

package windows

import (
	"io"
	"syscall"
	"unsafe"
)

var kernel32 = syscall.NewLazyDLL("kernel32.dll")

var (
	procReadConsoleInput = kernel32.NewProc("ReadConsoleInputW")
)

// REF https://github.com/elves/elvish/blob/master/pkg/sys/ewindows/console.go
func readConsoleInput(h uintptr, buf []inputRecord) (int, error) {
	var nr uintptr
	r, _, err := procReadConsoleInput.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&nr)))
	if r != 0 {
		err = nil
	}
	return int(nr), err
}

func NewReader(sigwinchCh chan interface{}) io.Reader {
	return &inputRecordReader{
		sigwinchCh: sigwinchCh,
		readConsoleInput: readConsoleInput,
	}
}
