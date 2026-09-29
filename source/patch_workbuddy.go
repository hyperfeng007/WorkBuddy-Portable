package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type sourcePatch struct {
	name, hash string
	transform  func(string) (string, error)
}

func replaceOnce(s, old, new string) (string, error) {
	if strings.Count(s, old) != 1 {
		return "", fmt.Errorf("patch anchor count != 1: %.100s", old)
	}
	return strings.Replace(s, old, new, 1), nil
}
func inserts(s string, m [][2]string) (string, error) {
	var e error
	for _, p := range m {
		s, e = replaceOnce(s, p[0], p[1])
		if e != nil {
			return "", e
		}
	}
	return s, nil
}
func patchMain(s string) (string, error) {
	var e error
	s, e = inserts(s, [][2]string{
		{"function repairOfficeFileAssociationsStep() {", "function repairOfficeFileAssociationsStep() {\n if(globalThis.__wbPortable) return {id: 'post-ready.repair-office-file-associations',phase:StartupPhase.PostReady,critical:false,async run(){}};"},
		{"function runEarlyPreflightAndMaybeBail() {", "function runEarlyPreflightAndMaybeBail() {\n if(globalThis.__wbPortable) return 'platform-skip';"},
		{"function applyPersistedProxyToBootstrapEnv() {", "function applyPersistedProxyToBootstrapEnv() {\n if(globalThis.__wbPortable) return 'portable';"},
		{"async function applyProxySettingsToMain(settings) {", "async function applyProxySettingsToMain(settings) {\n if(globalThis.__wbPortable) return globalThis.__wbPortable.env();"},
		{"async function configureRendererSessionProxy(deps) {", "async function configureRendererSessionProxy(deps) {\n if(globalThis.__wbPortable) {await globalThis.__wbPortable.electronReady;return;}"},
		{"if (httpProxy) deps.logInfo(`[WorkBuddy] Chromium proxy will use explicit: ${httpProxy.replace(/^https?:\\/\\//, \"\")}`);", "if (httpProxy) deps.logInfo('[WorkBuddy] Chromium proxy configured (credentials redacted)');"},
	})
	if e != nil {
		return "", e
	}
	anchor := "var UpdateServiceWin32 = class extends AbstractUpdateService {"
	start := strings.Index(s, anchor)
	if start < 0 {
		return "", fmt.Errorf("Windows updater missing")
	}
	end := strings.Index(s[start:], "\n//#endregion")
	if end < 0 {
		return "", fmt.Errorf("Windows updater boundary missing")
	}
	end += start
	region := s[start:end]
	region, e = inserts(region, [][2]string{
		{"async restoreDownloadedUpdate() {", "async restoreDownloadedUpdate() {\n if(globalThis.__wbPortable) return;"},
		{"getUpdateTargetSuffix() {", "getUpdateTargetSuffix() {\n if(globalThis.__wbPortable) return '-user';"},
		{"async checkForUpdates(explicit = false) {", "async checkForUpdates(explicit = false) {\n if(globalThis.__wbPortable) return globalThis.__wbPortable.checkUpdate(this,explicit);"},
		{"async downloadUpdate(updateInfo) {", "async downloadUpdate(updateInfo) {\n if(globalThis.__wbPortable) return globalThis.__wbPortable.checkUpdate(this,false);"},
		{"quitAndInstall() {", "quitAndInstall() {\n if(globalThis.__wbPortable) return globalThis.__wbPortable.installUpdate(this);"},
	})
	if e != nil {
		return "", e
	}
	return s[:start] + region + s[end:], nil
}
func patchProxy(s string) (string, error) {
	return inserts(s, [][2]string{
		{"async function resolveProxyForUrl(targetUrl) {", "async function resolveProxyForUrl(targetUrl) {\n if(globalThis.__wbPortable) return globalThis.__wbPortable.resolveProxyForUrl(targetUrl);"},
		{"function installUndiciProxyDispatcher() {", "function installUndiciProxyDispatcher() {\n if(globalThis.__wbPortable) return globalThis.__wbPortable.installDispatcher();"},
	})
}
func patchServer(s string) (string, error) {
	anchor := "var ProxySettingsService = class {"
	start := strings.Index(s, anchor)
	if start < 0 {
		return "", fmt.Errorf("proxy settings service missing")
	}
	end := strings.Index(s[start:], "\n//#endregion")
	if end < 0 {
		return "", fmt.Errorf("proxy settings boundary missing")
	}
	end += start
	r, e := inserts(s[start:end], [][2]string{
		{"async getSettings() {", "async getSettings() {\n if(globalThis.__wbPortable) return {mode:'system'};"},
		{"async saveSettings(settings) {", "async saveSettings(settings) {\n if(globalThis.__wbPortable) return {ok:false,errorCode:'PORTABLE_MANAGED',error:'便携版网络由启动器统一管理。请在托盘“网络设置”修改并重启，不要在应用内另设代理。'};"},
	})
	if e != nil {
		return "", e
	}
	return s[:start] + r + s[end:], nil
}
func workbuddyPatches() []sourcePatch {
	return []sourcePatch{
		{"main/index.js", "d78b2a67116d47629a7433630d3728dfb46b90ecf38545d1f808f4810efb56db", patchMain},
		{"main/daemon-app-server-entry.js", "d0c7112f78ae9e550d9a4f0533184124236f4c2b18bf771912f30038563025d2", func(s string) (string, error) { return "require('../portable-node.cjs').install('daemon');\n" + s, nil }},
		{"main/proxy-agents.js", "15b0d002521ab301962267edeff63a2e3dc9b921af36f6c695a53df7d9bce027", patchProxy},
		{"main/server.js", "ba99f1dc4ca233d25d5cb1f68273b1c07e3fce96b151bed0f524ba5adcf1fef6", patchServer},
	}
}
func patchAsar(file string) error {
	f, e := os.Open(file)
	if e != nil {
		return e
	}
	defer f.Close()
	tree, base, e := readAsarHeader(f)
	if e != nil {
		return e
	}
	b, e := readAsarFile(f, tree, base, "package.json")
	if e != nil {
		return e
	}
	var pkg map[string]any
	if e = json.Unmarshal(b, &pkg); e != nil {
		return e
	}
	if pkg["name"] != "@genie/workbuddy-desktop" || pkg["version"] != "5.6.2" || pkg["main"] != "main/index.js" {
		return fmt.Errorf("非已审核的 WorkBuddy 5.6.2 ASAR，未修改")
	}
	var payloads []asarPayload
	for _, p := range workbuddyPatches() {
		original, e := readAsarFile(f, tree, base, p.name)
		if e != nil {
			return e
		}
		h := sha256.Sum256(original)
		if hex.EncodeToString(h[:]) != p.hash {
			return fmt.Errorf("%s 原始哈希不符，拒绝适配", p.name)
		}
		text, e := p.transform(string(original))
		if e != nil {
			return fmt.Errorf("%s: %w", p.name, e)
		}
		payloads = append(payloads, asarPayload{p.name, []byte(text)})
	}
	for _, name := range []string{"portable.mjs", "portable-node.cjs", "portable-network.mjs", "portable-update.mjs"} {
		if asarFile(tree, name) != nil {
			return fmt.Errorf("原包意外包含便携文件")
		}
		b, e := assets.ReadFile("assets/" + name)
		if e != nil {
			return e
		}
		payloads = append(payloads, asarPayload{name, b})
	}
	pkg["main"] = "portable.mjs"
	pkg["workbuddyPortableWrapper"] = wrapperVersion
	pkg["workbuddyPortableOriginalVersion"] = supportedVersion
	b, e = json.MarshalIndent(pkg, "", "  ")
	if e != nil {
		return e
	}
	payloads = append(payloads, asarPayload{"package.json", b})
	if e = patchUnpackedCLI(file, tree); e != nil {
		return e
	}
	return writeAsarPayloads(file, f, tree, base, payloads)
}
