package main

import (
	"fmt"
	"golang.org/x/sys/windows"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
	"unsafe"
)

type Job struct{ h windows.Handle }

func newJob() (*Job, error) {
	h, e := windows.CreateJobObject(nil, nil)
	if e != nil {
		return nil, e
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, e = windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); e != nil {
		windows.CloseHandle(h)
		return nil, e
	}
	return &Job{h}, nil
}
func (j *Job) Kill() error { return windows.TerminateJobObject(j.h, 2) }
func (j *Job) Close()      { windows.CloseHandle(j.h) }

func (j *Job) WaitEmpty(timeout time.Duration) error {
	type accounting struct {
		User, Kernel, PeriodUser, PeriodKernel int64
		PageFaults, Total, Active, Terminated  uint32
	}
	until := time.Now().Add(timeout)
	for {
		var a accounting
		if e := windows.QueryInformationJobObject(j.h, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&a)), uint32(unsafe.Sizeof(a)), nil); e != nil {
			return e
		}
		if a.Active == 0 {
			return nil
		}
		if time.Now().After(until) {
			return fmt.Errorf("子进程尚未完全结束，请不要拔盘；关闭启动器后再尝试弹出 U 盘")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

type Child struct {
	handle windows.Handle
	pid    uint32
}

func (j *Job) Start(exe string, args []string, dir string, env []string, logPath string) (*Child, error) {
	exeW, e := windows.UTF16PtrFromString(exe)
	if e != nil {
		return nil, fmt.Errorf("可执行文件路径包含 NUL: %w", e)
	}
	dirW, e := windows.UTF16PtrFromString(dir)
	if e != nil {
		return nil, fmt.Errorf("工作目录包含 NUL: %w", e)
	}
	logW, e := windows.UTF16PtrFromString(logPath)
	if e != nil {
		return nil, e
	}
	log, e := windows.CreateFile(logW, windows.FILE_APPEND_DATA, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if e != nil {
		return nil, e
	}
	defer windows.CloseHandle(log)
	if e = windows.SetHandleInformation(log, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); e != nil {
		return nil, e
	}
	nul, e := windows.CreateFile(wstr("NUL"), windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, nil, windows.OPEN_EXISTING, 0, 0)
	if e != nil {
		return nil, e
	}
	defer windows.CloseHandle(nul)
	if e = windows.SetHandleInformation(nul, windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); e != nil {
		return nil, e
	}
	cmd, e := windowsCommandLine(exe, args)
	if e != nil {
		return nil, e
	}
	block, e := windowsEnvironmentBlock(env)
	if e != nil {
		return nil, e
	}

	si := windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfo{})), Flags: windows.STARTF_USESTDHANDLES, StdInput: nul, StdOutput: log, StdErr: log}
	var pi windows.ProcessInformation
	e = windows.CreateProcess(exeW, &cmd[0], nil, nil, true, windows.CREATE_SUSPENDED|windows.CREATE_UNICODE_ENVIRONMENT|windows.CREATE_NO_WINDOW, &block[0], dirW, &si, &pi)
	if e != nil {
		return nil, e
	}
	defer windows.CloseHandle(pi.Thread)
	if e = windows.AssignProcessToJobObject(j.h, pi.Process); e != nil {
		windows.TerminateProcess(pi.Process, 1)
		windows.CloseHandle(pi.Process)
		return nil, fmt.Errorf("无法启用进程保护，已停止启动: %w", e)
	}
	if _, e = windows.ResumeThread(pi.Thread); e != nil {
		windows.TerminateProcess(pi.Process, 1)
		windows.CloseHandle(pi.Process)
		return nil, e
	}
	return &Child{pi.Process, pi.ProcessId}, nil
}
func (c *Child) Wait() error {
	defer windows.CloseHandle(c.handle)
	if _, e := windows.WaitForSingleObject(c.handle, windows.INFINITE); e != nil {
		return e
	}
	var code uint32
	if e := windows.GetExitCodeProcess(c.handle, &code); e != nil {
		return e
	}
	if code != 0 {
		return fmt.Errorf("进程异常退出：%d（0x%08X）。请检查 desktop.log、chromium.log 和 bootstrap-error.txt", code, code)
	}
	return nil
}
func portableEnv(root, run, nonce, bridge, token string) ([]string, error) {
	m := map[string]string{}
	for _, v := range os.Environ() {
		k, v, ok := strings.Cut(v, "=")
		if ok && k != "" {
			m[strings.ToUpper(k)] = v
		}
	}
	paths := map[string]string{"HOME": "Home", "USERPROFILE": "Home", "APPDATA": "AppData/Roaming", "LOCALAPPDATA": "AppData/Local", "TEMP": "Temp", "TMP": "Temp", "WORKBUDDY_CONFIG_DIR": "WorkBuddy", "CODEBUDDY_CONFIG_DIR": "WorkBuddy", "WORKBUDDY_USER_DATA_DIR": "WorkBuddy/app", "WORKBUDDY_DATA_DIR": "Models", "PROGRAMDATA": "ProgramData", "XDG_CONFIG_HOME": "Home/.config", "XDG_CACHE_HOME": "Cache", "XDG_DATA_HOME": "Home/.local/share", "NPM_CONFIG_CACHE": "Cache/npm", "NPM_CONFIG_USERCONFIG": "Home/.npmrc", "YARN_CACHE_FOLDER": "Cache/yarn", "PNPM_HOME": "Home/pnpm", "COREPACK_HOME": "Cache/corepack", "PIP_CACHE_DIR": "Cache/pip", "UV_CACHE_DIR": "Cache/uv", "CARGO_HOME": "Home/.cargo", "RUSTUP_HOME": "Home/.rustup", "GIT_CONFIG_GLOBAL": "Home/.gitconfig"}
	for k, v := range paths {
		p := filepath.Join(root, "Data", filepath.FromSlash(v))
		dir := p
		if k == "NPM_CONFIG_USERCONFIG" || k == "GIT_CONFIG_GLOBAL" {
			dir = filepath.Dir(p)
		}
		if e := os.MkdirAll(dir, 0700); e != nil {
			return nil, e
		}
		m[k] = p
	}
	m["HOMEDRIVE"] = filepath.VolumeName(m["USERPROFILE"])
	m["HOMEPATH"] = strings.TrimPrefix(m["USERPROFILE"], m["HOMEDRIVE"])
	m["WBP_PORTABLE_ROOT"] = root
	m["WBP_PORTABLE_RUN"] = run
	m["WBP_PORTABLE_NONCE"] = nonce
	m["WBP_PORTABLE_VERSION"] = supportedVersion
	if _, ok := m["NODE_USE_SYSTEM_CA"]; !ok {
		m["NODE_USE_SYSTEM_CA"] = "1"
	}
	m["DO_NOT_TRACK"] = "1"
	m["POWERSHELL_TELEMETRY_OPTOUT"] = "1"
	m["DOTNET_CLI_TELEMETRY_OPTOUT"] = "1"
	m["NPM_CONFIG_UPDATE_NOTIFIER"] = "false"
	m["GIT_CONFIG_NOSYSTEM"] = "1"
	// Node injection flags or a stale inherited Electron mode can break startup or bypass the wrapper.
	for _, k := range []string{"NODE_OPTIONS", "ELECTRON_RUN_AS_NODE", "ELECTRON_NO_ASAR", "NODE_USE_ENV_PROXY", "NODE_TLS_REJECT_UNAUTHORIZED", "WBP_PORTABLE_PROXY", "WBP_PORTABLE_PROXY_TOKEN", "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY"} {
		delete(m, k)
	}
	if bridge != "" {
		u := strings.Replace(bridge, "http://", "http://wbp:"+token+"@", 1)
		m["HTTP_PROXY"] = u
		m["HTTPS_PROXY"] = u
		m["ALL_PROXY"] = u
		m["NO_PROXY"] = "localhost,127.0.0.1,::1,[::1]"
		m["WBP_PORTABLE_PROXY"] = bridge
		m["WBP_PORTABLE_PROXY_TOKEN"] = token
	}
	var out []string
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out, nil
}

// UTF-16 conversion and quoting happen exactly once, with no cmd.exe/code page involved.
func windowsCommandLine(exe string, args []string) ([]uint16, error) {
	parts := append([]string{exe}, args...)
	for i, s := range parts {
		if !utf8.ValidString(s) || strings.ContainsRune(s, 0) {
			return nil, fmt.Errorf("子进程参数不是有效文本或包含 NUL")
		}
		parts[i] = windows.EscapeArg(s)
	}
	out, e := windows.UTF16FromString(strings.Join(parts, " "))
	if e != nil {
		return nil, e
	}
	if len(out) > 32767 {
		return nil, fmt.Errorf("子进程命令行超过 Windows 的 32767 UTF-16 单元限制")
	}
	return out, nil
}
func windowsEnvironmentBlock(input []string) ([]uint16, error) {
	env := append([]string(nil), input...)
	sort.Slice(env, func(i, j int) bool { return strings.ToUpper(env[i]) < strings.ToUpper(env[j]) })
	block := []uint16{}
	for _, v := range env {
		if !utf8.ValidString(v) || strings.ContainsRune(v, 0) {
			return nil, fmt.Errorf("子进程环境变量编码无效或包含 NUL")
		}
		key := v
		if strings.HasPrefix(key, "=") {
			key = key[1:]
		}
		if i := strings.IndexByte(key, '='); i <= 0 {
			return nil, fmt.Errorf("子进程环境变量缺少有效名称或等号")
		}
		x, e := windows.UTF16FromString(v)
		if e != nil {
			return nil, e
		}
		block = append(block, x...)
	}
	if len(block) == 0 {
		block = append(block, 0)
	}
	return append(block, 0), nil // Every block, including the empty environment, ends with two NULs.
}
