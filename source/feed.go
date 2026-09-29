package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
)

const officialFeed = "https://www.workbuddy.cn/v2/update?platform=workbuddy-win32-x64-user"
const supportedVersion = "5.6.2.39298511"
const supportedURL = "https://download.codebuddy.cn/workbuddy/saas/win32-x64-user/WorkBuddy-win32-x64-user-5.6.2.39298511-37a65c0b.exe"
const supportedSHA256 = "627e5a565436d0876740af69c2747759648662c52958d2a5df1ba330a82c3025"
const supportedSize int64 = 530927840

// The official feed currently omits its hash. This reviewed, Authenticode-verified
// original is pinned instead of accepting an arbitrary changing executable.
type Feed struct {
	Version   string
	URL       string
	SHA256    string
	Size      int64
	Supported bool
}

func parseFeed(raw []byte) (Feed, error) {
	var j struct {
		Version        string `json:"version"`
		ProductVersion string `json:"productVersion"`
		URL            string `json:"url"`
		Hash           string `json:"sha256hash"`
	}
	if e := json.Unmarshal(raw, &j); e != nil {
		return Feed{}, fmt.Errorf("WorkBuddy 官方更新元数据不是有效 JSON")
	}
	if !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$`).MatchString(j.Version) || j.ProductVersion != j.Version {
		return Feed{}, fmt.Errorf("WorkBuddy 官方版本结构变化，请更新便携启动器")
	}
	u, e := url.Parse(j.URL)
	if e != nil || u.Scheme != "https" || u.Host != "download.codebuddy.cn" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !regexp.MustCompile(`^/workbuddy/saas/win32-x64-user/WorkBuddy-win32-x64-user-`+regexp.QuoteMeta(j.Version)+`-[a-f0-9]{8}\.exe$`).MatchString(u.Path) {
		return Feed{}, fmt.Errorf("拒绝非预期的 WorkBuddy 国内版 x64 下载地址")
	}
	f := Feed{Version: j.Version, URL: j.URL}
	if f.Version == supportedVersion && f.URL == supportedURL {
		if j.Hash != "" && j.Hash != supportedSHA256 {
			return Feed{}, fmt.Errorf("官方校验值与已审核构建不同，请更新便携启动器")
		}
		f.SHA256 = supportedSHA256
		f.Size = supportedSize
		f.Supported = true
	}
	return f, nil
}
func allowedOfficialURL(raw string) bool {
	u, e := url.Parse(raw)
	return e == nil && u.Scheme == "https" && u.User == nil && (u.Host == "www.workbuddy.cn" || u.Host == "download.codebuddy.cn")
}
