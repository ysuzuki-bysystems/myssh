package agent

import (
	"io"
	"os"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

type lazySigner struct {
	agent agent.Agent
	pub   ssh.PublicKey
}

func (s *lazySigner) PublicKey() ssh.PublicKey {
	return s.pub
}

func (s *lazySigner) Sign(rand io.Reader, data []byte) (*ssh.Signature, error) {
	return s.agent.Sign(s.pub, data)
}

type dialfn func() (io.ReadWriteCloser, error)

// FIXME Windows の Named Pipe (を開いている ssh-agent の実装??) が 1分 アイドルすると閉じるので、都度接続している...
type lazyAgent struct {
	dial dialfn
}

func (a *lazyAgent) newClient() (agent.ExtendedAgent, io.ReadWriteCloser, error) {
	conn, err := a.dial()
	if err != nil {
		return nil, nil, err
	}

	client := agent.NewClient(conn)
	return client, conn, nil
}

func (a *lazyAgent) List() ([]*agent.Key, error) {
	client, conn, err := a.newClient()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	return client.List()
}

func (a *lazyAgent) Sign(key ssh.PublicKey, data []byte) (*ssh.Signature, error) {
	client, conn, err := a.newClient()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	return client.Sign(key, data)
}

func (a *lazyAgent) Add(key agent.AddedKey) error {
	client, conn, err := a.newClient()
	if err != nil {
		return err
	}
	defer conn.Close()

	return client.Add(key)
}

func (a *lazyAgent) Remove(key ssh.PublicKey) error {
	client, conn, err := a.newClient()
	if err != nil {
		return err
	}
	defer conn.Close()

	return client.Remove(key)
}

func (a *lazyAgent) RemoveAll() error {
	client, conn, err := a.newClient()
	if err != nil {
		return err
	}
	defer conn.Close()

	return client.RemoveAll()
}

func (a *lazyAgent) Lock(passphrase []byte) error {
	client, conn, err := a.newClient()
	if err != nil {
		return err
	}
	defer conn.Close()

	return client.Lock(passphrase)
}

func (a *lazyAgent) Unlock(passphrase []byte) error {
	client, conn, err := a.newClient()
	if err != nil {
		return err
	}
	defer conn.Close()

	return client.Unlock(passphrase)
}

func (a *lazyAgent) Signers() ([]ssh.Signer, error) {
	client, conn, err := a.newClient()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	signers, err := client.Signers()
	if err != nil {
		return nil, err
	}

	ret := make([]ssh.Signer, 0, len(signers))
	for _, signer := range signers {
		ret = append(ret, &lazySigner{agent: a, pub: signer.PublicKey()})
	}
	return ret, nil
}

func (a *lazyAgent) SignWithFlags(key ssh.PublicKey, data []byte, flags agent.SignatureFlags) (*ssh.Signature, error) {
	client, conn, err := a.newClient()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	return client.SignWithFlags(key, data, flags)
}

func (a *lazyAgent) Extension(extensionType string, contents []byte) ([]byte, error) {
	client, conn, err := a.newClient()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	return client.Extension(extensionType, contents)
}

func ForwardAgent(client *ssh.Client, sess *ssh.Session, ag agent.ExtendedAgent) error {
	if err := agent.RequestAgentForwarding(sess); err != nil {
		return err
	}

	if err := agent.ForwardToAgent(client, ag); err != nil {
		return err
	}

	return nil
}

func NewAgent() agent.ExtendedAgent {
	p := os.Getenv("SSH_AUTH_SOCK")
	dial := newAgentDialer(p)
	return &lazyAgent{ dial: dial }
}
