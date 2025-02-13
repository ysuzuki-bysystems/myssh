package main

import (
	"flag"
	"log"

	"github.com/ysuzuki-bysystems/myssh/agent"
	"github.com/ysuzuki-bysystems/myssh/tty"
	"github.com/ysuzuki-bysystems/myssh/x11"
	"golang.org/x/crypto/ssh"
)

func proc(cfg *config) error {
	ag := agent.NewAgent()

	client, err := dialSsh(cfg, ag)
	if err != nil {
		return err
	}
	defer client.Close()

	sess, err := client.NewSession()
	if err != nil {
		return err
	}
	defer sess.Close()

	if cfg.forwardX11 {
		x11.ForwardX11(client, sess, cfg.x11Display, cfg.xAuthLocation)
	}
	if cfg.forwardAgent {
		agent.ForwardAgent(client, sess, ag)
	}

	sigwinchCh := make(chan interface{})
	defer close(sigwinchCh)

	t, err := tty.OpenTty(sigwinchCh)
	if err != nil {
		return err
	}
	defer t.Close()

	go func() {
		for range sigwinchCh {
			m, err := t.Size()
			if err != nil {
				continue
			}

			sess.WindowChange(m.H, m.W)
		}
	}()

	termmodes := ssh.TerminalModes{
		ssh.ECHO:          1,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}

	size, err := t.Size()
	if err != nil {
		return err
	}

	if err := sess.RequestPty("xterm-256color", size.H, size.W, termmodes); err != nil {
		return err
	}

	sess.Stdin = t
	sess.Stdout = t
	sess.Stderr = sess.Stdout

	if err := sess.Shell(); err != nil {
		return err
	}

	if err := sess.Wait(); err != nil {
		return err
	}

	return nil
}

func main() {
	var cfgloc string
	var display string
	var forwardX11 bool
	var forwardAgent bool

	flag.StringVar(&cfgloc, "config", "", "ssh_config")
	flag.StringVar(&display, "display", "", "X11 DISPLAY")
	flag.BoolVar(&forwardX11, "X", false, "Forward X11")
	flag.BoolVar(&forwardAgent, "A", false, "Forward Agent")
	flag.Parse()

	host := flag.Arg(0)
	if host == "" {
		log.Fatal("No host")
	}

	cfg, err := loadConfig(host, cfgloc)
	if err != nil {
		log.Fatal(err)
	}

	if display != "" {
		cfg.x11Display = display
		cfg.forwardX11 = true
	}
	if forwardX11 {
		cfg.forwardX11 = true
	}
	if forwardAgent {
		cfg.forwardAgent = true
	}

	if err := proc(cfg); err != nil {
		log.Fatal(err)
	}
}
