package tty

import (
	"errors"
	"os"

	"golang.org/x/term"
)

type Winsize struct {
	H int
	W int
}

type Tty struct {
	tty *tty
}

var ErrNotATerminal = errors.New("Not a terminal.")

func OpenTty(sigwinchCh chan interface{}) (*Tty, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return nil, ErrNotATerminal
	}

	tty, err := openTty(sigwinchCh)
	if err != nil {
		return nil, err
	}

	return &Tty { tty: tty }, nil
}

func (t *Tty) Close() error {
	return t.tty.close()
}

func (t *Tty) Read(b []byte) (int, error) {
	return t.tty.read(b)
}

func (t *Tty) Write(b []byte) (int, error) {
	return t.tty.write(b)
}

func (t *Tty) Size() (Winsize, error) {
	return t.tty.size()
}
