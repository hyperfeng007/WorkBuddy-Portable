package main

import (
	"bytes"
	"crypto/sha256"
	"debug/pe"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
)

const originalEXEHash = "6aa589e3edf1b91cd65d72f025def57689905505835c4c39481ed11f2b9e759c"
const originalHeaderHash = "07c5724186060f03529a7fdecf4195ac4b5c71dc4d6caa861f86dde019c52249"
const fuseSentinel = "dL7pKGdnNz796PbbjQWNKmHXBZaB9tsX"

func hashFile(p string) (string, error) {
	f, e := os.Open(p)
	if e != nil {
		return "", e
	}
	defer f.Close()
	h := sha256.New()
	if _, e = io.Copy(h, f); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
func asarHeaderHash(p string) (string, error) {
	f, e := os.Open(p)
	if e != nil {
		return "", e
	}
	defer f.Close()
	var h [16]byte
	if _, e = io.ReadFull(f, h[:]); e != nil {
		return "", e
	}
	n := binary.LittleEndian.Uint32(h[12:])
	if n == 0 || n > 32<<20 {
		return "", fmt.Errorf("bad ASAR header")
	}
	b := make([]byte, n)
	if _, e = io.ReadFull(f, b); e != nil {
		return "", e
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// Only operates on a fresh stage copied from the pinned, signed installer.
// Do NOT disable Electron's integrity/security fuses. Rebind both embedded ASAR
// integrity records, then remove the now-invalid signature from this changed EXE.
// The original installer is kept unchanged. This derivative is NOT Tencent-signed.
func adaptExecutable(exe, asar string) error {
	hash, e := hashFile(exe)
	if e != nil {
		return e
	}
	if hash != originalEXEHash {
		return fmt.Errorf("非已审核的原始 WorkBuddy.exe")
	}
	replacement, e := asarHeaderHash(asar)
	if e != nil {
		return e
	}
	if replacement == originalHeaderHash {
		return fmt.Errorf("ASAR 尚未适配")
	}
	f, e := os.OpenFile(exe, os.O_RDWR, 0)
	if e != nil {
		return e
	}
	defer f.Close()
	image, e := pe.NewFile(f)
	if e != nil {
		return e
	}
	defer image.Close()
	rsrc := image.Section(".rsrc")
	if rsrc == nil {
		return fmt.Errorf("PE resource section missing")
	}
	region, e := rsrc.Data()
	if e != nil {
		return e
	}
	old := []byte(originalHeaderHash)
	if bytes.Count(region, old) != 2 {
		return fmt.Errorf("ASAR integrity resource layout changed")
	}
	for _, alg := range []string{"SHA256", "sha256"} {
		anchor := []byte(`{"file":"resources\\app.asar","alg":"` + alg + `","value":"` + originalHeaderHash + `"}`)
		if bytes.Count(region, anchor) != 1 {
			return fmt.Errorf("ASAR integrity resource anchor changed")
		}
	}
	const fuseOffset int64 = 168881184 // This offset is bound to originalEXEHash above.
	fuse := make([]byte, len(fuseSentinel)+10)
	if _, e = f.ReadAt(fuse, fuseOffset); e != nil {
		return e
	}
	expected := append([]byte(fuseSentinel), []byte{1, 8, '1', '0', '1', '1', '1', '1', '0', '1'}...)
	if !bytes.Equal(fuse, expected) {
		return fmt.Errorf("Electron fuse layout changed")
	}
	var header [4096]byte
	if _, e = f.ReadAt(header[:], 0); e != nil {
		return e
	}
	optional := int(binary.LittleEndian.Uint32(header[0x3c:])) + 24
	if optional < 24 || optional+152 > len(header) || binary.LittleEndian.Uint16(header[optional:]) != 0x20b {
		return fmt.Errorf("not expected PE32+ x64")
	}
	security := optional + 112 + 4*8
	certOff := int64(binary.LittleEndian.Uint32(header[security:]))
	certSize := int64(binary.LittleEndian.Uint32(header[security+4:]))
	st, e := f.Stat()
	if e != nil {
		return e
	}
	if certOff < int64(rsrc.Offset)+int64(rsrc.Size) || certSize < 8 || certOff+certSize != st.Size() {
		return fmt.Errorf("unexpected certificate table; refusing to strip")
	}
	// All validation is complete. Only the freshly extracted stage is opened writable.
	// Stream/hash the large EXE; do not retain multiple 200-MB image copies in memory.
	region = bytes.ReplaceAll(region, old, []byte(replacement))
	if _, e = f.WriteAt(region, int64(rsrc.Offset)); e != nil {
		return e
	}
	if _, e = f.WriteAt(make([]byte, 8), int64(security)); e != nil {
		return e
	}
	if _, e = f.WriteAt(make([]byte, 4), int64(optional+64)); e != nil {
		return e
	}
	if e = f.Truncate(certOff); e != nil {
		return e
	}
	return f.Sync()
}

// Structure/size checks complement the pinned installer and ASAR/EXE hashes.
// This is not an independent signature for every third-party unpacked file.
func validateRuntimeLayout(dir string) error {
	required := map[string]int64{"chrome_100_percent.pak": 167282, "chrome_200_percent.pak": 258304, "icudtl.dat": 10467680, "resources.pak": 6037190, "snapshot_blob.bin": 321542, "v8_context_snapshot.bin": 699577, "libEGL.dll": 505344, "libGLESv2.dll": 8051200}
	check := func(name string, size int64) error {
		p, e := safeChild(dir, name)
		if e != nil {
			return e
		}
		st, e := os.Lstat(p)
		if e != nil || !st.Mode().IsRegular() || st.Size() != size {
			return fmt.Errorf("运行时文件缺失或大小异常: %s", name)
		}
		return nil
	}
	for name, size := range required {
		if e := check(name, size); e != nil {
			return e
		}
	}
	f, e := os.Open(filepath.Join(dir, "resources", "app.asar"))
	if e != nil {
		return e
	}
	defer f.Close()
	tree, _, e := readAsarHeader(f)
	if e != nil {
		return e
	}
	var walk func(*asarEntry, string) error
	walk = func(entry *asarEntry, name string) error {
		if entry.Link != "" {
			return fmt.Errorf("未预期的 ASAR 链接: %s", name)
		}
		if entry.Files != nil {
			for n, ch := range entry.Files {
				if e := walk(ch, path.Join(name, n)); e != nil {
					return e
				}
			}
			return nil
		}
		if entry.Unpacked {
			size := entry.Size
			if packaged, ok := packagedUnpackedSizes[name]; ok {
				size = packaged
			}
			return check("resources/app.asar.unpacked/"+name, size)
		}
		return nil
	}
	if e = walk(tree, ""); e != nil {
		return e
	}
	for _, p := range cliPatches() {
		entry := asarFile(tree, p.name)
		if entry == nil || entry.Integrity == nil || entry.Integrity.Algorithm != "SHA256" {
			return fmt.Errorf("CLI integrity metadata missing: %s", p.name)
		}
		sum, err := hashFile(filepath.Join(dir, "resources", "app.asar.unpacked", filepath.FromSlash(p.name)))
		if err != nil || sum != entry.Integrity.Hash {
			return fmt.Errorf("CLI integrity mismatch: %s", p.name)
		}
	}
	return nil
}
