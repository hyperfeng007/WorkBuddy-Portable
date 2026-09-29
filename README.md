# WorkBuddy-Portable

腾讯 WorkBuddy 国内版（workbuddy.cn）的 Windows 社区便携启动器，不是 workbuddy.ai 国际版。

**Windows 10/11 x64 · 启动器 1.0.3 · 社区启动器源码 MIT · 非官方便携发行版**

> 本仓库公开完整社区启动器源码、便携适配代码、测试、构建资源、第三方工具与许可证。**MIT 不会把官方应用或第三方组件重新授权为本项目代码。** 官方运行时按其原有条款从官方渠道准备，不在本仓库/发行 ZIP 内重分发。

## 下载

- **[最新版完整便携启动器 ZIP](https://github.com/hyperfeng007/WorkBuddy-Portable/releases/download/v1.0.3/WorkBuddy-Portable-Windows-x64-v1.0.3.zip)**（约 4.18 MiB）
- [发行页、独立升级 EXE 与校验文件](https://github.com/hyperfeng007/WorkBuddy-Portable/releases/tag/v1.0.3)
- [ZIP SHA-256](https://github.com/hyperfeng007/WorkBuddy-Portable/releases/download/v1.0.3/WorkBuddy-Portable-Windows-x64-v1.0.3.zip.sha256)

这是完整**启动器**包，不是预装离线官方应用。GitHub 的 **Code → Download ZIP** 是源码快照，不是可直接双击的发行包；普通使用请下载上面的 Release ZIP。首次没有运行时时仍需联网准备。

## 使用

1. 完整解压 Release ZIP 到可写磁盘/U 盘，不在压缩软件内运行。
2. 双击 `WorkBuddy-Portable.exe`，按提示准备官方桌面；不要求预装 Go/Node/Python。
3. 代理默认自动检测：环境变量及 Windows 系统代理/PAC。需要手动设置时运行 `Network-Settings.cmd`。浏览器扩展专用代理不保证可自动读取。
4. 官方主界面就绪后，启动窗口真正销毁，不保留启动器托盘；必要的无界面桥接和监管在应用运行期间保留。
5. 用应用自己的菜单/官方托盘**真正退出**，等待本次受管进程及启动器结束，再用 Windows 安全删除硬件。最小化/隐藏不是退出。

当前为紧凑界面：正文 18、标题 24、辅助 14 逻辑像素；100% 下窗口约 670×400，本工具缩放上限 125%。

完整使用说明与限制：[使用说明.md](使用说明.md)。另一款独立项目：[hyperfeng007/DSH-Desktop-Portable](https://github.com/hyperfeng007/DSH-Desktop-Portable)。

## 已有目录覆盖升级

先彻底退出并备份，只替换原目录的 `WorkBuddy-Portable.exe`；保留 **Data、Runtime、tools、Workspace、portable.json**。没有 `Network-Settings.cmd` 时从完整包补入。

当前启动器版本 **1.0.3**，适配兼容版本仍为 **1.0.1**；`Runtime/current.json` 的 wrapper 不随 UI 更新变化是正常现象。有效的该适配运行时不会因本次字号/窗口修改重新下载或重新适配。更早版本按完整使用说明中的迁移规则处理。

## 源码结构

| 路径 | 内容 |
|---|---|
| `source/` | 完整 Go 启动器源码、依赖锁定、构建脚本 |
| `source/assets/` | 便携启动、代理与更新适配模块 |
| `source/tests/`、`source/*_test.go` | 自动回归与原生 Windows 测试 |
| `source/winres/`、`source/*.syso` | 图标、Windows 清单、版本与预生成资源 |
| `tools/` | 原版 7-Zip 工具及许可证/对应源代码说明 |
| `docs/` | 技术、安全边界、已标明时间与范围的测试记录 |
| `releases/v1.0.3/` | 原始发行包清单、哈希与归档元数据 |

根目录 EXE 与 ZIP 放在 Releases，不重复加入 Git 历史。原始发行 ZIP 和 EXE 保持字节不变。公开归档只增加仓库说明/忽略规则，不改变生产源码。

## 从源码构建

需要 **Go 1.26.1**（原发行构建版本）。在 Windows PowerShell、仓库根目录运行：

```powershell
.\source\build.ps1
```

Linux/macOS 交叉编译：

```sh
sh source/build.sh
```

输出根目录 `WorkBuddy-Portable.exe`。运行时准备还需要仓库内的 `tools/`。资源已预生成；修改资源后可在 `source/` 用 `go run github.com/tc-hib/go-winres@v0.3.3 make --arch amd64` 重建。

测试：在 `source/` 中运行 `go test -race -count=1 -v ./...`；部分集成测试需要原官方 runtime/Node fixture，未设置时会明确 SKIP。

## 验证状态与限制

- 最新发行已执行的 Go/静态检查/编译及一致性结果：[测试与验收](docs/测试与验收.md)。仓库发布本身没有重新执行所有测试。
- **Windows 原生 GUI/Job 测试已编译但未在 Windows 真机执行**，不把交叉编译、历史测试或 mock 当成真实账号任务验收。
- 当前审核官方构建：**国内版 5.6.2.39298511**。不承诺任意未来上游构建都能被适配。
- 不是零痕迹沙箱，也不是全系统 VPN。不绕过公司网络、安全策略、官方账号/许可或强制升级规则。
- 只回收本次 Job 管理的进程，不按名称扫杀独立实例；系统服务/提权 broker 等不保证纳入。强拔盘/断电不能保证清理或数据安全。
- 启动器及适配副本未做官方代码签名；不要为了运行它关闭安全软件。

## 许可证和隐私

社区启动器：[MIT LICENSE](LICENSE)。第三方组件及官方程序仍遵守各自条款，见 [THIRD-PARTY-NOTICES.md](THIRD-PARTY-NOTICES.md)。

没有上传令牌、账号配置、会话、个人 Runtime/Data 或用户原始故障日志。`docs/` 中的是审查后的构建/受控测试记录；测试代码里的 `user:pass`、`wbp:token` 等为假凭据。提交 Issue 前仍应自行脱敏，参见 [SECURITY.md](SECURITY.md)。
