package main

import (
	"encoding/json"
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"unsafe"
)

var user32 = windows.NewLazySystemDLL("user32.dll")
var kernel32 = windows.NewLazySystemDLL("kernel32.dll")
var gdi32 = windows.NewLazySystemDLL("gdi32.dll")
var pDef = user32.NewProc("DefWindowProcW")
var pCreate = user32.NewProc("CreateWindowExW")
var pSend = user32.NewProc("SendMessageW")
var pPost = user32.NewProc("PostMessageW")
var pShow = user32.NewProc("ShowWindow")
var pSetText = user32.NewProc("SetWindowTextW")
var pDestroy = user32.NewProc("DestroyWindow")

const msgStatus = 0x8001
const msgReady = 0x8002
const msgExit = 0x8003
const msgFailed = 0x8004
const msgRestartUpdate = 0x8006
const msgNetworkCheck = 0x8007
const msgNetworkCheckDone = 0x8008
const windowClass = "WBPPortableWindow"
const productLabel = "WorkBuddy"

// hwnd is an invisible control endpoint, NEVER the progress window. It receives
// worker/device messages after the separate progressWindow has been destroyed.
var hwnd, progressWindow, titleControl, detailControl, progressControl, footerControl, retryControl uintptr
var settingsWindow, modeControl, urlControl, noProxyControl uintptr
var font, headingFont, smallFont, instance, icon uintptr
var busy, ready atomic.Bool
var statusMu sync.Mutex
var statusTitle, statusDetail string
var statusPercent int
var appRoot string
var onRetry, onUpdateStart, onStop, onSafeQuit, onRemoved func()
var settingsOnly bool
var uiDPI int32 = 96

type wndClass struct {
	Size, Style                        uint32
	Proc                               uintptr
	ClsExtra, WndExtra                 int32
	Instance, Icon, Cursor, Background uintptr
	Menu, Class                        *uint16
	IconSmall                          uintptr
}
type point struct{ X, Y int32 }
type winMsg struct {
	Hwnd           uintptr
	Message        uint32
	WParam, LParam uintptr
	Time           uint32
	Pt             point
	Private        uint32
}

func px(v int32) uintptr { return uintptr(scaleUI(v, uiDPI)) }
func setText(h uintptr, s string) {
	if h != 0 {
		pSetText.Call(h, uintptr(unsafe.Pointer(wstr(s))))
	}
}
func status(t, d string, p int) {
	statusMu.Lock()
	statusTitle = t
	statusDetail = d
	statusPercent = p
	statusMu.Unlock()
	if hwnd != 0 {
		pPost.Call(hwnd, msgStatus, 0, 0)
	}
}
func message(title, text string, flags uintptr) uintptr {
	r, _, _ := user32.NewProc("MessageBoxW").Call(hwnd, uintptr(unsafe.Pointer(wstr(text))), uintptr(unsafe.Pointer(wstr(title))), flags)
	return r
}
func confirm(t, s string) bool { return message(t, s, 0x24|0x100) == 6 }
func control(class, text string, style uintptr, x, y, w, h int32, id, parent uintptr) uintptr {
	r, _, _ := pCreate.Call(0, uintptr(unsafe.Pointer(wstr(class))), uintptr(unsafe.Pointer(wstr(text))), style|0x40000000|0x10000000, px(x), px(y), px(w), px(h), parent, id, instance, 0)
	pSend.Call(r, 0x30, font, 1)
	return r
}
func getText(h uintptr) string {
	n, _, _ := user32.NewProc("GetWindowTextLengthW").Call(h)
	b := make([]uint16, n+1)
	user32.NewProc("GetWindowTextW").Call(h, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)))
	return windows.UTF16ToString(b)
}
func updateStatus() {
	if progressWindow == 0 {
		return
	}
	statusMu.Lock()
	t, d, p := statusTitle, statusDetail, statusPercent
	statusMu.Unlock()
	setText(titleControl, t)
	setText(detailControl, d)
	style, _, _ := user32.NewProc("GetWindowLongPtrW").Call(progressControl, ^uintptr(15))
	if p < 0 {
		user32.NewProc("SetWindowLongPtrW").Call(progressControl, ^uintptr(15), style|8)
		pSend.Call(progressControl, 0x40a, 1, 35)
	} else {
		pSend.Call(progressControl, 0x40a, 0, 0)
		user32.NewProc("SetWindowLongPtrW").Call(progressControl, ^uintptr(15), style&^8)
		pSend.Call(progressControl, 0x402, uintptr(p), 0)
	}
}

// Create a compact client area, then add the ACTUAL Windows frame. Font/UI
// scaling is capped separately; native title-bar metrics must not clip buttons.
func createUIWindow(title string, x, y, width, height int32) (uintptr, error) {
	const style = 0x00c80000
	const exStyle = 0x40000
	r := struct{ Left, Top, Right, Bottom int32 }{0, 0, scaleUI(width, uiDPI), scaleUI(height, uiDPI)}
	if ok, _, e := user32.NewProc("AdjustWindowRectEx").Call(uintptr(unsafe.Pointer(&r)), style, 0, exStyle); ok == 0 {
		return 0, e
	}
	h, _, e := pCreate.Call(exStyle, uintptr(unsafe.Pointer(wstr(windowClass))), uintptr(unsafe.Pointer(wstr(title))), style, px(x), px(y), uintptr(r.Right-r.Left), uintptr(r.Bottom-r.Top), hwnd, 0, instance, 0)
	if h == 0 {
		return 0, e
	}
	return h, nil
}
func createProgressWindow() error {
	if progressWindow != 0 {
		return nil
	}
	var e error
	progressWindow, e = createUIWindow(productLabel+" Portable · 准备中", 60, 40, progressClientWidth, progressClientHeight)
	if progressWindow == 0 {
		return fmt.Errorf("无法创建启动进度窗口: %v", e)
	}
	h := progressWindow
	brand := control("STATIC", productLabel+"  /  PORTABLE", 0, 20, 16, 624, 32, 0, h)
	pSend.Call(brand, 0x30, headingFont, 1)
	desc := control("STATIC", "社区便携启动器 · "+launcherVersion+" · 主界面就绪后自动关闭本窗口", 0, 20, 54, 624, 22, 0, h)
	pSend.Call(desc, 0x30, smallFont, 1)
	titleControl = control("STATIC", "正在检查 U 盘与运行环境…", 0, 20, 91, 624, 28, 0, h)
	detailControl = control("STATIC", "首次准备需要联网下载，已有运行时优先复用。", 0, 20, 124, 624, 74, 0, h)
	progressControl = control("msctls_progress32", "", 8, 20, 207, 624, 16, 0, h)
	pSend.Call(progressControl, 0x406, 0, 100)
	footerControl = control("STATIC", "主界面打开后：无启动器窗口、无启动器托盘图标。\n应用真正退出后自动回收本次进程；请勿直接拔盘。", 0, 20, 240, 624, 42, 0, h)
	pSend.Call(footerControl, 0x30, smallFont, 1)
	control("BUTTON", "网络设置", 0x10000, 20, 320, 116, 32, 101, h)
	retryControl = control("BUTTON", "重试", 0x10000, 404, 320, 104, 32, 102, h)
	pShow.Call(retryControl, 0)
	control("BUTTON", "退出 / 取消", 0x10000, 524, 320, 120, 32, 103, h)
	return nil
}
func showMain() {
	if e := createProgressWindow(); e != nil {
		message("无法显示准备窗口", e.Error(), 0x10)
		if onStop != nil {
			onStop()
		}
		pDestroy.Call(hwnd)
		return
	}
	pShow.Call(progressWindow, 5)
	user32.NewProc("SetForegroundWindow").Call(progressWindow)
	updateStatus()
}
func destroyProgressWindow() {
	if settingsWindow != 0 && !settingsOnly {
		pDestroy.Call(settingsWindow)
	}
	if progressWindow != 0 {
		pDestroy.Call(progressWindow)
	}
	// All visible controls die with their parent. No ShowWindow(SW_HIDE), no tray.
}
func showSettings() {
	if settingsWindow != 0 {
		user32.NewProc("SetForegroundWindow").Call(settingsWindow)
		return
	}
	settingsWindow, _ = createUIWindow(productLabel+" Portable · 网络设置", 70, 50, settingsClientWidth, settingsClientHeight)
	if settingsWindow == 0 {
		message("网络设置", "无法创建设置窗口", 0x10)
		if settingsOnly {
			pDestroy.Call(hwnd)
		}
		return
	}
	h := settingsWindow
	control("STATIC", "网络方式（保存后下次启动 / 重试生效）", 0, 20, 18, 584, 24, 0, h)
	modeControl = control("COMBOBOX", "", 0x3|0x00200000|0x10000, 20, 46, 584, 180, 0, h)
	for _, s := range []string{"自动：环境变量 → Windows 系统 / PAC / WPAD", "手动：HTTP / HTTPS / SOCKS5 代理", "直连：不使用上游代理"} {
		pSend.Call(modeControl, 0x143, 0, uintptr(unsafe.Pointer(wstr(s))))
	}
	c, _ := readConfig(appRoot)
	sel := 0
	if c.ProxyMode == "manual" {
		sel = 1
	}
	if c.ProxyMode == "direct" {
		sel = 2
	}
	pSend.Call(modeControl, 0x14e, uintptr(sel), 0)
	control("STATIC", "手动代理地址（例：http://127.0.0.1:7890）", 0, 20, 90, 584, 24, 0, h)
	urlControl = control("EDIT", c.ProxyURL, 0x00800000|0x80|0x10000, 20, 117, 584, 28, 0, h)
	control("STATIC", "直连例外（逗号分隔；本地回环始终直连）", 0, 20, 161, 584, 24, 0, h)
	noProxyControl = control("EDIT", c.NoProxy, 0x00800000|0x80|0x10000, 20, 188, 584, 28, 0, h)
	tip := control("STATIC", "浏览器扩展专用代理需手动填写。\n应用启动后，可双击 Network-Settings.cmd 再打开本设置。", 0, 20, 233, 584, 44, 0, h)
	pSend.Call(tip, 0x30, smallFont, 1)
	control("BUTTON", "保存设置", 0x10000, 480, 292, 124, 32, 200, h)
	pShow.Call(h, 5)
}
func saveSettings() {
	i, _, _ := pSend.Call(modeControl, 0x147, 0, 0)
	mode := "auto"
	if i == 1 {
		mode = "manual"
	}
	if i == 2 {
		mode = "direct"
	}
	c := Config{mode, getText(urlControl), getText(noProxyControl)}
	if mode == "manual" {
		if _, e := validateProxy(c.ProxyURL); e != nil {
			message("代理设置", e.Error(), 0x30)
			return
		}
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	p := filepath.Join(appRoot, "portable.json")
	if e := os.WriteFile(p+".new", b, 0600); e != nil {
		message("保存失败", e.Error(), 0x10)
		return
	}
	if e := windows.MoveFileEx(wstr(p+".new"), wstr(p), windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); e != nil {
		message("保存失败", e.Error(), 0x10)
		return
	}
	message("已保存", "设置在应用下次启动或准备重试时生效。", 0x40)
	pPost.Call(settingsWindow, 0x10, 0, 0)
}
func windowProc(h uintptr, m uint32, w, l uintptr) uintptr {
	switch m {
	case 0x138:
		gdi32.NewProc("SetBkMode").Call(w, 1)
		b, _, _ := gdi32.NewProc("GetStockObject").Call(0)
		return b
	case 0x10:
		if h == settingsWindow {
			pDestroy.Call(h)
			if settingsOnly {
				pDestroy.Call(hwnd)
			}
			return 0
		}
		if ready.Load() {
			destroyProgressWindow()
			return 0
		}
		if busy.Load() && !confirm("停止准备？", "准备工作会中止；本次进程会清理，已有会话不删除。") {
			return 0
		}
		if onStop != nil {
			onStop()
		}
		pDestroy.Call(hwnd)
		return 0
	case 2:
		if h == progressWindow {
			progressWindow = 0
			titleControl = 0
			detailControl = 0
			progressControl = 0
			footerControl = 0
			retryControl = 0
		}
		if h == settingsWindow {
			settingsWindow = 0
		}
		if h == hwnd {
			user32.NewProc("PostQuitMessage").Call(0)
		}
		return 0
	case 0x111:
		switch w & 0xffff {
		case 101:
			showSettings()
		case 102:
			if onRetry != nil && !busy.Load() {
				pShow.Call(retryControl, 0)
				onRetry()
			}
		case 103:
			pPost.Call(hwnd, 0x10, 0, 0)
		case 200:
			saveSettings()
		}
		return 0
	case msgStatus:
		updateStatus()
		return 0
	case msgReady:
		ready.Store(true)
		destroyProgressWindow()
		return 0
	case msgFailed:
		ready.Store(false)
		showMain()
		pShow.Call(progressControl, 0)
		pShow.Call(retryControl, 5)
		return 0
	case msgNetworkCheck, msgNetworkCheckDone:
		// Background update checks use the application's result UI; do not recreate
		// the completed launcher progress window or a launcher taskbar/tray entry.
		if !ready.Load() {
			updateStatus()
		}
		return 0
	case msgRestartUpdate:
		ready.Store(false)
		showMain()
		pShow.Call(progressControl, 5)
		pShow.Call(retryControl, 0)
		if onUpdateStart != nil {
			onUpdateStart()
		}
		return 0
	case msgExit:
		pDestroy.Call(hwnd)
		return 0 // no modal "success" box keeping us alive
	case 0x219:
		if h == hwnd && w == 0x8004 && l != 0 {
			type volume struct {
				Size, Type, Reserved, Mask uint32
				Flags                      uint16
			}
			var v volume
			var n uintptr
			e := windows.ReadProcessMemory(windows.CurrentProcess(), l, (*byte)(unsafe.Pointer(&v)), unsafe.Sizeof(v), &n)
			if e == nil && n == unsafe.Sizeof(v) && v.Type == 2 {
				drive := filepath.VolumeName(appRoot)
				if len(drive) == 2 {
					bit := uint32(1) << uint(stringsUpperASCII(drive[0])-'A')
					if v.Mask&bit != 0 {
						if onRemoved != nil {
							onRemoved()
						}
						pDestroy.Call(hwnd)
					}
				}
			}
		}
		return 0
	case 0x11:
		return 1
	case 0x16:
		if w != 0 {
			if onStop != nil {
				onStop()
			}
			pDestroy.Call(hwnd)
		}
		return 0
	}
	r, _, _ := pDef.Call(h, uintptr(m), w, l)
	return r
}
func stringsUpperASCII(b byte) byte {
	if b >= 'a' && b <= 'z' {
		return b - 32
	}
	return b
}
func runUI(root string, begin func()) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	appRoot = root
	user32.NewProc("SetProcessDPIAware").Call()
	dc, _, _ := user32.NewProc("GetDC").Call(0)
	dpi, _, _ := gdi32.NewProc("GetDeviceCaps").Call(dc, 88)
	user32.NewProc("ReleaseDC").Call(0, dc)
	var area struct{ Left, Top, Right, Bottom int32 }
	user32.NewProc("SystemParametersInfoW").Call(0x30, 0, uintptr(unsafe.Pointer(&area)), 0)
	uiDPI = fitUIDPI(int32(dpi), area.Right-area.Left, area.Bottom-area.Top)
	type icc struct{ Size, Classes uint32 }
	ic := icc{8, 0x20}
	windows.NewLazySystemDLL("comctl32.dll").NewProc("InitCommonControlsEx").Call(uintptr(unsafe.Pointer(&ic)))
	instance, _, _ = kernel32.NewProc("GetModuleHandleW").Call(0)
	cursor, _, _ := user32.NewProc("LoadCursorW").Call(0, 32512)
	icon, _, _ = user32.NewProc("LoadIconW").Call(instance, 1)
	if icon == 0 {
		icon, _, _ = user32.NewProc("LoadIconW").Call(0, 32516)
	}
	mkFont := func(size, weight int32) uintptr {
		v, _, _ := gdi32.NewProc("CreateFontW").Call(uintptr(-scaleUI(size, uiDPI)), 0, 0, 0, uintptr(weight), 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(wstr("Microsoft YaHei UI"))))
		return v
	}
	font = mkFont(bodyFontSize, 400)
	headingFont = mkFont(headingFontSize, 600)
	smallFont = mkFont(smallFontSize, 400)
	defer gdi32.NewProc("DeleteObject").Call(font)
	defer gdi32.NewProc("DeleteObject").Call(headingFont)
	defer gdi32.NewProc("DeleteObject").Call(smallFont)
	wc := wndClass{Size: uint32(unsafe.Sizeof(wndClass{})), Proc: windows.NewCallback(windowProc), Instance: instance, Icon: icon, IconSmall: icon, Cursor: cursor, Background: 6, Class: wstr(windowClass)}
	if v, _, e := user32.NewProc("RegisterClassExW").Call(uintptr(unsafe.Pointer(&wc))); v == 0 && e != syscall.Errno(1410) {
		return e
	}
	// A never-shown top-level control window receives device broadcasts. This is
	// not the destroyed progress window, and has no tray/taskbar UI.
	hwnd, _, _ = pCreate.Call(0x80, uintptr(unsafe.Pointer(wstr(windowClass))), uintptr(unsafe.Pointer(wstr(productLabel+" portable controller"))), 0, 0, 0, 0, 0, 0, 0, instance, 0)
	if hwnd == 0 {
		return fmt.Errorf("无法创建无界面控制端点")
	}
	defer pDestroy.Call(hwnd)
	if settingsOnly {
		showSettings()
	} else {
		if e := createProgressWindow(); e != nil {
			return e
		}
		pShow.Call(progressWindow, 5)
		user32.NewProc("UpdateWindow").Call(progressWindow)
		if begin != nil {
			begin()
		}
	}
	var msg winMsg
	for {
		r, _, _ := user32.NewProc("GetMessageW").Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if r == 0 {
			break
		}
		if int32(r) == -1 {
			return fmt.Errorf("窗口消息循环失败")
		}
		user32.NewProc("TranslateMessage").Call(uintptr(unsafe.Pointer(&msg)))
		user32.NewProc("DispatchMessageW").Call(uintptr(unsafe.Pointer(&msg)))
	}
	return nil
}
