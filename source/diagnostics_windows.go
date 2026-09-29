package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"
)

var diagnosticMu sync.Mutex

func writeDiagnostic(root, name, text string) {
	diagnosticMu.Lock()
	defer diagnosticMu.Unlock()
	if root == "" || root == "." {
		return
	}
	dir := filepath.Join(root, "Data", "Logs")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return
	}
	p := filepath.Join(dir, name)
	// Do not rename the runtime crash-output handle while it is open.
	if name != "launcher-crash.log" {
		rotateLog(p)
	}
	f, e := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\r\n", time.Now().Format(time.RFC3339), text)
	f.Sync()
}
func recordPanic(root, where string, v any) {
	writeDiagnostic(root, "launcher-crash.log", fmt.Sprintf("panic in %s: %v\n%s", where, v, debug.Stack()))
}
func setupCrashOutput(root string) {
	p := filepath.Join(root, "Data", "Logs", "launcher-crash.log")
	rotateLog(p)
	f, e := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		return
	}
	defer f.Close()
	// The runtime duplicates the file handle. Fatal runtime errors in other goroutines
	// can be recorded even when the GUI application has no attached console.
	if e = debug.SetCrashOutput(f, debug.CrashOptions{}); e != nil {
		writeDiagnostic(root, "launcher.log", "Cannot initialize runtime crash-output handle")
	}
}
