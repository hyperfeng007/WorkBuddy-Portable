package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/pe"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestWorkBuddyFeed(t *testing.T) {
	raw := `{"version":"` + supportedVersion + `","productVersion":"` + supportedVersion + `","url":"` + supportedURL + `","sha256hash":""}`
	f, e := parseFeed([]byte(raw))
	if e != nil || !f.Supported || f.SHA256 != supportedSHA256 || f.Size != supportedSize {
		t.Fatal(f, e)
	}
	for _, bad := range []string{strings.ReplaceAll(raw, "download.codebuddy.cn", "evil.invalid"), strings.ReplaceAll(raw, "https://", "http://"), strings.ReplaceAll(raw, "win32-x64-user", "win32-arm64-user"), strings.ReplaceAll(raw, `"sha256hash":""`, `"sha256hash":"bad"`), strings.Replace(raw, `"productVersion":"`+supportedVersion+`"`, `"productVersion":"9"`, 1)} {
		if _, e = parseFeed([]byte(bad)); e == nil {
			t.Fatal("accepted invalid feed", bad)
		}
	}
	newer := strings.ReplaceAll(raw, supportedVersion, "9.9.9.99999999")
	f, e = parseFeed([]byte(newer))
	if e != nil || f.Supported || f.SHA256 != "" {
		t.Fatal("unknown version must not acquire a trusted hash", f, e)
	}
}
func copyFixture(t *testing.T, src, dst string) {
	t.Helper()
	if e := os.MkdirAll(filepath.Dir(dst), 0700); e != nil {
		t.Fatal(e)
	}
	a, e := os.Open(src)
	if e != nil {
		t.Fatal(e)
	}
	defer a.Close()
	b, e := os.Create(dst)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = io.Copy(b, a); e != nil {
		t.Fatal(e)
	}
	if e = b.Close(); e != nil {
		t.Fatal(e)
	}
}
func sourceFromAsar(t *testing.T, file, name string) []byte {
	t.Helper()
	f, e := os.Open(file)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	tree, base, e := readAsarHeader(f)
	if e != nil {
		t.Fatal(e)
	}
	b, e := readAsarFile(f, tree, base, name)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func TestOfficialWorkBuddyAdaptation(t *testing.T) {
	app := os.Getenv("WBP_TEST_APP")
	if app == "" {
		t.Skip("WBP_TEST_APP points to the original extracted 5.6.2 app")
	}
	if e := validateRuntimeLayout(app); e != nil {
		t.Fatal(e)
	}
	root := t.TempDir()
	temp := filepath.Join(root, "Runtime", "fixture")
	if e := filepath.WalkDir(app, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(app, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(temp, rel), 0700)
		}
		if d.Type()&os.ModeSymlink != 0 {
			return os.ErrInvalid
		}
		copyFixture(t, p, filepath.Join(temp, rel))
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	stock := filepath.Join(app, "resources", "app.asar")
	asar := filepath.Join(temp, "resources", "app.asar")
	exe := filepath.Join(temp, "WorkBuddy.exe")
	header, e := asarHeaderHash(asar)
	if e != nil || header != originalHeaderHash {
		t.Fatal(header, e)
	}
	if e = patchAsar(asar); e != nil {
		t.Fatal(e)
	}
	if e = adaptExecutable(exe, asar); e != nil {
		t.Fatal(e)
	}
	exeHash, _ := hashFile(exe)
	asarHash, _ := hashFile(asar)
	record := RuntimeRecord{EXEHash: exeHash, ASARHash: asarHash, Version: supportedVersion, Directory: "fixture", SHA256: supportedSHA256, Wrapper: wrapperVersion}
	pointer := filepath.Join(root, "Runtime", "current.json")
	recordBytes, _ := json.Marshal(record)
	os.WriteFile(pointer, recordBytes, 0600)
	if got, err := currentRuntime(root); err != nil || got != record {
		t.Fatal("same-version local reuse rejected", got, err)
	}
	record.Wrapper = "future-wrapper"
	recordBytes, _ = json.Marshal(record)
	os.WriteFile(pointer, recordBytes, 0600)
	if _, err := currentRuntime(root); err == nil {
		t.Fatal("wrapper adaptation mismatch not detected")
	}
	record.Wrapper = wrapperVersion
	record.Directory = "../escape"
	recordBytes, _ = json.Marshal(record)
	os.WriteFile(pointer, recordBytes, 0600)
	if _, err := currentRuntime(root); err == nil {
		t.Fatal("unsafe local runtime path accepted")
	}
	var pkg map[string]any
	if e = json.Unmarshal(sourceFromAsar(t, asar, "package.json"), &pkg); e != nil {
		t.Fatal(e)
	}
	if pkg["main"] != "portable.mjs" || pkg["name"] != "@genie/workbuddy-desktop" {
		t.Fatal(pkg)
	}
	for _, name := range []string{"main/credential-protection-bootstrap.js", "main/code-cache.js", "main/desktop-monitor-service.js", "main/log-acl-guard.js"} {
		if !bytes.Equal(sourceFromAsar(t, stock, name), sourceFromAsar(t, asar, name)) {
			t.Fatal("unrelated security/path module changed", name)
		}
	}
	main := string(sourceFromAsar(t, asar, "main/index.js"))
	original := string(sourceFromAsar(t, stock, "main/index.js"))
	a := strings.Index(original, "\tasync checkForceUpgrade() {")
	b := strings.Index(original[a:], "\n\t/**") + a
	if a < 0 || b <= a || !strings.Contains(main, original[a:b]) {
		t.Fatal("mandatory upgrade policy changed")
	}
	stockFile, e := os.Open(filepath.Join(app, "WorkBuddy.exe"))
	if e != nil {
		t.Fatal(e)
	}
	defer stockFile.Close()
	changedFile, e := os.Open(exe)
	if e != nil {
		t.Fatal(e)
	}
	defer changedFile.Close()
	originalPE, e := pe.NewFile(stockFile)
	if e != nil {
		t.Fatal(e)
	}
	defer originalPE.Close()
	rsrc := originalPE.Section(".rsrc")
	resource, e := rsrc.Data()
	if e != nil {
		t.Fatal(e)
	}
	changedResource := make([]byte, len(resource))
	if _, e = changedFile.ReadAt(changedResource, int64(rsrc.Offset)); e != nil {
		t.Fatal(e)
	}
	newHeader, e := asarHeaderHash(asar)
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Count(changedResource, []byte(newHeader)) != 2 || bytes.Contains(changedResource, []byte(originalHeaderHash)) {
		t.Fatal("PE integrity mismatch")
	}
	var oldHead, newHead [4096]byte
	stockFile.ReadAt(oldHead[:], 0)
	changedFile.ReadAt(newHead[:], 0)
	optional := int(binary.LittleEndian.Uint32(oldHead[0x3c:])) + 24
	security := optional + 112 + 4*8
	certOff := int64(binary.LittleEndian.Uint32(oldHead[security:]))
	changedStat, _ := changedFile.Stat()
	if changedStat.Size() != certOff || binary.LittleEndian.Uint64(newHead[security:]) != 0 {
		t.Fatal("invalid signature was not removed")
	}
	ranges := [][2]int64{{int64(optional + 64), int64(optional + 68)}, {int64(security), int64(security + 8)}}
	for begin := 0; begin < len(resource); {
		v := bytes.Index(resource[begin:], []byte(originalHeaderHash))
		if v < 0 {
			break
		}
		pos := int64(rsrc.Offset) + int64(begin+v)
		ranges = append(ranges, [2]int64{pos, pos + 64})
		begin += v + 64
	}
	sort.Slice(ranges, func(i, j int) bool { return ranges[i][0] < ranges[j][0] })
	ranges = append(ranges, [2]int64{certOff, certOff})
	cursor := int64(0)
	for _, r := range ranges {
		h1, h2 := sha256.New(), sha256.New()
		if _, e = io.Copy(h1, io.NewSectionReader(stockFile, cursor, r[0]-cursor)); e != nil {
			t.Fatal(e)
		}
		if _, e = io.Copy(h2, io.NewSectionReader(changedFile, cursor, r[0]-cursor)); e != nil {
			t.Fatal(e)
		}
		if !bytes.Equal(h1.Sum(nil), h2.Sum(nil)) {
			t.Fatalf("unexpected EXE change between %x and %x", cursor, r[0])
		}
		cursor = r[1]
	}
	node := os.Getenv("WBP_TEST_NODE")
	out := os.Getenv("WBP_TEST_OUTPUT")
	if out == "" {
		out = filepath.Join(temp, "modules")
	}
	for _, name := range []string{"portable.mjs", "portable-node.cjs", "portable-network.mjs", "portable-update.mjs", "main/index.js", "main/server.js", "main/proxy-agents.js", "main/daemon-app-server-entry.js"} {
		p := filepath.Join(out, filepath.FromSlash(name))
		os.MkdirAll(filepath.Dir(p), 0700)
		if e = os.WriteFile(p, sourceFromAsar(t, asar, name), 0600); e != nil {
			t.Fatal(e)
		}
		if node != "" {
			if b, e := exec.Command(node, "--check", p).CombinedOutput(); e != nil {
				t.Fatalf("JS syntax %s: %s %v", name, b, e)
			}
		}
	}
	for _, cli := range cliPatches() {
		name := filepath.Join(out, filepath.FromSlash(cli.name))
		copyFixture(t, filepath.Join(asar+".unpacked", filepath.FromSlash(cli.name)), name)
		if node != "" {
			if b, e := exec.Command(node, "--check", name).CombinedOutput(); e != nil {
				t.Fatalf("CLI syntax %s: %s %v", cli.name, b, e)
			}
		}
	}
	cliFile := filepath.Join(asar+".unpacked", filepath.FromSlash(cliPatches()[0].name))
	cf, err := os.OpenFile(cliFile, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	first := make([]byte, 1)
	cf.ReadAt(first, 0)
	cf.WriteAt([]byte{first[0] ^ 1}, 0)
	if err = validateRuntimeLayout(temp); err == nil {
		t.Fatal("same-size CLI corruption not detected")
	}
	cf.WriteAt(first, 0)
	cf.Close()
	before, _ := hashFile(asar)
	if patchAsar(asar) == nil {
		t.Fatal("accepted a second patch")
	}
	after, _ := hashFile(asar)
	if before != after {
		t.Fatal("failed patch mutated input")
	}
	if adaptExecutable(exe, asar) == nil {
		t.Fatal("accepted an already modified executable")
	}
	if installer := os.Getenv("WBP_TEST_INSTALLER"); installer != "" && !verifiedArchive(installer) {
		t.Fatal("original installer cache not reusable")
	}
	t.Log("complete runtime layout validated, same-version local record reused, mismatch/traversal rejected, original installer cache verified; real ASAR patched, 10 JS modules syntax checked (including both real CLI bundles), credential/force-upgrade modules preserved, native fuses unchanged, unsigned EXE integrity metadata rebound")
}
func TestPatchAnchorsFailClosed(t *testing.T) {
	if _, e := replaceOnce("x x", "x", "z"); e == nil {
		t.Fatal("ambiguous anchor accepted")
	}
	if _, e := replaceOnce("x", "y", "z"); e == nil {
		t.Fatal("missing anchor accepted")
	}
}

func TestReviewedCacheRepairDoesNotFetchFeed(t *testing.T) {
	installer := os.Getenv("WBP_TEST_INSTALLER")
	if installer == "" {
		t.Skip("set WBP_TEST_INSTALLER")
	}
	called := false
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		called = true
		return nil, fmt.Errorf("network intentionally unavailable")
	})}
	f, e := feedForPreparation(context.Background(), client, installer, false)
	if e != nil || called || !f.Supported || f.SHA256 != supportedSHA256 {
		t.Fatal("cache-only repair did not reuse original", e, called)
	}
	if _, e = feedForPreparation(context.Background(), client, installer, true); e == nil || !called {
		t.Fatal("explicit update must check feed")
	}
}
