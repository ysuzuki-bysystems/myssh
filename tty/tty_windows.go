//go:build windows && amd64

package tty

import (
	"context"
	"io"
	"log"
	"os"
	"sync"

	"golang.org/x/sys/windows"
	"golang.org/x/term"
	"golang.org/x/text/transform"
	"golang.org/x/text/encoding/unicode"
	syswin "github.com/ysuzuki-bysystems/myssh/tty/sys/windows"
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

	order := unicode.LittleEndian // amd64 only
	stdin := transform.NewReader(syswin.NewReader(sigwinchCh), unicode.UTF16(order, unicode.IgnoreBOM).NewDecoder())

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
