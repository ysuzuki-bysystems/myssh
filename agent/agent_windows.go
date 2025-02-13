//go:build windows

package agent

import (
	"io"

	"github.com/Microsoft/go-winio"
)

func newAgentDialer(pathIfSpecified string) dialfn {
	p := `\\.\pipe\openssh-ssh-agent`
	if pathIfSpecified != "" {
		p = pathIfSpecified
	}

	return func() (io.ReadWriteCloser, error) {
		conn, err := winio.DialPipe(p, nil)
		if err != nil {
			return nil, err
		}

		return conn, nil
	}
}
