package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

type cliPatch struct{ name, hash, parameter string }

func cliPatches() []cliPatch {
	return []cliPatch{
		{"cli/dist/codebuddy-headless.js", "83d19d8462e5dfc7e6ffb02528df5d89a2a502354270ccf5f5f456a4b068cfbb", "L"},
		{"cli/dist/codebuddy-lite-wb.mjs", "921e0bb17274e77c2a0f3bb3e8c3f067d4012da8ef2c6542752044fe8b32be93", "ei"},
	}
}

// Only used on a fresh runtime stage. The CLI contains a separate resolver from
// main/proxy-agents.js; its PAC hook otherwise overrides the authenticated env URL.
func patchUnpackedCLI(asar string, tree *asarEntry) error {
	helper, e := assets.ReadFile("assets/portable-cli-proxy.cjs")
	if e != nil {
		return e
	}
	type pending struct {
		file  string
		data  []byte
		entry *asarEntry
	}
	var changes []pending
	for _, p := range cliPatches() {
		entry := asarFile(tree, p.name)
		if entry == nil || !entry.Unpacked {
			return fmt.Errorf("CLI layout changed: %s", p.name)
		}
		file := filepath.Join(asar+".unpacked", filepath.FromSlash(p.name))
		st, e := os.Lstat(file)
		if e != nil {
			return e
		}
		if !st.Mode().IsRegular() || st.Size() > 16<<20 {
			return fmt.Errorf("invalid CLI file: %s", p.name)
		}
		data, e := os.ReadFile(file)
		if e != nil {
			return e
		}
		h := sha256.Sum256(data)
		if hex.EncodeToString(h[:]) != p.hash || int64(len(data)) != entry.Size {
			return fmt.Errorf("CLI original hash/size mismatch: %s", p.name)
		}
		async := "async resolveForUrl(" + p.parameter + "){"
		sync := "resolve(){let " + p.parameter + "=this.resolveFromConfig();"
		text, e := inserts(string(data), [][2]string{
			{async, async + "if(process.env.WBP_PORTABLE_ROOT)return __wbpCLIProxy(" + p.parameter + ");"},
			{sync, "resolve(){if(process.env.WBP_PORTABLE_ROOT)return __wbpCLIProxy();let " + p.parameter + "=this.resolveFromConfig();"},
		})
		if e != nil {
			return fmt.Errorf("CLI %s: %w", p.name, e)
		}
		changes = append(changes, pending{file, append([]byte(text), helper...), entry})
	}
	for _, c := range changes {
		if e = os.WriteFile(c.file, c.data, 0600); e != nil {
			return e
		}
		c.entry.Size = int64(len(c.data))
		c.entry.Integrity = checksumEntry(c.data)
	}
	return nil
}
