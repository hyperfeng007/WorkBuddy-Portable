package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"golang.org/x/sys/windows"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

func checkTools(root string) error {
	for name, want := range map[string]string{"7z.exe": "4a41aa37786c7eae7451e81c2c97458d5d1ae5a3a8154637a0d5f77adc05e619", "7z.dll": "bbd705e3b58ca7677c1e9e67473f166a6712da034dcb567d571fbb67507a443f"} {
		h, e := hashFile(filepath.Join(root, "tools", name))
		if e != nil || h != want {
			return fmt.Errorf("tools/%s 缺失或校验失败，请完整解压便携 ZIP", name)
		}
	}
	return nil
}
func obtainRuntime(ctx context.Context, root string, client *http.Client, j *Job, env []string, force bool) (RuntimeRecord, error) {
	status("检查本地 WorkBuddy", "校验本地运行时；已准备的同版本优先复用。", -1)
	old, oldErr := currentRuntime(root)
	if oldErr == nil && !force {
		return old, nil
	}
	status("01 / 05 · 检查本地原始包/官方构建", "修复优先校验并复用本地已审核缓存；无有效缓存时再查官网。", -1)
	cachedArchive := filepath.Join(root, "Runtime", "packages", supportedSHA256+".exe")
	f, e := feedForPreparation(ctx, client, cachedArchive, force)
	if e != nil {
		return old, e
	}
	if !f.Supported {
		return old, fmt.Errorf("官方当前为 %s，本便携版已审核 %s。新构建尚未完成兼容检查；未下载、未安装，请更新便携启动器", f.Version, supportedVersion)
	}
	if oldErr == nil && old.Version == f.Version && old.SHA256 == f.SHA256 {
		status("已是官方当前构建", "复用本地运行时，不下载。", 100)
		return old, nil
	}
	if e = checkTools(root); e != nil {
		return old, e
	}
	var free uint64
	if e = windows.GetDiskFreeSpaceEx(wstr(root), &free, nil, nil); e != nil {
		return old, e
	}
	if free < 6<<30 {
		return old, fmt.Errorf("首次准备/重新适配建议并要求至少 6 GB 可用空间，当前 %.1f GB", float64(free)/(1<<30))
	}
	if !confirm("WorkBuddy 社区便携版 · 首次/重新准备", "便携适配需要修改脚本与 EXE 中的完整性摘要，适配后的 WorkBuddy.exe 不再具有腾讯原始签名。原始安装包缓存不变，完整性校验、登录和凭据保护不关闭。\n\n本修正版尚未完成 Windows 账号登录与实际任务验收，建议先备份资料。是否继续准备？") {
		return old, fmt.Errorf("已取消便携准备，未运行安装器")
	}
	cache := filepath.Join(root, "Runtime", "packages")
	if e = os.MkdirAll(cache, 0700); e != nil {
		return old, e
	}
	archive := filepath.Join(cache, f.SHA256+".exe")
	status("02 / 05 · 检查本地安装包缓存", "已有经过校验的原始安装包时，只在本地重新解包。", -1)
	if !verifiedArchive(archive) {
		if _, e = os.Stat(filepath.Join(root, "Runtime", "current.json")); e == nil && !confirm("需要重新准备 WorkBuddy", "本地运行时或安装包缓存不能直接复用。是否下载官方原始包重新准备？Data 和旧运行时不删除。") {
			return old, fmt.Errorf("已取消下载，原数据保留")
		}
		status("02 / 05 · 下载 WorkBuddy 官方安装包", fmt.Sprintf("国内版 %s · %.0f MB · 不运行安装器", f.Version, float64(f.Size)/1e6), 0)
		tmp := archive + ".download"
		defer os.Remove(tmp)
		if e = download(ctx, client, f.URL, tmp, f.Size, f.SHA256); e != nil {
			return old, e
		}
		os.Remove(archive)
		if e = os.Rename(tmp, archive); e != nil {
			return old, e
		}
	} else {
		status("02 / 05 · 复用已下载的官方包", "SHA-256 校验通过，不重新下载。", 100)
	}
	if e = ctx.Err(); e != nil {
		return old, e
	}
	work, e := os.MkdirTemp(filepath.Join(root, "Runtime"), ".prepare-")
	if e != nil {
		return old, e
	}
	defer os.RemoveAll(work)
	status("03 / 05 · 解包官方运行时", "提取两层安装包；不执行 NSIS，不创建安装项。", -1)
	extractor := filepath.Join(root, "tools", "7z.exe")
	log := filepath.Join(root, "Data", "Logs", "extract.log")
	run := func(args ...string) error {
		if e := ctx.Err(); e != nil {
			return e
		}
		c, e := j.Start(extractor, args, root, env, log)
		if e != nil {
			return e
		}
		return c.Wait()
	}
	installer := filepath.Join(work, "installer")
	if e = run("x", archive, "-o"+installer, "-y", "-bd", "-bso0", "-bsp0", "$PLUGINSDIR/app-64.7z"); e != nil {
		return old, e
	}
	app := filepath.Join(work, "app")
	if e = run("x", filepath.Join(installer, "$PLUGINSDIR", "app-64.7z"), "-o"+app, "-y", "-bd", "-bso0", "-bsp0"); e != nil {
		return old, e
	}
	if e = validateRuntimeLayout(app); e != nil {
		return old, e
	}
	status("04 / 05 · 应用便携适配", "重定向目录、统一代理、委派更新，禁止自动注册文件关联和自启。", -1)
	if e = patchAsar(filepath.Join(app, "resources", "app.asar")); e != nil {
		return old, fmt.Errorf("WorkBuddy 结构验证/适配失败，旧版本不动: %w", e)
	}
	if e = adaptExecutable(filepath.Join(app, "WorkBuddy.exe"), filepath.Join(app, "resources", "app.asar")); e != nil {
		return old, e
	}
	exeHash, e := hashFile(filepath.Join(app, "WorkBuddy.exe"))
	if e != nil {
		return old, e
	}
	asarHash, e := hashFile(filepath.Join(app, "resources", "app.asar"))
	if e != nil {
		return old, e
	}
	if e = ctx.Err(); e != nil {
		return old, e
	}
	dir := "workbuddy-" + f.Version + "-" + wrapperVersion + "-" + time.Now().UTC().Format("20060102T150405")
	dest := filepath.Join(root, "Runtime", dir)
	if e = os.Rename(app, dest); e != nil {
		return old, e
	}
	r := RuntimeRecord{Version: f.Version, Directory: dir, SHA256: f.SHA256, Wrapper: wrapperVersion, ASARHash: asarHash, EXEHash: exeHash}
	if e = commitRuntimePointer(root, r); e != nil {
		return old, e
	}
	return r, nil
}
func download(ctx context.Context, c *http.Client, u, dest string, size int64, want string) error {
	if !allowedOfficialURL(u) {
		return fmt.Errorf("非官方下载地址")
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", u, nil)
	resp, e := c.Do(req)
	if e != nil {
		return fmt.Errorf("下载失败，请检查便携代理配置")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("下载返回 HTTP %d", resp.StatusCode)
	}
	f, e := os.OpenFile(dest, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	h := sha256.New()
	reader := io.LimitReader(resp.Body, size+1)
	buf := make([]byte, 256*1024)
	var n int64
	last := time.Time{}
	for {
		if e = ctx.Err(); e != nil {
			return e
		}
		m, err := reader.Read(buf)
		if m > 0 {
			if _, e = f.Write(buf[:m]); e != nil {
				return e
			}
			h.Write(buf[:m])
			n += int64(m)
			if time.Since(last) > 250*time.Millisecond {
				status("02 / 05 · 下载官方桌面程序", fmt.Sprintf("%.1f / %.1f MB · SHA-256 完整校验后才解包", float64(n)/1e6, float64(size)/1e6), int(100*n/size))
				last = time.Now()
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	if n != size || hex.EncodeToString(h.Sum(nil)) != want {
		return fmt.Errorf("下载大小或 SHA-256 不匹配，未解包、未执行")
	}
	return f.Sync()
}
func commitRuntimePointer(root string, r RuntimeRecord) error {
	p := filepath.Join(root, "Runtime", "current.json")
	if b, e := os.ReadFile(p); e == nil {
		if e = atomicRuntimeFile(filepath.Join(root, "Runtime", "previous.json"), b); e != nil {
			return e
		}
	}
	b, e := json.MarshalIndent(r, "", "  ")
	if e != nil {
		return e
	}
	return atomicRuntimeFile(p, b)
}
func atomicRuntimeFile(p string, b []byte) error {
	f, e := os.CreateTemp(filepath.Dir(p), ".pointer-")
	if e != nil {
		return e
	}
	name := f.Name()
	defer os.Remove(name)
	if _, e = f.Write(b); e == nil {
		e = f.Sync()
	}
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return windows.MoveFileEx(wstr(name), wstr(p), windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}
