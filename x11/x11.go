package x11

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"

	"golang.org/x/crypto/ssh"
)

type xdisplay struct {
	host   string
	number string
	screen string
}

func parseDisplay(displayname string) (*xdisplay, error) {
	// REF https://gitlab.freedesktop.org/xorg/app/xauth/-/blob/20125640fdc37732cb3c04627bd02011cff60a12/parsedpy.c#L94

	p := regexp.MustCompile(`^(?<host>.*)??:(?<num>\d+)(\.(?<screen>\d+))?$`)
	r := p.FindStringSubmatch(displayname)
	if r == nil {
		return nil, fmt.Errorf("Failed to parse DISPLAY: %s", displayname)
	}

	var host, num, screen string
	for i, n := range p.SubexpNames() {
		if i < 1 || n == "" {
			continue
		}

		switch n {
		case "host":
			host = r[i]
		case "num":
			num = r[i]
		case "screen":
			screen = r[i]
		default:
			panic(n)
		}
	}

	return &xdisplay{host, num, screen}, nil
}

func openDisplayConn(display string) (net.Conn, error) {
	dp, err := parseDisplay(display)
	if err != nil {
		return nil, err
	}

	if dp.host == "" {
		return net.Dial("unix", fmt.Sprintf("/tmp/.X11-unix/X%s", dp.number)) // Not tested.
	} else {
		num, err := strconv.Atoi(dp.number)
		if err != nil {
			panic("Must parse")
		}

		return net.Dial("tcp", fmt.Sprintf("%s:%d", dp.host, 6000+num))
	}
}

func closeConnWrite(w net.Conn) error {
	switch v := w.(type) {
	case *net.TCPConn:
		return v.CloseWrite()
	case *net.UnixConn:
		return v.CloseWrite()
	default:
		return errors.New("Unknown Type")
	}
}

func forwardX11Auth(r io.Reader, rcookie, pcookie []byte) ([]byte, error) {
	pad := func(e uint16) int {
		// pad(E) = (4 - (E mod 4)) mod 4
		return (4 - (int(e) % 4)) % 4
	}

	// https://www.x.org/releases/X11R7.7/doc/xproto/x11protocol.html#Encoding::Connection_Setup
	var b [12]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return nil, err
	}

	var ord binary.ByteOrder
	switch b[0] {
	case 0x42:
		ord = binary.BigEndian
	case 0x6c:
		ord = binary.LittleEndian
	default:
		return nil, fmt.Errorf("Incorrect byte order: %d", b[0])
	}
	_ = ord

	var authProtoNameLen, authProtoDataLen uint16
	if _, err := binary.Decode(b[6:8], ord, &authProtoNameLen); err != nil {
		return nil, err
	}

	if _, err := binary.Decode(b[8:10], ord, &authProtoDataLen); err != nil {
		return nil, err
	}

	b2 := make([]byte, int(authProtoNameLen)+pad(authProtoNameLen)+int(authProtoDataLen)+pad(authProtoDataLen))
	if _, err := io.ReadFull(r, b2); err != nil {
		return nil, err
	}
	authProtoName := b2[0:authProtoNameLen]
	authProtoData := b2[int(authProtoNameLen)+pad(authProtoNameLen) : int(authProtoNameLen)+pad(authProtoNameLen)+int(authProtoDataLen)]

	if string(authProtoName) != "MIT-MAGIC-COOKIE-1" {
		return nil, fmt.Errorf("Unsupported protocol: %s", string(authProtoName))
	}

	if bytes.Compare(authProtoData, pcookie) != 0 {
		return nil, errors.New("Cookie not match")
	}

	ret := make([]byte, 0, len(b)+int(authProtoNameLen)+pad(authProtoNameLen)+len(rcookie)+pad(uint16(len(rcookie))))
	w := bytes.NewBuffer(ret)
	// ~length of authorization-protocol-name
	if _, err := w.Write(b[:8]); err != nil {
		return nil, err
	}
	// length of authorization-protocol-data
	if err := binary.Write(w, ord, uint16(len(rcookie))); err != nil {
		return nil, err
	}
	// unused
	for range 2 {
		if err := w.WriteByte(0); err != nil {
			return nil, err
		}
	}
	// authorization-protocol-name, pad(n)
	if _, err := w.Write(b2[:int(authProtoNameLen)+pad(authProtoNameLen)]); err != nil {
		return nil, err
	}
	// authorization-protocol-data
	if _, err := w.Write(rcookie); err != nil {
		return nil, err
	}
	// pad(d)
	for range pad(uint16(len(rcookie))) {
		if err := w.WriteByte(0); err != nil {
			return nil, err
		}
	}

	return w.Bytes(), nil
}

func forwardX11Connection(ch ssh.Channel, display string, rcookie, pcookie []byte) error {
	defer ch.Close()

	ip, err := forwardX11Auth(ch, rcookie, pcookie)
	if err != nil {
		// TODO error response
		return err
	}

	conn, err := openDisplayConn(display)
	if err != nil {
		return err
	}
	defer conn.Close()

	if _, err := conn.Write(ip); err != nil {
		return err
	}

	errChan := make(chan error)
	go func() {
		defer closeConnWrite(conn)

		_, err := io.Copy(conn, ch)
		errChan <- err
	}()
	go func() {
		defer ch.Close()

		_, err := io.Copy(ch, conn)
		errChan <- err
	}()

	for range 2 {
		if err := <-errChan; err != nil {
			return err
		}
	}
	return nil
}

// REF https://gist.github.com/blacknon/9eca2e2b5462f71474e1101179847d2a
type x11request struct {
	SingleConnection bool
	AuthProtocol     string
	AuthCookie       string
	ScreenNumber     uint32
}

func queryCookie(display, xAuthLocation string) ([]byte, error) {
	cmd := exec.Command(xAuthLocation, "extract", "-", display)
	cmd.Stdin = nil
	cmd.Stderr = os.Stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	defer stdout.Close()

	if err := cmd.Start(); err != nil {
		return nil, err
	}
	defer cmd.Process.Kill()

	var cookie []byte
	for ent, err := range parseXauthority(stdout) {
		if err != nil {
			return nil, err
		}

		cookie = ent.data
	}

	if cookie == nil {
		return nil, errors.New("Cookie not found.")
	}

	return cookie, nil
}

func genPseudoCookie() ([]byte, error) {
	c := make([]byte, 16, 16)

	if _, err := io.ReadFull(rand.Reader, c); err != nil {
		return nil, err
	}

	return c, nil
}

func ForwardX11(client *ssh.Client, sess *ssh.Session, display, xAuthLocation string) error {
	if display == "" {
		return nil
	}

	rcookie, err := queryCookie(display, xAuthLocation)
	if err != nil {
		return err
	}
	pcookie, err := genPseudoCookie()
	if err != nil {
		return err
	}

	// X11 forwarding
	x11req := x11request{
		SingleConnection: false,
		AuthProtocol:     string("MIT-MAGIC-COOKIE-1"),
		AuthCookie:       string(hex.EncodeToString(pcookie)),
		ScreenNumber:     uint32(0),
	}
	ok, err := sess.SendRequest("x11-req", true, ssh.Marshal(x11req))
	if err != nil {
		return err
	}

	if !ok {
		return errors.New("Failed to x11-req")
	}

	go func() {
		x11chs := client.HandleChannelOpen("x11")

		for ch := range x11chs {
			channel, req, err := ch.Accept()
			if err != nil {
				continue
			}

			go ssh.DiscardRequests(req)
			go forwardX11Connection(channel, display, rcookie, pcookie)
		}
	}()

	return nil
}
