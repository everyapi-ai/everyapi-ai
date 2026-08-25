# Security policy — `everyapi` CLI

## 报告漏洞 / Reporting a vulnerability

请**不要**在公开 GitHub Issue 里发漏洞细节。两种私有渠道任选其一：

- **Email**: `security@everyapi.ai`（PGP key fingerprint 列在 [https://everyapi.ai/.well-known/security.txt](https://everyapi.ai/.well-known/security.txt)，邮件可加密）
- **GitHub 私有漏洞报告**: <https://github.com/everyapi-ai/everyapi/security/advisories/new>

报告时请尽量包含：
- 受影响版本（`everyapi version` 输出）
- 复现步骤（最小化 PoC）
- 你认为的影响范围（信息泄漏 / 远程代码执行 / 钱路 / 权限提升等）
- 你希望被公开致谢的姓名（可选）

## 支持的版本 / Supported versions

我们只接受**最新 minor release 序列**的安全报告：

| 版本 | 支持状态 |
|---|---|
| `v0.1.x`（current） | ✅ 安全修复 + 功能更新 |
| `< v0.1.0` | ❌ 请先升级到 latest |

## SLA / 响应时效

| 严重度 | 首次确认 | 修复 + 公开 advisory |
|---|---|---|
| Critical（钱路 / 权限提升 / 任意代码执行） | 24 小时 | 7 天内 |
| High（凭证泄漏 / 鉴权绕过） | 3 天 | 30 天内 |
| Medium / Low | 7 天 | 下个 minor release |

## 已知风险面 / Threat model

CLI 的设计威胁模型（这些**不是**漏洞，而是设计权衡）：

1. **`~/.config/everyapi/credentials.json` 明文存 token** — 文件 mode `0600`，但任何能读该文件的本机进程都能以你的身份调用 EveryAPI API。与 `gh auth` / `aws configure` 等业界 CLI 相同。Keychain / DPAPI / Secret Service backend 路线图中，但尚未上。**对策**：在不信任的机器上不要 `everyapi login`；怀疑泄漏立即 `everyapi logout` + 在 dashboard rotate API key。
2. **默认 `everyapi use <tool>` 可能把 token 通过 env 或生命周期绑定的临时配置交给子进程** — 第三方 CLI 的 debug 日志可能落档 env。**对策**：分享 debug log 前 redact `*_TOKEN` / `*_API_KEY`；或在支持的客户端使用 `--transparent`，让真实 relay key 只留在 EveryAPI 父进程内存。临时配置位于 `~/.config/everyapi/sessions`、权限 0600/0700，子进程退出后删除；同一 OS 用户下的恶意进程仍不是受信沙箱。
3. **`--transparent` 在本机终止目标域名 TLS** — Connector 能看到解密后的模型请求和响应；客户端也可检测代理变量和临时 CA。CA 私钥每次启动生成、只存在内存且不上传，公开 CA bundle 在退出时删除，监听地址强制为 loopback，未知模型路径 fail closed。**对策**：只在可信设备使用；不要把“保留官方 origin”理解为客户端到供应商的端到端 TLS，也不要宣称其不可检测。
4. **MCP server 凭据继承** — `everyapi mcp` 读同一份 credentials 文件，自动跑所有 tool。任何能 spawn `everyapi mcp` 的本机进程（恶意 MCP host、被入侵的 AI agent）都拥有相同权限。**对策**：只在信任的 MCP host 里配 `everyapi`；钱路工具 `everyapi_seller_withdraw` 已加 `confirm: "yes"` 必填字段做 friction step。
5. **Release 二进制无 OS 级 code signing** — macOS Gatekeeper / Windows SmartScreen 会拦。**对策**：每个 release 附带 `SHA256SUMS` + sigstore cosign keyless 签名（`SHA256SUMS.sig` + `SHA256SUMS.pem`），证明 `SHA256SUMS` 是由 `everyapi-ai/everyapi` 仓的 `cli-release.yml` workflow 在 GitHub Actions 上产生的。务必按 README §安装 部分给的命令走完两层校验；不要绕过 OS 警告直接运行未校验的二进制。OS notarization 路线图中。
6. **`everyapi computer` 拥有本机 GUI 读写能力** — macOS Accessibility 可读取窗口控件并注入点击、键盘、滚动和拖拽；同一 OS 用户下的其他进程也能读取命令生成的缓存文件。该功能不会绕过 macOS TCC，也不通过 MCP 暴露；只有用户点击 Connect 的“授权”或显式运行 `permissions --request` 时，签名辅助 app 才调用 Apple 的请求 API 登记自身并显示系统同意流程，最终授权仍只能由用户完成。实际调用发生在独立签名的 `EveryAPI Computer Use.app`（源码在 `clients/desktop/native/computer-use-macos`）里，CLI 首次使用时静默下载、校验、拉起它，并通过带 token 鉴权的本机 Unix socket 通信——权限绑定这个专用 bundle identity，不会波及所有 AppleScript/JXA。下载资产用独立 `.sha256` 校验；正式发布走 Apple Developer ID 签名与 notarize，缺失密钥时退化为 ad-hoc 并显式告警。截图仅通过真实窗口 id 捕获目标窗口自身像素，不使用可能泄露遮挡应用内容的屏幕区域替代方案。快照两分钟后失效，缓存目录为 0700、文件为 0600，观测文本会去除控制序列并遮蔽凭据，出站文本会拒绝检测到的凭据，辅助 app 内部串行化操作。公开 Accessibility 属性无法区分属性完全相同的替换对象，因此 fingerprint 是变化检测器而非唯一实例 ID；其他本机进程和用户仍可能在校验后改变焦点。终端、密码管理器、Keychain Access、Passwords、System Settings 和 EveryAPI Connect 的 bundle ID 拒绝列表只是 defense-in-depth friction，不是完整分类器。**对策**：只操作明确且可信的 `--app`；仅向 “EveryAPI Computer Use” 授权；不需要时在 System Settings 撤销；不要同步或分享缓存；遇到 `action_outcome_unknown` 时先刷新，不要盲目重试。

## 范围外 / Out of scope

以下不在本 CLI 的 security 范围内：

- 后端 / 服务端漏洞（EveryAPI API / dashboard / 计费 / 网关）——请到 [everyapi-ai 主仓的 SECURITY 流程](https://everyapi.ai/security) 走
- 第三方依赖：本 CLI **没有任何**第三方 Go 依赖（`go.mod` 只列 `go 1.25.1`），但 release 二进制由 Go runtime 静态链接 — Go runtime 自身的 CVE 由我们升级 Go 版本响应
- 用户自托管部署：自己改了 `--api-base` 指向私有后端时，私有后端的安全责任在你

## 致谢

修复后我们会在 release notes 里致谢报告者（如同意）。Hall of Fame 列在
<https://everyapi.ai/security/hall-of-fame>。
