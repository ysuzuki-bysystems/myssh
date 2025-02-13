package x11

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"iter"
)

// REF https://gitlab.freedesktop.org/xorg/lib/libxau

type xauthorityEntry struct {
	family  uint16
	address []byte
	number  string
	name    string
	data    []byte
}

func readXauthorityEntry(r io.Reader, ent *xauthorityEntry) error {
	wrapErr := func(err error) error {
		if !errors.Is(err, io.EOF) {
			return err
		}
		return io.ErrUnexpectedEOF
	}

	readData := func() ([]byte, error) {
		var l uint16
		if err := binary.Read(r, binary.BigEndian, &l); err != nil {
			return nil, err
		}
		b := make([]byte, l)
		if _, err := io.ReadFull(r, b); err != nil {
			return nil, err
		}
		return b, nil
	}

	if err := binary.Read(r, binary.BigEndian, &ent.family); err != nil {
		return err
	}

	addr, err := readData()
	if err != nil {
		return wrapErr(err)
	}
	ent.address = addr

	number, err := readData()
	if err != nil {
		return wrapErr(err)
	}
	ent.number = string(number)

	name, err := readData()
	if err != nil {
		return wrapErr(err)
	}
	ent.name = string(name)

	data, err := readData()
	if err != nil {
		return wrapErr(err)
	}
	ent.data = data

	return nil
}

func parseXauthority(r io.Reader) iter.Seq2[*xauthorityEntry, error] {
	return func(yield func(*xauthorityEntry, error) bool) {
		buf := bufio.NewReader(r)

		for {
			var ent xauthorityEntry
			if err := readXauthorityEntry(buf, &ent); err != nil {
				if errors.Is(err, io.EOF) {
					return
				}

				yield(nil, err)
				return
			}

			if !yield(&ent, nil) {
				return
			}
		}
	}
}
