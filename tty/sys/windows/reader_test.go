package windows

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"

	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

func TestInputRecordReader(t *testing.T) {
	asBytes := func(event keyEventRecord) [16]byte {
		var b [16]byte
		binary.NativeEndian.PutUint32(b[0:], uint32(event.keyDown))
		binary.NativeEndian.PutUint16(b[4:], uint16(event.repeatCount))
		binary.NativeEndian.PutUint16(b[6:], uint16(event.virtualKeyCode))
		binary.NativeEndian.PutUint16(b[8:], uint16(event.virtualScanCode))
		binary.NativeEndian.PutUint16(b[10:], uint16(event.unicodeChar))
		binary.NativeEndian.PutUint32(b[12:], uint32(event.controlKeyState))
		return b
	}

	tests := []struct{
		name  string
		input []inputRecord
		wants []byte
	}{
		{
			name: "empty",
			input: []inputRecord{},
			wants: []byte{},
		},
		{
			name: "simple",
			input: []inputRecord{
				{
					eventType: keyEvent,
					event: asBytes(keyEventRecord{
						keyDown: 1,
						unicodeChar: wchar('a'),
					}),
				},
				{
					eventType: keyEvent,
					event: asBytes(keyEventRecord{
						keyDown: 0,
						unicodeChar: wchar('a'),
					}),
				},
				{
					eventType: keyEvent,
					event: asBytes(keyEventRecord{
						keyDown: 1,
						unicodeChar: wchar('b'),
					}),
				},
				{
					eventType: keyEvent,
					event: asBytes(keyEventRecord{
						keyDown: 0,
						unicodeChar: wchar('b'),
					}),
				},
				{
					eventType: keyEvent,
					event: asBytes(keyEventRecord{
						keyDown: 1,
						unicodeChar: wchar('c'),
					}),
				},
				{
					eventType: keyEvent,
					event: asBytes(keyEventRecord{
						keyDown: 0,
						unicodeChar: wchar('c'),
					}),
				},
			},
			wants: []byte("abc"),
		},
		{
			name: "shift-press",
			input: []inputRecord{
				{
					eventType: keyEvent,
					event: asBytes(keyEventRecord{
						keyDown: 1,
						unicodeChar: 0,
						virtualScanCode: 0x010,
					}),
				},
				{
					eventType: keyEvent,
					event: asBytes(keyEventRecord{
						keyDown: 0,
						unicodeChar: 0,
						virtualScanCode: 0x010,
					}),
				},
				{
					eventType: keyEvent,
					event: asBytes(keyEventRecord{
						keyDown: 1,
						unicodeChar: 0,
						virtualScanCode: 0x010,
					}),
				},
				{
					eventType: keyEvent,
					event: asBytes(keyEventRecord{
						keyDown: 0,
						unicodeChar: 0,
						virtualScanCode: 0x010,
					}),
				},
			},
			wants: []byte{},
		},
	}

	for _, test := range tests {
		var order unicode.Endianness
		if binary.NativeEndian.Uint16([]byte{0xAB,0xCD}) == 0xCDAB {
			order = unicode.LittleEndian
		} else {
			order = unicode.BigEndian
		}

		t.Run(test.name, func(t *testing.T) {
			input := test.input
			r := &inputRecordReader{
				readConsoleInput: func(h uintptr, buf []inputRecord) (int, error) {
					n := min(len(buf), len(input))
					if n == 0 {
						return 0, io.EOF
					}

					copy(buf[:n], input[:n])
					input = input[n:]
					return n, nil
				},
			}

			dst := bytes.NewBuffer([]byte{})
			if _, err := io.Copy(dst, r); err != nil {
				t.Fatal(err)
			}

			b, _, err := transform.Bytes(unicode.UTF16(order, unicode.IgnoreBOM).NewDecoder(), dst.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Compare(b, test.wants) != 0 {
				t.Fatal(b)
			}
		})
	}
}
