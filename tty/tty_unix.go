//go:build unix

package tty

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"golang.org/x/term"
)

type tty struct {
	cancel context.CancelFunc
	wg     *sync.WaitGroup
}

func openTty(sigwinchCh chan interface{}) (*tty, error) {
	wg := new(sync.WaitGroup)
	cx, cancel := context.WithCancel(context.Background())

	prev, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		cancel()
		return nil, err
	}
	wg.Add(1)
	context.AfterFunc(cx, func() {
		defer wg.Done()

		if err := term.Restore(int(os.Stdin.Fd()), prev); err != nil {
			log.Println(err)
		}
	})

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGWINCH)
	context.AfterFunc(cx, func() {
		signal.Stop(c)
		close(c)
	})

	wg.Add(1)
	go func() {
		defer wg.Done()

		for sig := range c {
			if sig != syscall.SIGWINCH {
				continue
			}

			select {
			case sigwinchCh <- nil:
			default:
			}
		}
	}()

	return &tty{
		cancel: cancel,
		wg:     wg,
	}, nil
}

func (t *tty) close() error {
	t.cancel()

	t.wg.Wait()
	return nil
}

func (t *tty) read(p []byte) (int, error) {
	return os.Stdin.Read(p)
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
