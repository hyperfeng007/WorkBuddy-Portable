package main

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// Native Windows tests: compile on Linux, but MUST NOT report them as executed
// unless actually run on Windows. No official runtime/account/download needed.
func TestWindowsProgressReallyDestroyed(t *testing.T) {
	settingsOnly = false
	busy.Store(false)
	ready.Store(false)
	stopCalls := 0
	onStop = func() { stopCalls++ }
	defer func() { onStop = nil; ready.Store(false) }()
	isWindow := user32.NewProc("IsWindow")
	err := runUI(t.TempDir(), func() {
		old := progressWindow
		if old == 0 {
			t.Error("missing initial window")
		}
		windowProc(hwnd, msgReady, 0, 0)
		exists, _, _ := isWindow.Call(old)
		controller, _, _ := isWindow.Call(hwnd)
		if exists != 0 || progressWindow != 0 || controller != 1 {
			t.Error("ready must destroy UI, keep controller")
		}
		var msg winMsg
		quit, _, _ := user32.NewProc("PeekMessageW").Call(uintptr(unsafe.Pointer(&msg)), 0, 0x12, 0x12, 0)
		if quit != 0 {
			t.Error("destroying progress must NOT post WM_QUIT")
		}
		windowProc(hwnd, msgNetworkCheck, 0, 0)
		windowProc(hwnd, msgNetworkCheckDone, 0, 0)
		windowProc(hwnd, 0x10, 0, 0)
		if progressWindow != 0 || stopCalls != 0 {
			t.Error("headless update/close must not stop running application")
		}
		windowProc(hwnd, msgRestartUpdate, 0, 0)
		exists, _, _ = isWindow.Call(progressWindow)
		if progressWindow == 0 || exists != 1 || ready.Load() {
			t.Error("explicit restart must create a NEW visible progress window")
		}
		windowProc(hwnd, msgExit, 0, 0)
	})
	if err != nil {
		t.Fatal(err)
	}
}
func TestWindowsSettingsWithoutRuntime(t *testing.T) {
	settingsOnly = true
	defer func() { settingsOnly = false }()
	// Post close after the settings window exists, without driving main's app lock
	// or starting any worker. The UI thread receives a private message only.
	onStop = nil
	root := t.TempDir()
	// Exercise settings-only creation with a timer sent to the UI thread. The
	// native callback handles the one-shot close, and the message loop then ends.
	timerCallback := windows.NewCallback(func(h uintptr, m uint32, id, tick uintptr) uintptr {
		user32.NewProc("KillTimer").Call(0, id)
		if settingsWindow == 0 || progressWindow != 0 {
			t.Error("settings-only must have just its own settings UI")
		}
		pPost.Call(settingsWindow, 0x10, 0, 0)
		return 0
	})
	// SetTimer belongs to the current thread; runUI locks this same OS thread.
	// Lock here too so Go cannot move before entering runUI.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	timer, _, _ := user32.NewProc("SetTimer").Call(0, 0, 100, timerCallback)
	if timer == 0 {
		t.Fatal("SetTimer failed")
	}
	defer user32.NewProc("KillTimer").Call(0, timer)
	if e := runUI(root, nil); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(root, "Data")); !os.IsNotExist(e) {
		t.Fatal("settings created application data")
	}
}
func TestOwnedTreeHelper(t *testing.T) {
	role := os.Getenv("PORTABLE_TREE_ROLE")
	if role == "" {
		return
	}
	root := os.Getenv("PORTABLE_TREE_DIR")
	if e := os.WriteFile(filepath.Join(root, role+".pid"), []byte(fmt.Sprint(os.Getpid())), 0600); e != nil {
		os.Exit(4)
	}
	if role == "root" || role == "child" {
		next := "child"
		if role == "child" {
			next = "grandchild"
		}
		exe, _ := os.Executable()
		cmd := exec.Command(exe, "-test.run=^TestOwnedTreeHelper$")
		cmd.Env = append(os.Environ(), "PORTABLE_TREE_ROLE="+next)
		cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
		if e := cmd.Start(); e != nil {
			os.Exit(3)
		}
	}
	if role == "root" {
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			if _, e := os.Stat(filepath.Join(root, "grandchild.pid")); e == nil {
				os.Exit(0)
			}
			time.Sleep(20 * time.Millisecond)
		}
		os.Exit(5)
	}
	time.Sleep(time.Minute)
	os.Exit(0)
}
func TestWindowsReapDescendantsButNotIndependentProcess(t *testing.T) {
	root := t.TempDir()
	exe, _ := os.Executable()
	outside := exec.Command(exe, "-test.run=^TestOwnedTreeHelper$")
	outside.Env = append(os.Environ(), "PORTABLE_TREE_ROLE=independent", "PORTABLE_TREE_DIR="+root)
	outside.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	if e := outside.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { outside.Process.Kill(); outside.Wait() }()
	independent, e := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(outside.Process.Pid))
	if e != nil {
		t.Fatal(e)
	}
	defer windows.CloseHandle(independent)
	job, e := newJob()
	if e != nil {
		t.Fatal(e)
	}
	defer job.Close()
	child, e := job.Start(exe, []string{"-test.run=^TestOwnedTreeHelper$"}, root, append(os.Environ(), "PORTABLE_TREE_ROLE=root", "PORTABLE_TREE_DIR="+root), filepath.Join(root, "child.log"))
	if e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("root did not exit")
	}
	var handles []windows.Handle
	for _, role := range []string{"child", "grandchild"} {
		raw, e := os.ReadFile(filepath.Join(root, role+".pid"))
		if e != nil {
			t.Fatal(e)
		}
		pid, e := strconv.Atoi(strings.TrimSpace(string(raw)))
		if e != nil {
			t.Fatal(e)
		}
		h, e := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
		if e != nil {
			t.Fatal(e)
		}
		defer windows.CloseHandle(h)
		state, e := windows.WaitForSingleObject(h, 0)
		if e != nil || state != uint32(windows.WAIT_TIMEOUT) {
			t.Fatal("descendant did not survive root exit", state, e)
		}
		handles = append(handles, h)
	}
	if e := reapOwnedJob(job, 10*time.Second); e != nil {
		t.Fatal(e)
	}
	for _, h := range handles {
		state, e := windows.WaitForSingleObject(h, 1000)
		if e != nil || state != windows.WAIT_OBJECT_0 {
			t.Fatal("descendant survived cleanup", state, e)
		}
	}
	state, e := windows.WaitForSingleObject(independent, 0)
	if e != nil || state != uint32(windows.WAIT_TIMEOUT) {
		t.Fatal("independent process was killed", state, e)
	}
}

func TestWindowsCancelBeforeWorkerStarts(t *testing.T) {
	stopActive()
	defer shutdownRequested.Store(false)
	defer exitRequested.Store(false)
	root := t.TempDir()
	worker(root, false)
	stateMu.Lock()
	defer stateMu.Unlock()
	if activeJob != nil || activeCancel != nil || busy.Load() {
		t.Fatal("canceled worker retained launch resources")
	}
	if _, err := os.Stat(filepath.Join(root, "Runtime")); !os.IsNotExist(err) {
		t.Fatal("canceled worker prepared a runtime")
	}
}
