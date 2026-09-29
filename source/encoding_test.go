package main

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

func encodedUTF16(s string, order binary.ByteOrder) []byte {
	b := new(bytes.Buffer)
	if order == binary.LittleEndian {
		b.Write([]byte{0xff, 0xfe})
	} else {
		b.Write([]byte{0xfe, 0xff})
	}
	binary.Write(b, order, utf16.Encode([]rune(s)))
	return b.Bytes()
}
func TestConfigurationEncodings(t *testing.T) {
	c := Config{ProxyMode: "manual", ProxyURL: "http://user:pass@localhost:7890", NoProxy: "中文.example,*.测试,𠀀😀"}
	b, _ := json.Marshal(c)
	cases := map[string][]byte{"UTF8": b, "UTF8-BOM": append([]byte{0xef, 0xbb, 0xbf}, b...), "UTF16-LE-BOM": encodedUTF16(string(b), binary.LittleEndian), "UTF16-BE-BOM": encodedUTF16(string(b), binary.BigEndian)}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if e := os.WriteFile(filepath.Join(dir, "portable.json"), data, 0600); e != nil {
				t.Fatal(e)
			}
			got, e := readConfig(dir)
			if e != nil {
				t.Fatal(e)
			}
			if got != c {
				t.Fatalf("round-trip mismatch: %#v", got)
			}
		})
	}
}
func TestMalformedConfigurationEncodingRejected(t *testing.T) {
	for _, b := range [][]byte{{0xff}, {0xff, 0xfe, 0x41}, {0xff, 0xfe, 0, 0xd8}, {0xfe, 0xff, 0xdc, 0}, {0xd6, 0xd0, 0xce, 0xc4}, {0xff, 0xfe, 0, 0, 0x41, 0, 0, 0}} {
		if _, e := decodeConfigText(b); e == nil {
			t.Fatalf("accepted malformed encoding %x", b)
		}
	}
}
func TestWindowsLengthCountsUTF16NotUTF8(t *testing.T) {
	if utf16Units(strings.Repeat("中", 60)) != 60 {
		t.Fatal("CJK path measured in bytes")
	}
	if utf16Units("a😀𠀀") != 5 {
		t.Fatal("non-BMP UTF16 units not counted")
	}
}
