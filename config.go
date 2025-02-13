package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"iter"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/kevinburke/ssh_config"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

func defaultUserConfigLocation(user *user.User) string {
	return filepath.Join(user.HomeDir, ".ssh", "config")
}

func defaultSystemConfigLocation() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.Getenv("ProgramData"), "ssh", "ssh_config")
	}

	return "/etc/ssh/ssh_config"
}

func defaultUserKnownHostsFile(user *user.User) string {
	return filepath.Join(user.HomeDir, ".ssh", "known_hosts")
}

func defaultGlobalKnownHostsFile() string {
	if runtime.GOOS == "windows" {
		// https://github.com/PowerShell/openssh-portable/blob/8fe096c7b7c7c51afd1d18654ec652187e85921b/contrib/win32/openssh/sshd_config#L41
		return filepath.Join(os.Getenv("ProgramData"), "ssh", "ssh_known_hosts")
	}

	return "/etc/ssh/ssh_known_hosts"
}

func loadSshConfig(path string) (*ssh_config.Config, error) {
	fp, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fp.Close()

	cfg, err := ssh_config.Decode(fp)
	if err != nil {
		return nil, err
	}

	return cfg, nil
}

type config struct {
	user             string
	hostname         string
	port             string
	userKnownHosts   string
	globalKnownHosts string
	forwardX11       bool
	forwardAgent     bool
	xAuthLocation    string

	x11Display string
}

func loadConfig(host, cfg string) (*config, error) {
	user, err := user.Current()
	if err != nil {
		return nil, err
	}

	if cfg == "" {
		cfg = defaultUserConfigLocation(user)
	}

	userConfig, _ := loadSshConfig(cfg)
	systemConfig, _ := loadSshConfig(defaultSystemConfigLocation())

	get := func(name string, fallback string) string {
		var val string

		if userConfig != nil {
			val, _ = userConfig.Get(host, name)
		}
		if val == "" && systemConfig != nil {
			val, _ = systemConfig.Get(host, name)
		}
		if val == "" {
			val = fallback
		}

		return val
	}

	return &config{
		user:             get("User", user.Username),
		hostname:         get("Hostname", host),
		port:             get("Port", "22"),
		userKnownHosts:   get("UserKnownHostsFile", defaultUserKnownHostsFile(user)),
		globalKnownHosts: get("GlobalKnownHostsFile", defaultGlobalKnownHostsFile()),
		forwardX11:       get("ForwardX11", "no") == "yes",
		forwardAgent:     get("ForwardAgent", "no") == "yes",
		xAuthLocation:    get("XAuthLocation", "xauth"),

		x11Display: os.Getenv("DISPLAY"),
	}, nil
}

type knownHostsEntry struct {
	hosts  []string
	pubKey ssh.PublicKey
}

func iterKnownHosts(r io.Reader) iter.Seq2[*knownHostsEntry, error] {
	return func(yield func(*knownHostsEntry, error) bool) {
		buf, err := io.ReadAll(r)
		if err != nil {
			yield(nil, err)
			return
		}

		for len(buf) > 0 {
			var hosts []string
			var pubKey ssh.PublicKey

			_, hosts, pubKey, _, buf, err = ssh.ParseKnownHosts(buf)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				if !yield(nil, err) {
					return
				}
			}

			ent := knownHostsEntry{hosts, pubKey}
			if !yield(&ent, nil) {
				return
			}
		}
	}
}

func knownHostsHostKey(knownHosts, defaultPort string) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		if strings.HasSuffix(hostname, fmt.Sprintf(":%s", defaultPort)) {
			hostname = hostname[:len(hostname)-(1+len(defaultPort))]
		}

		fp, err := os.Open(knownHosts)
		if err != nil {
			return err
		}
		defer fp.Close()

		for ent, err := range iterKnownHosts(fp) {
			if err != nil {
				return err
			}

			if !slices.Contains(ent.hosts, hostname) {
				continue
			}

			if key.Type() != ent.pubKey.Type() {
				continue
			}

			if bytes.Equal(key.Marshal(), ent.pubKey.Marshal()) {
				return nil
			}
		}

		return fmt.Errorf("NO MATCH ENTRIES FOUND: %s", hostname)
	}
}

func combinedHostKey(fns ...ssh.HostKeyCallback) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		result := errors.New("Not checked.")
		for _, fn := range fns {
			err := fn(hostname, remote, key)
			if err == nil {
				return nil
			}
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			result = err
		}
		return result
	}
}

func dialSsh(cfg *config, agent agent.Agent) (*ssh.Client, error) {
	hostkeycallbacks := make([]ssh.HostKeyCallback, 0)
	if cfg.userKnownHosts != "" {
		// TODO split " "
		hostkeycallbacks = append(hostkeycallbacks, knownHostsHostKey(cfg.userKnownHosts, "22"))
	}
	if cfg.globalKnownHosts != "" {
		// TODO split " "
		hostkeycallbacks = append(hostkeycallbacks, knownHostsHostKey(cfg.globalKnownHosts, "22"))
	}
	hostKeyCallback := combinedHostKey(hostkeycallbacks...)

	sshcfg := &ssh.ClientConfig{
		User: cfg.user,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeysCallback(agent.Signers),
		},
		HostKeyCallback: hostKeyCallback,
	}
	return ssh.Dial("tcp", fmt.Sprintf("%s:%s", cfg.hostname, cfg.port), sshcfg)
}
