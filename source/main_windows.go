package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var restartUpdate atomic.Bool
var exitRequested atomic.Bool
var workers sync.WaitGroup
var shutdownRequested atomic.Bool
var stateMu sync.Mutex
var activeJob *Job
var activeCancel context.CancelFunc
var activeRun, activeNonce string

func randomID() string {
	b := make([]byte, 20)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
func stopActive() {
	shutdownRequested.Store(true) // Covers cancellation BEFORE the worker publishes its handles.
	stateMu.Lock()
	defer stateMu.Unlock()
	if activeCancel != nil {
		activeCancel()
	}
	if activeJob != nil {
		activeJob.Kill()
	}
}
func safeQuit() {
	stateMu.Lock()
	r, n := activeRun, activeNonce
	stateMu.Unlock()
	if !ready.Load() || r == "" {
		pPost.Call(hwnd, 0x10, 0, 0)
		return
	}
	if e := os.WriteFile(filepath.Join(r, "quit"), []byte(n), 0600); e != nil {
		message("无法正常退出", "无法写入退出请求。若 U 盘已断开，请等待守护器终止进程。\n"+e.Error(), 0x30)
	}
}
func fatalText(root string, e error) {
	writeDiagnostic(root, "launcher.log", e.Error())
	status("启动未完成", e.Error()+"\n可检查网络设置后重试。日志在 Data\\Logs。", 0)
	pPost.Call(hwnd, msgFailed, 0, 0)
}
func worker(root string, update bool) {
	shown := false
	defer func() {
		if v := recover(); v != nil {
			recordPanic(root, "worker", v)
			if shown {
				exitRequested.Store(true)
			} else {
				fatalText(root, fmt.Errorf("启动器内部异常，详情已尝试写入 Data/Logs/launcher-crash.log；请勿反复强制拔盘"))
			}
		}
	}()
	defer busy.Store(false)
	if shutdownRequested.Load() {
		exitRequested.Store(true)
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	job, e := newJob()
	if e != nil {
		fatalText(root, e)
		return
	}
	defer job.Close()
	stateMu.Lock()
	activeJob = job
	activeCancel = cancel
	stateMu.Unlock()
	defer func() {
		stateMu.Lock()
		activeJob = nil
		activeCancel = nil
		activeRun = ""
		activeNonce = ""
		stateMu.Unlock()
	}()
	var cleanup []func()
	reaped := false
	cleanupJob := func() error {
		if reaped {
			return nil
		}
		err := reapOwnedJob(job, 15*time.Second)
		if err == nil {
			reaped = true
		}
		return err
	}
	defer func() {
		cancel()
		err := cleanupJob() // Stop owned processes BEFORE releasing their resources.
		if err != nil {
			writeDiagnostic(root, "launcher.log", "Job cleanup not verified: "+err.Error())
		}
		for i := len(cleanup) - 1; i >= 0; i-- {
			cleanup[i]()
		}
		if err == nil {
			if e := os.RemoveAll(filepath.Join(root, "Data", "Temp")); e != nil {
				writeDiagnostic(root, "launcher.log", "Temporary directory cleanup failed: "+e.Error())
			}
		}
	}()
	if shutdownRequested.Load() {
		exitRequested.Store(true)
		cancel()
		return
	}
	c, e := readConfig(root)
	if e != nil {
		fatalText(root, e)
		return
	}
	sys, e := newSystemProxy(c)
	if e != nil {
		fatalText(root, e)
		return
	}
	cleanup = append(cleanup, func() { sys.Close() })
	status("连接系统网络设置", sys.Description()+"；代理不可用时不会擅自切换为直连。", -1)
	token := randomID()
	route := func(r *http.Request) (*url.URL, error) {
		u, err := sys.Resolve(r)
		mode := "upstream-proxy"
		if u == nil {
			mode = "resolved-direct"
		}
		if err != nil {
			mode = "resolution-error"
		}
		raw, _ := json.Marshal(map[string]string{"role": "launcher-route", "origin": r.URL.Scheme + "://" + r.URL.Host, "route": mode})
		writeDiagnostic(root, "network.log", string(raw))
		return u, err
	}
	bridge, e := startBridgeObserved(route, token, func(event, host, detail string) {
		raw, _ := json.Marshal(map[string]string{"role": "bridge", "event": event, "target": host, "detail": detail})
		writeDiagnostic(root, "network.log", string(raw))
	})
	if e != nil {
		fatalText(root, e)
		return
	}
	cleanup = append(cleanup, func() { bridge.Close() })
	run := filepath.Join(root, "Data", "Run", randomID())
	if e = os.MkdirAll(run, 0700); e != nil {
		fatalText(root, e)
		return
	}
	cleanup = append(cleanup, func() {
		if e := os.RemoveAll(run); e != nil {
			writeDiagnostic(root, "launcher.log", "Run directory cleanup failed: "+e.Error())
		}
	})
	var updateCheckWG sync.WaitGroup
	var updateChecking atomic.Bool
	cleanup = append(cleanup, func() { cancel(); updateCheckWG.Wait() })
	nonce := randomID()
	stateMu.Lock()
	activeRun = run
	activeNonce = nonce
	stateMu.Unlock()
	env, e := portableEnv(root, run, nonce, bridge.URL(), token)
	if e != nil {
		fatalText(root, e)
		return
	}
	transport := newTransport(route)
	cleanup = append(cleanup, transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 90 * time.Minute, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if len(via) > 5 {
			return fmt.Errorf("过多重定向")
		}
		if !allowedOfficialURL(r.URL.String()) {
			return fmt.Errorf("拒绝重定向到非官方下载地址")
		}
		return nil
	}}
	if ctx.Err() != nil {
		exitRequested.Store(true)
		return
	}
	record, e := obtainRuntime(ctx, root, client, job, env, update)
	if e != nil {
		if ctx.Err() != nil {
			return
		}
		fatalText(root, e)
		return
	}
	if e = ctx.Err(); e != nil {
		return
	}
	status("05 / 05 · 启动官方桌面", record.Version+" · "+sys.Description()+"\n正在等待官方欢迎页或工作区加载完成…", -1)
	runtimeDir := filepath.Join(root, "Runtime", record.Directory)
	log := filepath.Join(root, "Data", "Logs", "desktop.log")
	rotateLog(log)
	rotateLog(filepath.Join(root, "Data", "Logs", "chromium.log"))
	work := filepath.Join(root, "Workspace")
	os.MkdirAll(work, 0700)
	if ctx.Err() != nil {
		exitRequested.Store(true)
		return
	}
	child, e := job.Start(filepath.Join(runtimeDir, "WorkBuddy.exe"), []string{"--user-data-dir=" + filepath.Join(root, "Data", "WorkBuddy", "app"), "--disable-breakpad", "--disable-crash-reporter", "--enable-logging=file", "--log-file=" + filepath.Join(root, "Data", "Logs", "chromium.log")}, work, env, log)
	if e != nil {
		fatalText(root, fmt.Errorf("桌面程序启动失败: %w", e))
		return
	}
	ended := make(chan error, 1)
	go func() {
		defer func() {
			if v := recover(); v != nil {
				recordPanic(root, "process-wait", v)
				ended <- fmt.Errorf("子进程监视异常，请查看 launcher-crash.log")
			}
		}()
		ended <- child.Wait()
	}()
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(4 * time.Minute)
	defer deadline.Stop()
	for {
		select {
		case <-ctx.Done():
			exitRequested.Store(true)
			if err := cleanupJob(); err != nil {
				writeDiagnostic(root, "launcher.log", "Canceled launch cleanup not verified: "+err.Error())
				return
			}
			select {
			case <-ended:
			case <-time.After(10 * time.Second):
				writeDiagnostic(root, "launcher.log", "Owned Job is empty; process-wait handle has not returned yet.")
			}
			return
		case e := <-ended:
			cancel() // Stop pending launcher update requests before closing the run.
			if !shown {
				b, _ := os.ReadFile(filepath.Join(run, "ready"))
				shown = string(b) == nonce
			}
			if err := cleanupJob(); err != nil {
				fatalText(root, err)
				return
			}
			if exitErr := desktopExitError(shown, e); exitErr != nil {
				writeDiagnostic(root, "launcher.log", "Desktop exit: "+exitErr.Error())
				if shown {
					exitRequested.Store(true)
				} else {
					fatalText(root, exitErr)
				}
				return
			}
			writeDiagnostic(root, "launcher.log", "Owned job is empty; closing bridge and launcher.")
			requested, _ := os.ReadFile(filepath.Join(run, "update-install.request"))
			if string(requested) == nonce {
				restartUpdate.Store(true)
			} else {
				exitRequested.Store(true)
			}
			return
		case <-ticker.C:
			if _, err := os.Stat(filepath.Join(run, "update-check.request.json")); err == nil && updateChecking.CompareAndSwap(false, true) {
				updateCheckWG.Add(1)
				go func() {
					defer updateCheckWG.Done()
					defer updateChecking.Store(false)
					status("正在检查官方桌面更新", "复用便携启动器的网络代理；不会运行安装程序。", -1)
					pPost.Call(hwnd, msgNetworkCheck, 0, 0)
					defer pPost.Call(hwnd, msgNetworkCheckDone, 0, 0)
					if err := serviceUpdateCheck(ctx, client, run, nonce); err != nil {
						writeDiagnostic(root, "network.log", "portable-update request failed")
					}
				}()
			}
			if !shown {
				b, e := os.ReadFile(filepath.Join(run, "ready"))
				if e == nil && string(b) == nonce {
					shown = true
					deadline.Stop()
					pPost.Call(hwnd, msgReady, 0, 0)
				}
			}
		case <-deadline.C:
			if !shown {
				status("桌面启动较慢，仍在等待", "已等待超过 4 分钟；不会自动强制终止。可继续等待，或手动点击“退出 / 取消”。", -1)
				writeDiagnostic(root, "launcher.log", "Startup not ready after four minutes; continuing without forced termination.")
			}
		}
	}
}
func rotateLog(p string) {
	if s, e := os.Stat(p); e == nil && s.Size() > 4<<20 {
		os.Remove(p + ".previous")
		os.Rename(p, p+".previous")
	}
}
func main() {
	defer func() {
		if v := recover(); v != nil {
			stopActive()
			exe, _ := os.Executable()
			root := filepath.Dir(exe)
			recordPanic(root, "main", v)
			message("启动器内部异常", "错误信息已尝试写入 U 盘 Data/Logs/launcher-crash.log。\n请保留日志以便排查，不要以反复拔盘代替正常退出。", 0x10)
		}
	}()
	exe, e := os.Executable()
	if e != nil {
		message("无法确定程序路径", e.Error(), 0x10)
		return
	}
	root := filepath.Dir(exe)
	if strings.HasPrefix(root, `\\`) {
		message("WorkBuddy Portable", "请从本地磁盘或 U 盘运行，不支持 UNC 网络共享路径。", 0x10)
		return
	}
	if utf16Units(root) > 140 {
		message("路径过长", "请把便携目录放到 U 盘根目录，例如 E:\\WorkBuddy-Portable，避免 Windows 路径过长导致官方组件失败。", 0x10)
		return
	}
	for _, arg := range os.Args[1:] {
		if arg == "--settings" {
			settingsOnly = true
			if err := runUI(root, nil); err != nil {
				message("网络设置", err.Error(), 0x10)
			}
			return
		}
	}
	for _, rel := range []string{"Data/Logs", "Data/Run", "Runtime", "Workspace"} {
		if e = os.MkdirAll(filepath.Join(root, filepath.FromSlash(rel)), 0700); e != nil {
			message("无法写入 U 盘", e.Error(), 0x10)
			return
		}
	}
	setupCrashOutput(root)
	writeDiagnostic(root, "launcher.log", "Launcher "+launcherVersion+"; runtime adapter "+wrapperVersion+".")
	lock, e := windows.CreateFile(wstr(filepath.Join(root, "Data", ".portable.lock")), windows.GENERIC_READ|windows.GENERIC_WRITE|windows.DELETE, 0, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_HIDDEN|windows.FILE_FLAG_DELETE_ON_CLOSE, 0)
	if e != nil {
		message("无法启动", "此便携目录可能已在运行、被占用或不可写。\n请先从应用自己的菜单/托盘退出已有 WorkBuddy。\n\n"+e.Error(), 0x30)
		return
	}
	defer windows.CloseHandle(lock)
	update := false
	for _, a := range os.Args[1:] {
		if a == "--update" {
			update = true
		}
	}
	onStop = stopActive
	onRemoved = stopActive
	onSafeQuit = safeQuit
	launch := func(check bool) {
		if !shutdownRequested.Load() && busy.CompareAndSwap(false, true) {
			ready.Store(false)
			pShow.Call(progressControl, 5)
			workers.Add(1)
			go func() {
				defer workers.Done()
				worker(root, check)
				if restartUpdate.Swap(false) {
					pPost.Call(hwnd, msgRestartUpdate, 0, 0)
				} else if exitRequested.Swap(false) {
					pPost.Call(hwnd, msgExit, 0, 0)
				}
			}()
		}
	}
	onRetry = func() { launch(update) }
	onUpdateStart = func() { launch(true) }
	e = runUI(root, onRetry)
	stopActive()
	workers.Wait() // Never leave before workers release Job/bridge/locks.
	if e != nil && !errors.Is(e, context.Canceled) {
		message("WorkBuddy Portable", e.Error(), 0x10)
	}
}
