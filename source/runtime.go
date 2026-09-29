package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type RuntimeRecord struct {
	EXEHash   string `json:"exe_sha256"`
	Version   string `json:"version"`
	Directory string `json:"directory"`
	SHA256    string `json:"upstream_sha256"`
	Wrapper   string `json:"wrapper"`
	ASARHash  string `json:"asar_sha256"`
}

func currentRuntime(root string) (RuntimeRecord, error) {
	var r RuntimeRecord
	b, e := os.ReadFile(filepath.Join(root, "Runtime", "current.json"))
	if e != nil {
		return r, e
	}
	if e = json.Unmarshal(b, &r); e != nil {
		return r, e
	}
	dir, e := safeChild(filepath.Join(root, "Runtime"), r.Directory)
	if e != nil {
		return r, e
	}
	if r.Version != supportedVersion || r.SHA256 != supportedSHA256 || r.Wrapper != wrapperVersion {
		return r, fmt.Errorf("运行时需要重新适配")
	}
	if st, e := os.Stat(filepath.Join(dir, "WorkBuddy.exe")); e != nil || st.Size() == 0 {
		return r, fmt.Errorf("本地 WorkBuddy.exe 缺失")
	}
	eh, ee := hashFile(filepath.Join(dir, "WorkBuddy.exe"))
	if ee != nil || eh != r.EXEHash {
		return r, fmt.Errorf("本地 WorkBuddy.exe 校验失败")
	}
	h, e := hashFile(filepath.Join(dir, "resources", "app.asar"))
	if e != nil || h != r.ASARHash {
		return r, fmt.Errorf("本地 ASAR 校验失败")
	}
	if e := validateRuntimeLayout(dir); e != nil {
		return r, e
	}
	return r, nil
}

func verifiedArchive(p string) bool {
	st, e := os.Stat(p)
	if e != nil || !st.Mode().IsRegular() || st.Size() != supportedSize {
		return false
	}
	h, e := hashFile(p)
	return e == nil && h == supportedSHA256
}
