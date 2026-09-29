package main

import (
	"crypto/sha256"
	"embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

//go:embed assets/*.mjs assets/*.cjs
var assets embed.FS

type asarPayload struct {
	name string
	data []byte
}

type asarEntry struct {
	Files      map[string]*asarEntry `json:"files,omitempty"`
	Size       int64                 `json:"size,omitempty"`
	Offset     string                `json:"offset,omitempty"`
	Unpacked   bool                  `json:"unpacked,omitempty"`
	Executable bool                  `json:"executable,omitempty"`
	Link       string                `json:"link,omitempty"`
	Integrity  *asarIntegrity        `json:"integrity,omitempty"`
}

// Preserve explicit zero-sized files and empty directories in Chromium ASAR headers.
func (e asarEntry) MarshalJSON() ([]byte, error) {
	m := map[string]any{}
	if e.Files != nil {
		m["files"] = e.Files
	} else if e.Link != "" {
		m["link"] = e.Link
	} else {
		m["size"] = e.Size
		if !e.Unpacked {
			m["offset"] = e.Offset
		}
	}
	if e.Unpacked {
		m["unpacked"] = true
	}
	if e.Executable {
		m["executable"] = true
	}
	if e.Integrity != nil {
		m["integrity"] = e.Integrity
	}
	return json.Marshal(m)
}

type asarIntegrity struct {
	Algorithm string   `json:"algorithm"`
	Hash      string   `json:"hash"`
	BlockSize int      `json:"blockSize"`
	Blocks    []string `json:"blocks"`
}

func checksumEntry(data []byte) *asarIntegrity {
	h := sha256.Sum256(data)
	a := &asarIntegrity{Algorithm: "SHA256", Hash: hex.EncodeToString(h[:]), BlockSize: 4194304, Blocks: []string{}}
	for i := 0; i < len(data); i += a.BlockSize {
		end := i + a.BlockSize
		if end > len(data) {
			end = len(data)
		}
		x := sha256.Sum256(data[i:end])
		a.Blocks = append(a.Blocks, hex.EncodeToString(x[:]))
	}
	return a
}
func readAsarHeader(f *os.File) (*asarEntry, int64, error) {
	var b [16]byte
	if _, e := io.ReadFull(f, b[:]); e != nil {
		return nil, 0, e
	}
	hs := binary.LittleEndian.Uint32(b[4:8])
	js := binary.LittleEndian.Uint32(b[12:16])
	if binary.LittleEndian.Uint32(b[:4]) != 4 || js > 32<<20 || hs < 8 || js > hs-8 {
		return nil, 0, fmt.Errorf("不支持的 ASAR 格式")
	}
	raw := make([]byte, js)
	if _, e := io.ReadFull(f, raw); e != nil {
		return nil, 0, e
	}
	var tree asarEntry
	if e := json.Unmarshal(raw, &tree); e != nil {
		return nil, 0, e
	}
	return &tree, int64(hs) + 8, nil
}
func asarFile(t *asarEntry, name string) *asarEntry {
	for _, s := range strings.Split(name, "/") {
		if t == nil {
			return nil
		}
		t = t.Files[s]
	}
	return t
}
func readAsarFile(f *os.File, t *asarEntry, base int64, name string) ([]byte, error) {
	e := asarFile(t, name)
	if e == nil || e.Unpacked || e.Size < 0 || e.Size > 16<<20 {
		return nil, fmt.Errorf("缺少或异常的 ASAR 文件: %s", name)
	}
	off, err := strconv.ParseInt(e.Offset, 10, 64)
	if err != nil || off < 0 {
		return nil, fmt.Errorf("ASAR 偏移无效")
	}
	b := make([]byte, e.Size)
	_, err = f.ReadAt(b, base+off)
	return b, err
}
func writeAsarPayloads(file string, f *os.File, tree *asarEntry, base int64, payloads []asarPayload) error {
	stat, e := f.Stat()
	if e != nil {
		return e
	}
	bodyLen := stat.Size() - base
	if bodyLen < 0 {
		return fmt.Errorf("ASAR 长度无效")
	}
	offset := bodyLen
	for _, x := range payloads {
		parent := tree
		parts := strings.Split(x.name, "/")
		for _, part := range parts[:len(parts)-1] {
			next := parent.Files[part]
			if next == nil || next.Files == nil {
				return fmt.Errorf("ASAR payload parent missing")
			}
			parent = next
		}
		parent.Files[parts[len(parts)-1]] = &asarEntry{Size: int64(len(x.data)), Offset: strconv.FormatInt(offset, 10), Integrity: checksumEntry(x.data)}
		offset += int64(len(x.data))
	}
	head, e := json.Marshal(tree)
	if e != nil {
		return e
	}
	padded := (len(head) + 3) &^ 3
	hsize := 8 + padded
	dest := file + ".portable-new"
	out, e := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	done := false
	defer func() {
		out.Close()
		if !done {
			os.Remove(dest)
		}
	}()
	ints := []uint32{4, uint32(hsize), uint32(4 + padded), uint32(len(head))}
	if e = binary.Write(out, binary.LittleEndian, ints); e != nil {
		return e
	}
	if _, e = out.Write(head); e != nil {
		return e
	}
	if _, e = out.Write(make([]byte, padded-len(head))); e != nil {
		return e
	}
	if _, e = f.Seek(base, io.SeekStart); e != nil {
		return e
	}
	if _, e = io.Copy(out, f); e != nil {
		return e
	}
	for _, x := range payloads {
		if _, e = out.Write(x.data); e != nil {
			return e
		}
	}
	if e = out.Sync(); e != nil {
		return e
	}
	if e = out.Close(); e != nil {
		return e
	}
	f.Close()
	if e = os.Remove(file); e != nil {
		return e
	}
	if e = os.Rename(dest, file); e != nil {
		return e
	}
	done = true
	return nil
}
