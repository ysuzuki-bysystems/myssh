package x11

import (
	"bytes"
	"encoding/hex"
	"net/netip"
	"os"
	"testing"
)

func TestParseXauthority(t *testing.T) {
	// xauth -f test-data/Xauthority add localhost/unix:1 MIT-MAGIC-COOKIE-1 11112222333344445555666677778888
	// xauth -f test-data/Xauthority add 192.0.2.1:2 MIT-MAGIC-COOKIE-1 22223333444455556666777788889999
	// xauth -f test-data/Xauthority add '[2001:db8::1]':1 MIT-MAGIC-COOKIE-1 3333444455556666777788889999aaaa
	//
	// ```
	// $ xauth -n -f test-data/Xauthority list
	// localhost/unix:1  MIT-MAGIC-COOKIE-1  11112222333344445555666677778888
	// 192.0.2.1:2  MIT-MAGIC-COOKIE-1  22223333444455556666777788889999
	// [2001:db8::1]:1  MIT-MAGIC-COOKIE-1  3333444455556666777788889999aaaa
	// ```
	fp, err := os.Open("./test-data/Xauthority")
	if err != nil {
		t.Fatal(err)
	}
	defer fp.Close()

	match := func(ent *xauthorityEntry, family uint16, addr []byte, number, name, data string) bool {
		if ent.family != family {
			return false
		}

		if bytes.Compare(ent.address, addr) != 0 {
			return false
		}

		if ent.number != number {
			return false
		}

		if ent.name != name {
			return false
		}

		datah, err := hex.DecodeString(data)
		if err != nil {
			panic(err)
		}
		if bytes.Compare(ent.data, datah) != 0 {
			return false
		}

		return true
	}

	addr := func(s string) []byte {
		a, err := netip.ParseAddr(s)
		if err != nil {
			panic(err)
		}
		return a.AsSlice()
	}

	for ent, err := range parseXauthority(fp) {
		if err != nil {
			t.Fatal(err)
		}

		m1 := match(ent, 0x0100, []byte("localhost"), "1", "MIT-MAGIC-COOKIE-1", "11112222333344445555666677778888")
		m2 := match(ent, 0x0000, addr("192.0.2.1"), "2", "MIT-MAGIC-COOKIE-1", "22223333444455556666777788889999")
		m3 := match(ent, 0x0006, addr("2001:db8::1"), "1", "MIT-MAGIC-COOKIE-1", "3333444455556666777788889999aaaa")

		if !m1 && !m2 && !m3 {
			t.Fatalf("%#v", ent)
		}
	}
}
