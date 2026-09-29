package main

import (
	"fmt"
	"golang.org/x/net/http/httpproxy"
	"golang.org/x/sys/windows"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"
	"unsafe"
)

var wh = windows.NewLazySystemDLL("winhttp.dll")
var whOpen = wh.NewProc("WinHttpOpen")
var whIE = wh.NewProc("WinHttpGetIEProxyConfigForCurrentUser")
var whURL = wh.NewProc("WinHttpGetProxyForUrl")
var whClose = wh.NewProc("WinHttpCloseHandle")
var globalFree = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalFree")

type ieConfig struct {
	AutoDetect         int32
	PAC, Proxy, Bypass *uint16
}
type autoOptions struct {
	Flags, Detect uint32
	URL           *uint16
	Reserved      uintptr
	Reserved2     uint32
	AutoLogon     int32
}
type proxyInfo struct {
	Access        uint32
	Proxy, Bypass *uint16
}
type cachedRoute struct {
	u     *url.URL
	err   error
	until time.Time
}
type SystemProxy struct {
	closed  bool
	c       Config
	ie      ieConfig
	session uintptr
	env     func(*url.URL) (*url.URL, error)
	hasEnv  bool
	cache   map[string]cachedRoute
	mu      sync.Mutex
}

func wstr(s string) *uint16 { p, _ := windows.UTF16PtrFromString(s); return p }
func readW(p *uint16) string {
	if p == nil {
		return ""
	}
	return windows.UTF16PtrToString(p)
}
func newSystemProxy(c Config) (*SystemProxy, error) {
	s := &SystemProxy{c: c, cache: map[string]cachedRoute{}}
	get := func(keys ...string) string {
		for _, k := range keys {
			if v := strings.TrimSpace(os.Getenv(k)); v != "" {
				return v
			}
		}
		return ""
	}
	hp := get("http_proxy", "HTTP_PROXY")
	sp := get("https_proxy", "HTTPS_PROXY")
	all := get("all_proxy", "ALL_PROXY")
	if hp == "" {
		hp = all
	}
	if sp == "" {
		sp = all
	}
	if sp == "" {
		sp = hp
	}
	s.hasEnv = hp != "" || sp != ""
	s.env = (&httpproxy.Config{HTTPProxy: hp, HTTPSProxy: sp, NoProxy: get("no_proxy", "NO_PROXY") + "," + c.NoProxy}).ProxyFunc()
	if c.ProxyMode != "auto" || s.hasEnv {
		return s, nil
	}
	ok, _, e := whIE.Call(uintptr(unsafe.Pointer(&s.ie)))
	if ok == 0 {
		return nil, fmt.Errorf("无法读取 Windows 系统代理: %v", e)
	}
	h, _, e := whOpen.Call(uintptr(unsafe.Pointer(wstr("WorkBuddy-Portable/1.0"))), 1, 0, 0, 0)
	if h == 0 {
		s.Close()
		return nil, e
	}
	s.session = h
	wh.NewProc("WinHttpSetTimeouts").Call(h, 10000, 10000, 10000, 10000)
	return s, nil
}
func (s *SystemProxy) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	if s.session != 0 {
		whClose.Call(s.session)
	}
	for _, p := range []*uint16{s.ie.PAC, s.ie.Proxy, s.ie.Bypass} {
		if p != nil {
			globalFree.Call(uintptr(unsafe.Pointer(p)))
		}
	}
}
func localHost(h string) bool {
	h = strings.TrimSuffix(strings.ToLower(h), ".")
	ip := net.ParseIP(h)
	return h == "localhost" || (ip != nil && (ip.IsLoopback() || ip.IsUnspecified()))
}
func bypass(host, list string) bool {
	host = strings.ToLower(host)
	for _, pattern := range strings.FieldsFunc(strings.ToLower(list), func(r rune) bool { return r == ';' || r == ',' || r == ' ' }) {
		if pattern == "<local>" && !strings.Contains(host, ".") {
			return true
		}
		if match, _ := path.Match(pattern, host); match {
			return true
		}
	}
	return false
}
func parseWindowsProxy(raw, scheme string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var fallback, selected string
	hasSocks := false
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool { return r == ';' || r == ' ' }) {
		k, v, ok := strings.Cut(part, "=")
		if ok {
			if strings.EqualFold(k, "socks") {
				hasSocks = true
			}
			if strings.EqualFold(k, scheme) {
				selected = v
			}
		} else if fallback == "" {
			fallback = part
		}
	}
	if selected == "" {
		selected = fallback
	}
	if selected == "" {
		if hasSocks {
			return nil, fmt.Errorf("系统仅提供 SOCKS 代理映射，请在网络设置中手动填写 socks5:// 地址")
		}
		return nil, nil
	}
	if !strings.Contains(selected, "://") {
		selected = "http://" + selected
	}
	return validateProxy(selected)
}
func (s *SystemProxy) Resolve(r *http.Request) (*url.URL, error) {
	if localHost(r.URL.Hostname()) || bypass(r.URL.Hostname(), s.c.NoProxy) {
		return nil, nil
	}
	switch s.c.ProxyMode {
	case "direct":
		return nil, nil
	case "manual":
		return validateProxy(s.c.ProxyURL)
	}
	if s.hasEnv {
		u, e := s.env(r.URL)
		if e != nil {
			return nil, fmt.Errorf("环境代理无效")
		}
		if u != nil {
			u, e = validateProxy(u.String())
		}
		return u, e
	}
	key := r.URL.String()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("proxy resolver closed")
	}
	if c, ok := s.cache[key]; ok && time.Now().Before(c.until) {
		return c.u, c.err
	}
	u, e := s.resolveWindows(r.URL)
	if len(s.cache) > 512 {
		s.cache = map[string]cachedRoute{}
	}
	s.cache[key] = cachedRoute{u, e, time.Now().Add(time.Minute)}
	return u, e
}
func simplifiedPath(u *url.URL) string { return u.EscapedPath() } // PAC can route by path; queries are not persisted.
func (s *SystemProxy) resolveWindows(u *url.URL) (*url.URL, error) {
	if s.ie.PAC != nil || s.ie.AutoDetect != 0 {
		opt := autoOptions{AutoLogon: 0}
		if s.ie.PAC != nil {
			opt.Flags = 2
			opt.URL = s.ie.PAC
		} else {
			opt.Flags = 1
			opt.Detect = 3
		}
		var pi proxyInfo
		ok, _, e := whURL.Call(s.session, uintptr(unsafe.Pointer(wstr(u.String()))), uintptr(unsafe.Pointer(&opt)), uintptr(unsafe.Pointer(&pi)))
		if ok != 0 {
			defer func() {
				for _, p := range []*uint16{pi.Proxy, pi.Bypass} {
					if p != nil {
						globalFree.Call(uintptr(unsafe.Pointer(p)))
					}
				}
			}()
			if pi.Access == 1 || bypass(u.Hostname(), readW(pi.Bypass)) {
				return nil, nil
			}
			return parseWindowsProxy(readW(pi.Proxy), u.Scheme)
		}
		// WPAD absence is normal. An explicitly configured PAC failure is never silently made direct.
		if s.ie.PAC != nil {
			return nil, fmt.Errorf("PAC 解析失败（%v），请修复系统 PAC 或设置手动代理", e)
		}
		if readW(s.ie.Proxy) == "" && e != windows.Errno(12180) {
			return nil, fmt.Errorf("自动代理解析失败: %v", e)
		}
	}
	if bypass(u.Hostname(), readW(s.ie.Bypass)) {
		return nil, nil
	}
	return parseWindowsProxy(readW(s.ie.Proxy), u.Scheme)
}
func (s *SystemProxy) Description() string {
	switch s.c.ProxyMode {
	case "manual":
		return "手动代理（通过本机转接）"
	case "direct":
		return "直连（已明确选择）"
	}
	if s.hasEnv {
		return "环境变量代理"
	}
	if s.ie.PAC != nil {
		return "Windows PAC 自动代理"
	}
	if s.ie.Proxy != nil {
		return "Windows 系统代理"
	}
	if s.ie.AutoDetect != 0 {
		return "Windows 自动检测代理 / 无代理时直连"
	}
	return "Windows 系统设置：直连"
}
