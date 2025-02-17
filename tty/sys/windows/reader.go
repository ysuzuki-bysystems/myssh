package windows

import (
	"encoding/binary"
	"errors"
	"io"
	"os"
	"unsafe"
)

const (
	keyEvent              = 0x1
	windowBufferSizeEvent = 0x4

	vkMenu = 0x12
)

type wchar uint16
type dword uint32
type word uint16

type inputRecord struct {
	eventType word
	_         [2]byte
	event     [16]byte
}

type keyEventRecord struct {
	keyDown         int32
	repeatCount     word
	virtualKeyCode  word
	virtualScanCode word
	unicodeChar     wchar
	controlKeyState dword
}

type inputRecordReader struct {
	buf 			 [1024]inputRecord
	remaining 		 []inputRecord
	sigwinchCh 		 chan interface{}

	readConsoleInput func(h uintptr, buf []inputRecord) (int, error)
}

func (p *inputRecordReader) Read(b []byte) (int, error) {
	remaining := p.remaining
	if remaining == nil {
		// TODO WaitForSingleObjectEx ??
		// OpenSSH (Windows) https://github.com/PowerShell/openssh-portable/blob/8fe096c7b7c7c51afd1d18654ec652187e85921b/contrib/win32/win32compat/tncon.c#L95-L104
		// WezTerm too.

		nr, err := p.readConsoleInput(os.Stdin.Fd(), p.buf[:])
		if err != nil {
			if errors.Is(err, io.EOF) {
				// TODO Closed??
				return 0, io.EOF
			}
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

			if len(b[n:]) < 2 {
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

