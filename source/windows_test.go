//go:build windows

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
	"unsafe"
)

func TestWindowsProxyParsing(t *testing.T) {
	cases := []struct{ raw, scheme, want string }{{"127.0.0.1:7890", "https", "http://127.0.0.1:7890"}, {"http=proxy:80;https=secure:8080", "https", "http://secure:8080"}, {"http=proxy:80", "https", ""}}
	for _, c := range cases {
		u, e := parseWindowsProxy(c.raw, c.scheme)
		if e != nil {
			t.Fatal(e)
		}
		actual := ""
		if u != nil {
			actual = u.String()
		}
		if actual != c.want {
			t.Fatal(actual, c.want)
		}
	}
	if !bypass("printer", "<local>") || !bypass("api.example.com", "*.example.com") {
		t.Fatal("bypass mismatch")
	}
}
func TestPortableWindowsEnvironment(t *testing.T) {
	root := t.TempDir()
	env, e := portableEnv(root, root, "nonce", "http://127.0.0.1:2000", "token")
	if e != nil {
		t.Fatal(e)
	}
	m := map[string]string{}
	for _, s := range env {
		k, v, _ := strings.Cut(s, "=")
		m[k] = v
	}
	if m["WORKBUDDY_CONFIG_DIR"] != filepath.Join(root, "Data", "WorkBuddy") {
		t.Fatal(m["WORKBUDDY_CONFIG_DIR"])
	}
	if m["HTTP_PROXY"] != "http://wbp:token@127.0.0.1:2000" {
		t.Fatal("missing proxy bridge")
	}
	if m["WBP_PORTABLE_VERSION"] != supportedVersion {
		t.Fatal("runtime version")
	}
}
func TestJobChildHelper(t *testing.T) {
	if os.Getenv("WBP_TEST_JOB_CHILD") != "1" {
		return
	}
	if e := os.WriteFile(os.Getenv("WBP_TEST_JOB_MARKER"), []byte("started"), 0600); e != nil {
		os.Exit(3)
	}
	time.Sleep(time.Minute)
	os.Exit(0)
}
func TestWindowsJobLifecycle(t *testing.T) {
	j, e := newJob()
	if e != nil {
		t.Fatal(e)
	}
	defer j.Close()
	root := t.TempDir()
	marker := filepath.Join(root, "started")
	env := append(os.Environ(), "WBP_TEST_JOB_CHILD=1", "WBP_TEST_JOB_MARKER="+marker)
	exe, _ := os.Executable()
	c, e := j.Start(exe, []string{"-test.run=^TestJobChildHelper$"}, root, env, filepath.Join(root, "child.log"))
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, e = os.Stat(marker); e == nil {
			break
		}
		if time.Now().After(deadline) {
			j.Kill()
			t.Fatal("child was not resumed")
		}
		time.Sleep(100 * time.Millisecond)
	}
	j.Kill()
	if e = j.WaitEmpty(10 * time.Second); e != nil {
		t.Fatal(e)
	}
	select {
	case e := <-done:
		if e == nil {
			t.Fatal("killed child returned success")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("job did not terminate child")
	}
}

func TestWindowsEnvironmentEncoding(t *testing.T) {
	cases := [][]string{nil, {"EMPTY="}, {"文字=中文𠀀😀", "X=路径 C:\\空 格\\ & # %TMP% !", "=C:=C:\\中文"}}
	for _, env := range cases {
		b, e := windowsEnvironmentBlock(env)
		if e != nil {
			t.Fatal(e)
		}
		if len(b) < 2 || b[len(b)-1] != 0 || b[len(b)-2] != 0 {
			t.Fatalf("not double NUL: %v", b)
		}
		text := string(utf16.Decode(b))
		for _, value := range env {
			if !strings.Contains(text, value+"\x00") {
				t.Fatal("environment changed", value)
			}
		}
	}
	for _, env := range [][]string{{"A=x\x00y"}, {"NO_EQUAL"}, {"=missing"}, {string([]byte{'A', '=', 0xff})}} {
		if _, e := windowsEnvironmentBlock(env); e == nil {
			t.Fatal("invalid environment accepted")
		}
	}
}
func TestWindowsCommandLineLimits(t *testing.T) {
	for _, arg := range []string{"bad\x00arg", strings.Repeat("中", 32768), string([]byte{0xff})} {
		if _, e := windowsCommandLine(`C:\test.exe`, []string{arg}); e == nil {
			t.Fatal("bad command accepted")
		}
	}
}
func TestUnicodeProcessHelper(t *testing.T) {
	if os.Getenv("WBP_UNICODE_HELPER") != "1" {
		return
	}
	if v := os.Getenv("WBP_UNICODE_EXIT"); v != "" {
		os.Exit(23)
	}
	cwd, _ := os.Getwd()
	after := []string{}
	for i, a := range os.Args {
		if a == "--" {
			after = os.Args[i+1:]
			break
		}
	}
	report := struct {
		Args           []string
		CWD, Env, Home string
	}{after, cwd, os.Getenv("WBP_UNICODE_VALUE"), os.Getenv("WORKBUDDY_CONFIG_DIR")}
	data, _ := json.Marshal(report)
	if e := os.WriteFile(os.Getenv("WBP_UNICODE_REPORT"), data, 0600); e != nil {
		os.Exit(4)
	}
	os.Stdout.WriteString("日志中文 UTF-8 𠀀😀\n")
	os.Exit(0)
}
func TestRealWindowsUnicodeLaunch(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "中文 空格 # & %TEMP% ! ' 𠀀😀")
	if e := os.MkdirAll(root, 0700); e != nil {
		t.Fatal(e)
	}
	self, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(self)
	if e != nil {
		t.Fatal(e)
	}
	exe := filepath.Join(root, "测试 启动器😀.exe")
	if e = os.WriteFile(exe, raw, 0700); e != nil {
		t.Fatal(e)
	}
	reportPath := filepath.Join(root, "子进程参数.json")
	value := "含空格 C:\\中文\\尾\\ 𠀀😀 %TEMP% & # ! \"quote\""
	args := []string{"中文", "", `空 格\`, `x"quote`, `C:\目录 名\`, "# & %TEMP% ! 𠀀😀"}
	env, e := portableEnv(root, root, "nonce", "http://127.0.0.1:1", "token")
	if e != nil {
		t.Fatal(e)
	}
	env = append(env, "WBP_UNICODE_HELPER=1", "WBP_UNICODE_REPORT="+reportPath, "WBP_UNICODE_VALUE="+value)
	j, e := newJob()
	if e != nil {
		t.Fatal(e)
	}
	defer j.Close()
	logPath := filepath.Join(root, "中文日志.log")
	c, e := j.Start(exe, append([]string{"-test.run=^TestUnicodeProcessHelper$", "--"}, args...), root, env, logPath)
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- c.Wait() }()
	select {
	case e = <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(20 * time.Second):
		j.Kill()
		t.Fatal("Unicode child timed out")
	}
	var r struct {
		Args           []string
		CWD, Env, Home string
	}
	b, e := os.ReadFile(reportPath)
	if e != nil {
		t.Fatal(e)
	}
	if e = json.Unmarshal(b, &r); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(r.Args, args) || r.CWD != root || r.Env != value || r.Home != filepath.Join(root, "Data", "WorkBuddy") {
		t.Fatalf("Unicode round-trip failed: %#v", r)
	}
	b, e = os.ReadFile(logPath)
	if e != nil || !bytes.Contains(b, []byte("日志中文 UTF-8 𠀀😀")) {
		t.Fatal("UTF-8 child log lost", e)
	}
	// A real nonzero child exit must remain an error, with the diagnostic hexadecimal code.
	c, e = j.Start(exe, []string{"-test.run=^TestUnicodeProcessHelper$"}, root, append(env, "WBP_UNICODE_EXIT=1"), logPath)
	if e != nil {
		t.Fatal(e)
	}
	if e = c.Wait(); e == nil || !strings.Contains(e.Error(), "0x00000017") {
		t.Fatal("lost child exit code", e)
	}
}
func TestWin32UnicodeTextUnderGC(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	h, _, e := pCreate.Call(0, uintptr(unsafe.Pointer(wstr("STATIC"))), uintptr(unsafe.Pointer(wstr("中文"))), 0, 0, 0, 100, 30, 0, 0, 0, 0)
	if h == 0 {
		t.Fatal(e)
	}
	defer pDestroy.Call(h)
	for i := 0; i < 60; i++ {
		runtime.GC()
		s := fmt.Sprintf("第 %d 次：中文、𠀀😀、# & %%", i)
		setText(h, s)
		if got := getText(h); got != s {
			t.Fatalf("text mismatch %q != %q", got, s)
		}
	}
}
