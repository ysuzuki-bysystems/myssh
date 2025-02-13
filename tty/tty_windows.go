//go:build windows

package tty

import (
	"context"
	"log"
	"os"
	"sync"
	"syscall"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/term"
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

type termState struct {
	stin  uint32
	stout uint32
}

func makeRaw(stdinfd, stdoutfd int) (*termState, error) {
	// https://github.com/golang/term/blob/743b2709ab25357d30ce1eac4a840eb0b7deb1bf/term_windows.go#L23
	// https://github.com/PowerShell/openssh-portable/blob/8fe096c7b7c7c51afd1d18654ec652187e85921b/contrib/win32/win32compat/console.c#L129

	var raw uint32

	var stin uint32
	if err := windows.GetConsoleMode(windows.Handle(stdinfd), &stin); err != nil {
		return nil, err
	}

	raw = stin &^ (windows.ENABLE_LINE_INPUT | windows.ENABLE_ECHO_INPUT | windows.ENABLE_PROCESSED_INPUT | windows.ENABLE_MOUSE_INPUT)
	raw |= windows.ENABLE_WINDOW_INPUT | windows.ENABLE_VIRTUAL_TERMINAL_INPUT

	if err := windows.SetConsoleMode(windows.Handle(stdinfd), raw); err != nil {
		return nil, err
	}

	var stout uint32

	if err := windows.GetConsoleMode(windows.Handle(stdoutfd), &stout); err != nil {
		_ = windows.SetConsoleMode(windows.Handle(stdinfd), stin)
		return nil, err
	}
	raw = stout | windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING | windows.DISABLE_NEWLINE_AUTO_RETURN

	if err := windows.SetConsoleMode(windows.Handle(stdoutfd), raw); err != nil {
		_ = windows.SetConsoleMode(windows.Handle(stdinfd), stin)
		return nil, err
	}

	return &termState{stin: stin, stout: stout}, nil
}

func termRestore(stdinfd, stdoutfd int, state *termState) error {
	err1 := windows.SetConsoleMode(windows.Handle(stdinfd), state.stin)
	err2 := windows.SetConsoleMode(windows.Handle(stdoutfd), state.stout)

	if err1 != nil {
		return err1
	}
	if err2 != nil {
		return err2
	}

	return nil
}

type tty struct {
	cancel     context.CancelFunc
	wg         *sync.WaitGroup
	sigwinchCh chan interface{}

	rem      []byte
	fragment rune
}

func openTty(sigwinchCh chan interface{}) (*tty, error) {
	wg := new(sync.WaitGroup)
	cx, cancel := context.WithCancel(context.Background())

	prev, err := makeRaw(int(os.Stdin.Fd()), int(os.Stdout.Fd()))
	if err != nil {
		cancel()
		return nil, err
	}
	wg.Add(1)
	context.AfterFunc(cx, func() {
		defer wg.Done()

		if err := termRestore(int(os.Stdin.Fd()), int(os.Stdout.Fd()), prev); err != nil {
			log.Println(err)
		}
	})

	return &tty{
		cancel:     cancel,
		wg:         wg,
		sigwinchCh: sigwinchCh,
	}, nil
}

func (t *tty) close() error {
	t.cancel()

	t.wg.Wait()
	return nil
}

func (t *tty) read(p []byte) (int, error) {
	var buf []byte

	if t.rem != nil {
		buf = t.rem
	} else {
		// TODO WaitForSingleObjectEx
		// OpenSSH (Windows) https://github.com/PowerShell/openssh-portable/blob/8fe096c7b7c7c51afd1d18654ec652187e85921b/contrib/win32/win32compat/tncon.c#L95-L104
		// WezTerm too.

		fragment := t.fragment

		// https://github.com/microsoft/terminal/blob/8b78be5f4ae40f720d980ed41075cd11e9eb0814/samples/ReadConsoleInputStream/ReadConsoleInputStream.cs#L67

		var recs [1024]inputRecord

		nr, err := readConsoleInput(os.Stdin.Fd(), recs[:])
		if err != nil {
			return 0, err
		}

		buf = make([]byte, 0)
		for _, rec := range recs[:nr] {
			switch rec.eventType {
			case keyEvent:
				kr := (*keyEventRecord)(unsafe.Pointer(&rec.event))
				// REF https://github.com/PowerShell/openssh-portable/blob/8fe096c7b7c7c51afd1d18654ec652187e85921b/contrib/win32/win32compat/tncon.c#L168-L178
				if !((kr.keyDown != 0 || kr.virtualKeyCode == vkMenu) &&
					(kr.unicodeChar != 0 || kr.virtualScanCode == 0)) {
					continue
				}

				r := rune(kr.unicodeChar)
				if utf16.IsSurrogate(fragment) {
					r = utf16.DecodeRune(fragment, r)
					fragment = 0
				}

				if utf16.IsSurrogate(r) {
					fragment = r
				} else {
					buf = utf8.AppendRune(buf, r)
					fragment = 0
				}

			case windowBufferSizeEvent:
				t.sigwinchCh <- nil

			default:
			}
		}

		t.fragment = fragment
	}

	n := min(len(p), len(buf))
	copy(p[:n], buf[:n])

	rem := buf[n:]
	if len(rem) > 0 {
		t.rem = rem
	} else {
		t.rem = nil
	}

	return n, nil
}

func (t *tty) write(p []byte) (int, error) {
	return os.Stdout.Write(p)
}

func (t *tty) size() (Winsize, error) {
	w, h, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		return Winsize{}, err
	}

	return Winsize{W: w, H: h}, nil
}
