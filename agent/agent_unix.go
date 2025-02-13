//go:build unix

package agent

import (
	"errors"
	"io"
	"net"
)

func newAgentDialer(pathIfSpecified string) dialfn {
	if pathIfSpecified == "" {
		return func() (io.ReadWriteCloser, error) {
			return nil, errors.New("Could not connect agent socket")
		}
	}

	return func() (io.ReadWriteCloser, error) {
		conn, err := net.Dial("unix", pathIfSpecified)
		if err != nil {
			return nil, err
		}

		return conn, nil
	}
}
