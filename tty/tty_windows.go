//go:build windows

package tty

import (
	"context"
	"encoding/binary"
	"io"
	"log"
	"os"
	"sync"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/term"
	"golang.org/x/text/transform"
)

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

type inputRecordReader struct {
	buf 		[1024]inputRecord
	remaining 	[]inputRecord
	sigwinchCh 	chan interface{}
}

func (p *inputRecordReader) Read(b []byte) (int, error) {
	remaining := p.remaining
	if remaining == nil {
		// TODO WaitForSingleObjectEx ??
		// OpenSSH (Windows) https://github.com/PowerShell/openssh-portable/blob/8fe096c7b7c7c51afd1d18654ec652187e85921b/contrib/win32/win32compat/tncon.c#L95-L104
		// WezTerm too.

		// TODO Closed??
		nr, err := readConsoleInput(os.Stdin.Fd(), p.buf[:])
		if err != nil {
			return 0, err
		}

		remaining = p.buf[:nr]
	}

	var n int
	var pos int
	loop: for _, rec := range remaining {
		pos++

		switch rec.eventType {
		case keyEvent:
			kr := (*keyEventRecord)(unsafe.Pointer(&rec.event))
			// REF https://github.com/PowerShell/openssh-portable/blob/8fe096c7b7c7c51afd1d18654ec652187e85921b/contrib/win32/win32compat/tncon.c#L168-L178
			if !((kr.keyDown != 0 || kr.virtualKeyCode == vkMenu) &&
				(kr.unicodeChar != 0 || kr.virtualScanCode == 0)) {
				continue
			}

			if len(b) < 2 {
				pos-- // Unread
				break loop
			}

			binary.NativeEndian.PutUint16(b[n:], uint16(kr.unicodeChar))
			n += 2

		case windowBufferSizeEvent:
			if p.sigwinchCh == nil {
				continue
			}

			p.sigwinchCh <- nil

		default:
		}
	}

	remaining = remaining[pos:]
	if len(remaining) < 1 {
		p.remaining = nil
	} else {
		p.remaining = remaining
	}

	return n, nil
}

type w16ToUtf8Transformer struct {
	// Pending high surrogate. 0 is not set.
	high rune
}

func (t *w16ToUtf8Transformer) Reset() {
	t.high = 0
}

func (t *w16ToUtf8Transformer) Transform(dst, src []byte, atEOF bool) (int, int, error) {
	if len(src) == 0 && atEOF {
		return 0, 0, io.EOF
	}

	high := t.high
	var ndst, nsrc int

	for i := range len(src) / 2 {
		nsrc += 2

		v := binary.NativeEndian.Uint16(src[i*2:])
		c := rune(v)

		if high != 0 {
			c = utf16.DecodeRune(high, c)
		}

		if utf16.IsSurrogate(c) {
			high = c
			continue
		}

		if utf8.RuneLen(c) > len(dst) - ndst {
			nsrc -= 2 // Unread
			break
		}

		ndst += utf8.EncodeRune(dst[ndst:], c)
		high = 0
	}

	t.high = high
	return ndst, nsrc, nil
}

type tty struct {
	cancel     context.CancelFunc
	wg         *sync.WaitGroup
	stdin      io.Reader
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

	stdin := transform.NewReader(&inputRecordReader{ sigwinchCh: sigwinchCh }, &w16ToUtf8Transformer{})

	return &tty{
		cancel:     cancel,
		wg:         wg,
		stdin:      stdin,
	}, nil
}

func (t *tty) close() error {
	t.cancel()

	t.wg.Wait()
	return nil
}

func (t *tty) read(p []byte) (int, error) {
	return t.stdin.Read(p)
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
