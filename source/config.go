package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// launcherVersion tracks GUI/lifetime releases; wrapperVersion remains the
// unchanged runtime adapter compatibility version so no runtime redownload/repatch.
const launcherVersion = "1.0.3"

const wrapperVersion = "1.0.1"

type Config struct {
	ProxyMode string `json:"proxy_mode"`
	ProxyURL  string `json:"proxy_url"`
	NoProxy   string `json:"no_proxy"`
}

func readConfig(root string) (Config, error) {
	c := Config{ProxyMode: "auto"}
	b, e := os.ReadFile(filepath.Join(root, "portable.json"))
	if os.IsNotExist(e) {
		return c, nil
	}
	if e != nil {
		return c, e
	}
	b, e = decodeConfigText(b)
	if e != nil {
		return c, e
	}
	if e = json.Unmarshal(b, &c); e != nil {
		return c, fmt.Errorf("portable.json 格式错误: %w", e)
	}
	switch c.ProxyMode {
	case "auto", "direct":
	case "manual":
		if _, e = validateProxy(c.ProxyURL); e != nil {
			return c, e
		}
	default:
		return c, fmt.Errorf("proxy_mode 必须是 auto / direct / manual")
	}
	return c, nil
}
func validateProxy(s string) (*url.URL, error) {
	u, e := url.Parse(s)
	if e != nil {
		return nil, fmt.Errorf("代理 URL 无效")
	}
	if u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5" && u.Scheme != "socks5h") || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("代理应为 http://主机:端口、https://主机:端口或 socks5://主机:端口")
	}
	return u, nil
}
func safeChild(base, rel string) (string, error) {
	rel = strings.ReplaceAll(rel, "\\", "/")
	if rel == "" || strings.Contains(rel, ":") || strings.HasPrefix(rel, "/") {
		return "", fmt.Errorf("非法归档路径")
	}
	for _, s := range strings.Split(rel, "/") {
		if s == ".." {
			return "", fmt.Errorf("非法归档路径")
		}
	}
	return filepath.Join(base, filepath.FromSlash(rel)), nil
}
