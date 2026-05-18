# `relaya` CLI

[Relaya](https://relaya.pro) AI API 网关的 buyer onboarding CLI。让任何 Claude Code / Codex / Gemini CLI **一分钟内**接到网关。

状态：**v1 buyer onboarding** + **seller plain-key 子命令**已落地。Sanitizer proxy、OAuth seller 流、QR sign-in 等后续 PR 增量补齐（见文末「不在范围」段）。

## 安装

当前要么 clone + 自行 build，要么等 `v*` tag 后从 GitHub Release 拉二进制。

```bash
git clone https://github.com/relaya-ai/relaya-ai
cd relaya-ai
go build -o relaya .
```

或者直接走 Go install：

```bash
go install github.com/relaya-ai/relaya-ai@latest
# 二进制落在 $(go env GOPATH)/bin/relaya
```

或者 Homebrew tap（待 v* tag 推出）：

```bash
# 首次安装
brew tap relaya-ai/tap         # 一次性挂上 tap
brew install relaya

# 之后升级 — 注意必须先 `brew update`
brew update                    # 刷新 tap formula 拿到最新版本号
brew upgrade relaya
```

> ⚠️ **必须先 `brew update`**：单跑 `brew upgrade relaya` 用的是本地缓存的 formula，会显示「already installed」即使 release 已经有新版。`brew update` 拉 tap repo 最新 formula 再 upgrade 才能跨版本。
>
> Homebrew 短语法 `brew install relaya-ai/tap/relaya` 同样能用——它内部会替你 tap，但显式 `tap` 一次能让后续 `brew upgrade` / `brew uninstall` 不必每次都写完整 path。

### 从 GitHub Release 拉二进制 — 务必校验

V1 还没做 OS 级 code signing（macOS notarization / Windows Authenticode），但**每次 release 都附带 `SHA256SUMS` + sigstore cosign 签名**。前者抓换包、后者抓「checksum 文件本身被换」。**下载二进制后务必走完两层校验**：

#### 第一层：SHA256 校验文件完整性

```bash
# Linux / macOS
curl -LO https://github.com/relaya-ai/relaya-ai/releases/download/v0.1.0/relaya_linux_amd64.tar.gz
curl -LO https://github.com/relaya-ai/relaya-ai/releases/download/v0.1.0/SHA256SUMS
sha256sum -c SHA256SUMS --ignore-missing
# relaya_linux_amd64.tar.gz: OK

# Windows PowerShell
$expected = (Get-Content SHA256SUMS | Select-String "relaya_windows_amd64.zip").ToString().Split()[0]
$actual = (Get-FileHash relaya_windows_amd64.zip -Algorithm SHA256).Hash.ToLower()
if ($expected -ne $actual) { throw "checksum mismatch" } else { "OK" }
```

#### 第二层：cosign 验证 SHA256SUMS 来自正版 release pipeline

如果攻击者能换 release 资产，他也能换 `SHA256SUMS`。所以再用 [cosign](https://github.com/sigstore/cosign) 通过 sigstore Fulcio 证书链验证 `SHA256SUMS` 是由 `relaya-ai/relaya` 的 `cli-release.yml` workflow 产生的：

```bash
curl -LO https://github.com/relaya-ai/relaya-ai/releases/download/v0.1.0/SHA256SUMS.sig
curl -LO https://github.com/relaya-ai/relaya-ai/releases/download/v0.1.0/SHA256SUMS.pem

cosign verify-blob \
  --certificate-identity-regexp '^https://github.com/relaya-ai/relaya/\.github/workflows/cli-release\.yml@refs/heads/main$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  --cert SHA256SUMS.pem \
  --signature SHA256SUMS.sig \
  SHA256SUMS
# Verified OK
```

如果**任一层失败**——`SHA256SUMS: FAILED` 或 cosign 输出非 `Verified OK`——**不要解压、不要运行**，到 Issue tracker 报告 + 检查你的下载源是否被劫持。

## 命令

```
relaya login              当前设备登录 Relaya
relaya logout             清除本设备凭证
relaya status             查看余额、使用量、配额
relaya use <tool>         配好 env 并 exec 进第三方 CLI（指向 Relaya）
relaya seller <sub>       Marketplace 卖家端命令（list / withdraw / add-key / setup）
relaya mcp                作为 MCP server 跑（stdin/stdout JSON-RPC）
relaya update             检查是否有新版本，打印对应安装方式的升级命令
relaya version            显示构建版本
relaya help               帮助
```

### `relaya use <tool>`

装这个 CLI 的主要理由。它把目标第三方工具的环境变量按惯例配置好，然后 exec 进去——你既有的 Claude Code / OpenAI Codex CLI / Gemini CLI **不用改任何配置**就指向了 Relaya 网关。

```bash
relaya use claude         # Claude Code → Relaya
relaya use codex          # OpenAI Codex CLI → Relaya
relaya use gemini         # Gemini CLI → Relaya
relaya use                # 无参 → 交互式选择已安装的工具
```

每个工具的 env 惯例各不相同，CLI 帮你记住：

| 工具 | 设置的环境变量 |
|---|---|
| claude | `ANTHROPIC_BASE_URL`、`ANTHROPIC_AUTH_TOKEN` |
| codex | `OPENAI_BASE_URL`、`OPENAI_API_KEY` |
| gemini | `GEMINI_API_KEY`、`GOOGLE_GEMINI_BASE_URL` |

不必再去查每个工具读哪个变量名、要不要拼 `/v1` 后缀、走哪种 auth header。

> ⚠️ **Subprocess env 安全提示**：上面这些环境变量包含你的 relay API key。第三方 CLI 的 debug / verbose 模式可能会把 env 写进日志——`relaya use` 之前确认你打开的 debug flag 不会泄漏 `*_TOKEN` / `*_API_KEY`；分享 debug 日志前先 `sed -i 's/sk-relaya-[A-Za-z0-9]*/REDACTED/g'`。

### `relaya login`

走 Device Authorization Grant（RFC 8628 形态）：

1. CLI 创建一个 session，打印一个短码 + URL。
2. 在已登录 Relaya 的浏览器里打开 URL、粘贴短码、点 Approve。
3. CLI 拿到 access token，落地到 `~/.config/relaya/credentials.json`（mode 0600）。

```bash
relaya login                                    # 生产
relaya login --api-base http://localhost:3000   # 本地开发 / 自托管
relaya login --no-browser                       # 不自动开浏览器
```

### `relaya seller …`

卖家端子命令——把 dashboard 的 channel mount / 提现操作搬到 terminal，方便 scripted onboarding。挂渠道前 `seller setup` 会先查 eligibility（账号激活 / 邮箱验证 / 账号年龄 / 消费记录 / channel 上限），失败的 gate 在**输入 key 之前**就先列出来，避免用户填一通才发现提交侧 422。

```
relaya seller list                          # 列出已挂载的 channel
relaya seller withdraw                      # 把全部 pending seller 收入转入主余额
relaya seller withdraw --quota 1000         # 部分转账（DB 单位）
relaya seller add-key   --type claude --name 'my-pro' \
                        --key 'sk-ant-...' --models 'claude-3-opus,claude-3-sonnet'
relaya seller setup                         # 交互式 wizard：先查 eligibility，再引导 add-key
```

#### `add-key` 多 key 备份池

`--key` 可以重复，把 N 把等价凭证挂在同一个 channel 上作为 backup pool（B2，PRODUCT §4.5）；当主 key 401/403 时后端自动 failover 到下一把。`--key-remark` 同样可重复，按位置跟 `--key` 对齐（第 i 个 `--key-remark` 是第 i 个 `--key` 的标签，便于以后 dashboard 上识别）。OAuth blob 不能进 backup pool —— 仍只能作为单 key channel。

```
relaya seller add-key   --type claude --name 'claude-pool' \
                        --models 'claude-3-opus' \
                        --key 'sk-ant-primary' --key-remark 'primary' \
                        --key 'sk-ant-backup'  --key-remark 'team backup'
```

`add-key` 的 `--type` 接受 alias（`openai` / `claude` / `gemini` / `codex` / `vertex` / `aws` / `xai` / `deepseek`）或数字 id。挂载受 marketplace eligibility 限制（账号激活、邮箱已验证、消费记录、channel 数上限），任何 gate 失败时后端会返回明确 message，CLI 原样透传。

OAuth flow（`seller add-oauth claude/chatgpt`）尚未实现，等独立 PR。

### `relaya status`

```
$ relaya status

  alice (alice@example.com)
  quota:     $12.34 remaining   $5.67 used
  requests:  1,234
  topup:     https://app.relaya.pro/wallet
```

### `relaya update`

查 GitHub mirror 最新 release，跟当前版本比对，**自动跑对应安装方式的升级命令**——一条命令搞定，不用复制粘贴。

检测逻辑（基于 `os.Executable()` resolve symlink 后的真实路径）：

| 路径包含 | 检测为 | 自动执行 |
|---|---|---|
| `/Cellar/` | Homebrew | `brew update && brew upgrade relaya`（两步分开跑） |
| `$GOBIN` 或 `$GOPATH/bin` 或 `$HOME/go/bin` | `go install` | `go install github.com/relaya-ai/relaya-ai@latest` |
| 其它（curl / 手工放进 PATH） | unknown | 打印手动命令 + 提醒走 SHA256 + cosign 验证（无法安全自动替换二进制） |

```bash
$ relaya update

Update available: v0.1.0 → v0.2.0
Install method:   Homebrew

$ brew update
==> Updated Homebrew from ...

$ brew upgrade relaya
==> Upgrading relaya-ai/tap/relaya
  v0.1.0 -> v0.2.0
...

Done. Run `relaya version` to confirm.
```

为什么不直接换二进制？三个原因：（1）brew / go 自己的校验链（SHA / module checksum）比我们自己在 CLI 内重做的更扎实；（2）自替换 running executable 在 Windows 平台基本是地雷区；（3）保留 README 推荐的 SHA256 + cosign 双层校验流程作为 unknown 路径下的明确 fallback。

Flag：
- `--check` —— 静默对比，已最新 exit 0 / 过时 exit 1。给 CI / cron 用：
  ```bash
  relaya update --check || echo "needs upgrade"
  ```
- `--dry-run` —— 打印将要跑的命令但不实际执行，做 inspection 用

## 配置文件

凭证落在 `~/.config/relaya/credentials.json`（若设置了 `$XDG_CONFIG_HOME` 则走 `$XDG_CONFIG_HOME/relaya/`），文件模式 `0600`。由 `relaya login` 写、其它命令读。

> ⚠️ **Token 以明文存储**。文件 mode `0600` + `$HOME` 私有路径与 `gh auth` / `aws configure` 等业界 CLI 同模式，但**对家用电脑被偷 / 恶意软件场景**，任何能读这个文件的进程都可以以你的身份调用 Relaya API（包括 MCP 工具，见下文 §钱路 friction step）。建议：
> - 不在共享 / 公共机器上 `relaya login`
> - macOS 用户：考虑在 FileVault 启用前先 `relaya logout`
> - Linux 用户：开启 home-dir 加密（`ecryptfs` / LUKS）
> - 怀疑泄漏 → `relaya logout` 立即清除本机凭证，并到 Relaya dashboard rotate API key
>
> Platform keychain backend（macOS Keychain / Windows DPAPI / Linux Secret Service）规划中，未上。

字段：

- `api_base` —— Relaya 网关 URL。默认 `https://api.relaya.pro`。自托管用户 / 本地开发可在 `login` 时用 `--api-base` 覆盖。
- `access_token` —— 所有需鉴权的 API 调用使用的 bearer。
- `relay_key` —— relay API key（`sk-relaya-…`），用于 `relaya use` 的子进程 env。从 `/api/token/*` 拉来、缓存于此。
- `user_id` / `username` —— 缓存，使 `status` 在首次 API 往返前就能渲染身份行。

## 开发

在 CLI 源码目录（含本 README、`go.mod`、`Makefile` 的目录）下执行：

```bash
go test ./...
go run . status            # 对生产
go run . login --api-base http://localhost:3000   # 对本地后端
```

本地全平台交叉编译（跟 CI 用同一份配方）：

```bash
make cli-release           # 产物在 dist/（5 平台 × 1 二进制 = 5 个文件）
```

## MCP server (`relaya mcp` 子命令)

`relaya` 二进制**内建** [Model Context Protocol](https://modelcontextprotocol.io) server——以子命令形式启动（`relaya mcp` 读 stdin 写 stdout），AI agent（Claude Code / Cursor / Codex CLI 等任意 MCP client）可直接 invoke 它，**用户不必打开终端**。

> ⚠️ **MCP server 鉴权模型 + 暴露面**
>
> - **不开端口**：`relaya mcp` 纯 stdio JSON-RPC，由 host CLI fork。**不监听任何 socket / TCP port**——网络层不暴露面。
> - **直接读 `~/.config/relaya/credentials.json`**：MCP server 没有自己的鉴权流，credentials 文件能读 = 可以以你的身份调用所有暴露的 tool。任何能以你的 user 权限跑进程的 MCP host 都拥有完全访问。
> - **钱路 `relaya_seller_withdraw` 有 friction step**：调用方必须传 `confirm: "yes"`，确保 AI agent 把转账动作在 UI 上 surface 给人类，避免 silent drain。其它 read-only 工具（status / topup / seller_list）无此要求。
>
> 不信任的 MCP host 不要装。

### 安装

跟 CLI 同一个 binary，装好 CLI 就能用：

```bash
make cli                                              # 本地编译，产物 ./bin/relaya
# 或直接 go install:
go install github.com/relaya-ai/relaya-ai@latest
```

### 接入 Claude Code

`~/.claude/settings.json` 加：

```json
{
  "mcpServers": {
    "relaya": {
      "command": "/abs/path/to/relaya",
      "args": ["mcp"]
    }
  }
}
```

接入 Cursor、Codex CLI 等其它 MCP client 类似——`command` 指向 `relaya` binary、`args: ["mcp"]`。

### 鉴权前提

必须先在终端跑过一次 `relaya login`——MCP server 是后台进程，没有终端交互能力，没法自己跑 device-code 流。它直接读 `~/.config/relaya/credentials.json`；缺则每个 tool 都返回 `isError: true` 的 "not logged in" 引导用户去 login。

### v1 暴露的 tools（4 个）

| Tool | 入参 | 作用 |
|---|---|---|
| `relaya_status` | 无 | 当前余额 / 已用 / 请求数 |
| `relaya_topup` | 无 | 返回 web 充值 URL |
| `relaya_seller_list` | 无 | 列出 marketplace seller channels |
| `relaya_seller_withdraw` | `{confirm: "yes", quota?: int}` | 把 seller_quota 转入主余额；**`confirm: "yes"` 必填**（钱路 friction） |

### 手动 smoke

```bash
make cli
./bin/relaya mcp <<'EOF'
{"jsonrpc":"2.0","id":1,"method":"initialize"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"relaya_status","arguments":{}}}
EOF
```

应该看到 3 行 JSON 响应：initialize 结果、4 个 tool 的列表、status 文本（或 not-logged-in 的 isError）。

## 这个二进制还**不**包含什么

当前未实现的（按重要性排序，后续 release 增量补，不破坏上面已有的 v1 surface）：

- ⚠️ OS 级 code signing（macOS notarization / Windows Authenticode）——目前靠 sigstore cosign keyless + SHA256SUMS 双层校验，详见上文「从 GitHub Release 拉二进制 — 务必校验」
- ❌ Platform keychain backend——token 仍明文存盘
- ❌ `relaya start` / `relaya configure` —— 客户端敏感字段脱敏的 local sanitizer proxy
- ❌ `relaya seller add-oauth claude/chatgpt` —— PKCE + local listener + browser flow（plain `add-key` 已落）
- ❌ QR sign-in 主路径 —— 当前 `login` 走 device-code
- ❌ Cert pinning、暗号字符串等防钓鱼分层

## 报告漏洞

请见 [`SECURITY.md`](./SECURITY.md)。
