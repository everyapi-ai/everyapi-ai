# `everyapi` CLI

[EveryAPI](https://everyapi.ai) AI API 网关的 buyer onboarding CLI。让任何 Claude Code / Codex / Gemini CLI **一分钟内**接到网关。

状态：**v1 已收口** —— buyer onboarding、seller 命令（plain-key + OAuth 三家）、sanitizer proxy、QR sign-in 主路径、防钓鱼分层均已落地。仍未实现的只有 OS 级 code signing 与 platform keychain backend（见文末「这个二进制还不包含什么」）。

## 安装

当前要么 clone + 自行 build，要么等 `v*` tag 后从 GitHub Release 拉二进制。

```bash
git clone https://github.com/everyapi-ai/everyapi-ai
cd everyapi-ai
go build -o everyapi .
```

或者直接走 Go install：

```bash
go install github.com/everyapi-ai/everyapi-ai@latest
# 二进制落在 $(go env GOPATH)/bin/everyapi
```

或者 Homebrew tap（待 v* tag 推出）：

```bash
# 首次安装
brew tap everyapi-ai/tap         # 一次性挂上 tap
brew install everyapi

# 之后升级 — 注意必须先 `brew update`
brew update                    # 刷新 tap formula 拿到最新版本号
brew upgrade everyapi
```

> ⚠️ **必须先 `brew update`**：单跑 `brew upgrade everyapi` 用的是本地缓存的 formula，会显示「already installed」即使 release 已经有新版。`brew update` 拉 tap repo 最新 formula 再 upgrade 才能跨版本。
>
> Homebrew 短语法 `brew install everyapi-ai/tap/everyapi` 同样能用——它内部会替你 tap，但显式 `tap` 一次能让后续 `brew upgrade` / `brew uninstall` 不必每次都写完整 path。

### 从 GitHub Release 拉二进制 — 务必校验

V1 还没做 OS 级 code signing（macOS notarization / Windows Authenticode），但**每次 release 都附带 `SHA256SUMS` 文件**，下载后请校验对得上：

```bash
# Linux / macOS
curl -LO https://github.com/everyapi-ai/everyapi-ai/releases/download/v0.1.0/everyapi_linux_amd64.tar.gz
curl -LO https://github.com/everyapi-ai/everyapi-ai/releases/download/v0.1.0/SHA256SUMS
sha256sum -c SHA256SUMS --ignore-missing
# everyapi_linux_amd64.tar.gz: OK

# Windows PowerShell
$expected = (Get-Content SHA256SUMS | Select-String "everyapi_windows_amd64.zip").ToString().Split()[0]
$actual = (Get-FileHash everyapi_windows_amd64.zip -Algorithm SHA256).Hash.ToLower()
if ($expected -ne $actual) { throw "checksum mismatch" } else { "OK" }
```

如果 `SHA256SUMS: FAILED`，**不要解压、不要运行**，到 Issue tracker 报告 + 检查你的下载源是否被劫持。

## 命令

| 命令 | 作用 |
|---|---|
| `everyapi login` | 当前设备登录 EveryAPI |
| `everyapi logout` | 清除本设备凭证 |
| `everyapi status` | 查看余额、使用量、配额 |
| `everyapi topup` | 打开充值页（带反钓鱼暗号 phrase 验证） |
| `everyapi use <tool>` | 配好 env 并 exec 进第三方 CLI（指向 EveryAPI） |
| `everyapi seller <sub>` | Marketplace 卖家端命令（list / withdraw / add-key / setup） |
| `everyapi edge <sub>` | BYO-GPU supplier agent 一键部署（register / start / status / logs / models / stop / update / remove） |
| `everyapi mcp` | 作为 MCP server 跑（stdin/stdout JSON-RPC） |
| `everyapi update` | 检查是否有新版本，打印对应安装方式的升级命令 |
| `everyapi version` | 显示构建版本 |
| `everyapi help` | 帮助 |

### `everyapi use <tool>` — exec 进第三方 CLI（指向 EveryAPI 网关）

装这个 CLI 的主要理由。它把目标第三方工具的环境变量按惯例配置好，然后 exec 进去——你既有的 Claude Code / OpenAI Codex CLI / Gemini CLI **不用改任何配置**就指向了 EveryAPI 网关。

```bash
everyapi use claude         # Claude Code → EveryAPI
everyapi use codex          # OpenAI Codex CLI → EveryAPI
everyapi use gemini         # Gemini CLI → EveryAPI
everyapi use                # 无参 → 交互式选择已安装的工具
```

每个工具的 env 惯例各不相同，CLI 帮你记住：

| 工具 | 设置的环境变量 |
|---|---|
| claude | `ANTHROPIC_BASE_URL`、`ANTHROPIC_AUTH_TOKEN` |
| codex | `OPENAI_BASE_URL`、`OPENAI_API_KEY` |
| gemini | `GEMINI_API_KEY`、`GOOGLE_GEMINI_BASE_URL` |

不必再去查每个工具读哪个变量名、要不要拼 `/v1` 后缀、走哪种 auth header。

> ⚠️ **Subprocess env 安全提示**：上面这些环境变量包含你的 relay API key。第三方 CLI 的 debug / verbose 模式可能会把 env 写进日志——`everyapi use` 之前确认你打开的 debug flag 不会泄漏 `*_TOKEN` / `*_API_KEY`；分享 debug 日志前先 `sed -i 's/sk-everyapi-[A-Za-z0-9]*/REDACTED/g'`。

### `everyapi login` — Device Authorization Grant + QR 登录

走 Device Authorization Grant（RFC 8628 形态）+ docs §7-5 Layer 1 「设备到设备 QR 登录」：

1. CLI 创建一个 session，**渲染终端 QR + 打印短码 + URL**
2. 用手机扫码（或在已登录 EveryAPI 的浏览器里打开 URL）——QR 里的 URL 已带 `?code=USR-789` 参数，dashboard 自动填好 code，user 只用点 Approve
3. CLI 拿到 access token，落地到 `~/.config/everyapi/credentials.json`（mode 0600）

```bash
everyapi login                                    # 生产，默认渲染 QR + 自动开浏览器
everyapi login --api-base http://localhost:8787   # 本地开发 / 自托管
everyapi login --no-browser                       # 不自动开浏览器（用 QR 扫）
everyapi login --no-qr                            # 不渲染 QR（非 UTF-8 终端 / piping）
```

QR 终端渲染样例（Unicode 半块字符；约 18-20 行高）：

```
█▀▀▀▀▀█  ▀▀ ▄  █▀▀▀▀▀█
█ ███ █  ▀▄█▀  █ ███ █
█ ▀▀▀ █ ▄ ▀ █▀ █ ▀▀▀ █
▀▀▀▀▀▀▀ █▄█▄█▄ ▀▀▀▀▀▀▀
... (实际 QR 编码 verification_uri?code=USR-789)
```

为什么这是更强的反钓鱼路径：

- User **不需要在新设备输密码** → 钓鱼站没机会骗 credential
- User **不需要弹浏览器到陌生页面** → web 跳转钓鱼面消失
- 即使 CLI 是恶意 fork 生成假 QR，扫码后的确认页是真 everyapi.ai 上的 dashboard（user 已登录设备触发），user 看到不认识的代码不会 approve

docs §7-5 其余 layers（cert pinning / 暗号字符串 / PKCE OAuth）已各自独立 PR 落地（cert pinning 为 report-only，enforce 按产品决策不做）。

### `everyapi seller <sub>` — marketplace 卖家端子命令

把 dashboard 的 channel mount / 提现操作搬到 terminal，方便 scripted onboarding。挂渠道前 `seller setup` 会先查 eligibility（账号激活 / 邮箱验证 / 账号年龄 / 消费记录 / channel 上限），失败的 gate 在**输入 key 之前**就先列出来，避免用户填一通才发现提交侧 422。

```bash
everyapi seller list                          # 列出已挂载的 channel
everyapi seller withdraw                      # 把全部 pending seller 收入转入主余额
everyapi seller withdraw --quota 1000         # 部分转账（DB 单位）
everyapi seller add-key   --type claude --name 'my-pro' \
                        --key 'sk-ant-...' --models 'claude-3-opus,claude-3-sonnet'
everyapi seller add-oauth codex --name 'my-chatgpt' --models 'gpt-4'
                                            # 一键 OAuth：CLI 启动 device flow，user 在浏览器
                                            # 输入 user_code，token 自动落 channel
everyapi seller add-oauth claude --name 'my-claude' --models 'claude-3-opus,claude-3-sonnet'
                                            # paste flow：CLI 打开 Anthropic 授权页，user 把
                                            # callback 显示的 code#state 粘回 terminal
everyapi seller add-oauth gemini --name 'my-gemini' --models 'gemini-1.5-pro'
                                            # 真一键 loopback：CLI 起 random-port listener，
                                            # Google 把 code 直接送回 CLI 无需粘贴
everyapi seller setup                         # 交互式 wizard：先查 eligibility，再引导 add-key
```

#### `add-key` — 多 key 备份池

`--key` 可以重复，把 N 把等价凭证挂在同一个 channel 上作为 backup pool（B2，PRODUCT §4.5）；当主 key 401/403 时后端自动 failover 到下一把。`--key-remark` 同样可重复，按位置跟 `--key` 对齐（第 i 个 `--key-remark` 是第 i 个 `--key` 的标签，便于以后 dashboard 上识别）。OAuth blob 不能进 backup pool —— 仍只能作为单 key channel。

```
everyapi seller add-key   --type claude --name 'claude-pool' \
                        --models 'claude-3-opus' \
                        --key 'sk-ant-primary' --key-remark 'primary' \
                        --key 'sk-ant-backup'  --key-remark 'team backup'
```

`add-key` 的 `--type` 接受 alias（`openai` / `claude` / `gemini` / `codex` / `vertex` / `aws` / `xai` / `deepseek`）或数字 id。挂载受 marketplace eligibility 限制（账号激活、邮箱已验证、消费记录、channel 数上限），CLI 在 `add-key` / `add-oauth` / `setup` 三条入口都会先查 eligibility 失败时把 checklist 列出来。

#### `add-oauth codex` — 一键 OAuth（device flow）

`everyapi seller add-oauth codex --name 'my-chatgpt' --models 'gpt-4'` 走 Codex / ChatGPT 的 RFC 8628-ish device authorization flow——seller**全程不接触 token 字符串**：

1. CLI 调 `/api/seller/codex/device/start`，拿到一个短 `user_code` 和 `verification_uri`
2. CLI 默认自动 open browser 到 `https://auth.openai.com/codex/device`（`--no-browser` 跳过）；user 在浏览器输入 `user_code` 完成授权
3. CLI 轮询 `/api/seller/codex/device/poll`，授权完成后后端自动建 channel、把 OAuth token 落到 channel `key` 字段
4. 输出 channel id + 已绑定的 ChatGPT 邮箱

授权 cookie 由进程内 `http.CookieJar` 管，不写盘——device flow state 短命且进程绑定，跟威胁模型对齐。

#### `add-oauth claude` — paste-and-submit OAuth

`everyapi seller add-oauth claude --name … --models …`。Anthropic OAuth provider 在他们那头把 `redirect_uri` 写死成 `https://console.anthropic.com/oauth/code/callback`，CLI 没法用 localhost listener 自动接 callback。流程：

1. CLI 调 `/api/seller/claude/oauth/start`，后端建 PKCE 对 + state，返 Anthropic 的 authorize URL
2. CLI 默认 open browser（`--no-browser` 跳过）；user 登 Anthropic、批准
3. Anthropic 把 user 重定向到他们 callback 页，显示一串 `<code>#<state>`
4. **user 复制这串粘回 CLI**
5. CLI 调 `/api/seller/claude/oauth/complete`，后端 exchange code+verifier 拿 token，mint channel

比 device flow 多 1 步粘贴操作，但仍比手工找 `~/.claude/auth.json` 简单太多。session cookie 在 start 时由 backend 下发，complete 必须命中同一 session——CLI 的 `http.CookieJar` 进程内管，per-invocation 隔离。

#### `add-oauth gemini` — 真一键 loopback OAuth

`everyapi seller add-oauth gemini --name … --models … [--no-browser] [--timeout 5m]`。Google 的 gemini-cli installed-app OAuth client 接受 `http://127.0.0.1:<port>/callback` 作为 redirect_uri，**CLI 自己起 listener 接 callback**，user 在浏览器登录后无需任何粘贴。流程：

1. CLI 在随机 ephemeral port (`127.0.0.1:0`) 起一个一次性 HTTP listener，路径固定 `/callback`
2. CLI 调 `/api/seller/gemini/oauth/start` 带 `redirect_uri = http://127.0.0.1:<port>/callback`；backend 严格校验 redirect 是 loopback / port ≥ 1024 / scheme=http / path=/callback / 无 query/fragment/userinfo（防 SSRF + 防 redirect 劫持）
3. CLI 默认 open browser；user 在 Google 登录 + 同意
4. Google 把 `?code=…&state=…` 重定向到 CLI 的 listener
5. CLI 验证 state 匹配（防 stale flow / 伪造），调 `/api/seller/gemini/oauth/complete`
6. Backend exchange code + 同一 redirect_uri 拿 token，mint channel

跟其他两个 provider 的对比：

| Provider | 体验 | 原因 |
|---|---|---|
| `codex` | user 输 6 位 user_code 到浏览器，CLI 自动 poll | OpenAI device flow，无 redirect_uri |
| `claude` | user 在浏览器登 + 复制 `code#state` 粘回 CLI | Anthropic 把 redirect_uri 写死成自家 callback URL |
| `gemini` | user 在浏览器登 + 关掉 tab 即完成 | Google 接受 loopback redirect |

`--timeout` 控制最长等待时间（默认 5 分钟）。超时退出 + 干净关闭 listener。

### `everyapi edge <sub>` — BYO-GPU supplier agent 一键部署

把空闲 GPU 接入 EveryAPI 卖算力。CLI 把整套部署收口成 8 个子命令，省掉 supplier 自己抄 docker-compose / 填 `.env` / 复制 registration token 这些手动步骤：

```bash
everyapi login                              # 复用现有登录
everyapi edge register --name "rtx-4090"    # 调 /api/seller/edge/nodes 拿 node_id + token，落到 ~/.local/share/everyapi/edge/<id>/
everyapi edge start                         # 自动探 NVIDIA / ROCm / Apple Silicon / CPU，docker compose up -d
everyapi edge models pull llama3.1:8b       # docker compose exec ollama ollama pull ...
everyapi edge status                        # 本地 docker compose ps + dashboard 端 online/offline
everyapi edge logs -f                       # 跟实时日志
everyapi edge update                        # docker compose pull && up -d
everyapi edge remove                        # down -v + 删本地 dir + 调 backend DELETE
```

`start` 通过 `text/template` runtime 渲染 `docker-compose.yml`（**不是 embed 静态 YAML**）—— 这样 container name 按 node_id namespace，单机多 node 不互踩；GPU passthrough 按 mode 条件渲染（NVIDIA = `deploy.resources.devices` + nvidia driver；ROCm = `/dev/kfd`+`/dev/dri`+`group_add: video`；macOS = 无 ollama 容器，agent 通过 `host.docker.internal` 连本地原生 ollama）。

凭证流：cli 用现有 `sk-everyapi-` Bearer 调 `POST /api/seller/edge/nodes` → backend 一次性返回 `registration_token`（之后 backend 只存 sha256，永不再回显）→ cli 0600 落盘到 `~/.local/share/everyapi/edge/<id>/node.json` → render 进 compose 的 `EVERYAPI_REGISTRATION_TOKEN` env。**注册 token 不会写进任何 .env 文件**（避免 supplier 误 commit）。

依赖：`docker` + `docker compose v2`（v1 EOL 不支持）。macOS 需要 `brew install ollama && brew services start ollama`（Metal 加速在 docker 容器里跑不了）。

### `everyapi topup` — 带反钓鱼暗号的充值跳转

`everyapi topup` 打开 dashboard 充值页，跳转前走一层 docs §7-5 Layer 3 验证：

1. CLI 调 backend `POST /api/cli/jump-session`，拿一个 session id + 4-emoji 暗号串（例如 `🌊 🦊 🍕 🚀`）
2. CLI 把 URL + 暗号都打到 terminal，提示 user "等下页面顶部要显示同样暗号"
3. User 按 Enter，CLI 用系统浏览器打开 URL（含 `?jump_session=<id>`）
4. Dashboard 加载时调 backend `GET /api/cli/jump-session/:id/phrase`，拿到同一个暗号串，在 page header **prominent 显示**
5. User 视觉对比：暗号一致 → 真 EveryAPI；不一致或没显示 → 关掉 tab，可能被钓鱼

为什么这能挡钓鱼：暗号在 backend 的内存里 keyed by random 32-hex session id；钓鱼站没 auth path 拿不到，攻击者构造的假 `wallet/topup?jump_session=<id>` 也读不到暗号。短 TTL (10 min) + single-use（dashboard 拿一次后 session 删除）进一步限制 reuse 风险。

```bash
everyapi topup                    # 默认开浏览器
everyapi topup --no-browser       # 只打 URL，手动复制
```

### `everyapi status` — 当前余额 / 使用量 / 配额

```
$ everyapi status

  alice (alice@example.com)
  quota:     $12.34 remaining   $5.67 used
  requests:  1,234
  topup:     https://app.everyapi.ai/wallet
```

### `everyapi update` — 自动跑对应安装方式的升级命令

查 GitHub mirror 最新 release，跟当前版本比对，**自动跑对应安装方式的升级命令**——一条命令搞定，不用复制粘贴。

检测逻辑（基于 `os.Executable()` resolve symlink 后的真实路径）：

| 路径包含 | 检测为 | 自动执行 |
|---|---|---|
| `/Cellar/` | Homebrew | `brew update && brew upgrade everyapi`（两步分开跑） |
| `$GOBIN` 或 `$GOPATH/bin` 或 `$HOME/go/bin` | `go install` | `go install github.com/everyapi-ai/everyapi-ai@latest` |
| 其它（curl / 手工放进 PATH） | unknown | 打印手动命令 + 提醒走 SHA256 + cosign 验证（无法安全自动替换二进制） |

```bash
$ everyapi update

Update available: v0.1.0 → v0.2.0
Install method:   Homebrew

$ brew update
==> Updated Homebrew from ...

$ brew upgrade everyapi
==> Upgrading everyapi-ai/tap/everyapi
  v0.1.0 -> v0.2.0
...

Done. Run `everyapi version` to confirm.
```

为什么不直接换二进制？三个原因：（1）brew / go 自己的校验链（SHA / module checksum）比我们自己在 CLI 内重做的更扎实；（2）自替换 running executable 在 Windows 平台基本是地雷区；（3）保留 README 推荐的 SHA256 + cosign 双层校验流程作为 unknown 路径下的明确 fallback。

Flag：
- `--check` —— 静默对比，已最新 exit 0 / 过时 exit 1。给 CI / cron 用：
  ```bash
  everyapi update --check || echo "needs upgrade"
  ```
- `--dry-run` —— 打印将要跑的命令但不实际执行，做 inspection 用

## 配置文件

凭证落在 `~/.config/everyapi/credentials.json`（若设置了 `$XDG_CONFIG_HOME` 则走 `$XDG_CONFIG_HOME/everyapi/`），文件模式 `0600`。由 `everyapi login` 写、其它命令读。

> ⚠️ **Token 以明文存储**。文件 mode `0600` + `$HOME` 私有路径与 `gh auth` / `aws configure` 等业界 CLI 同模式，但**对家用电脑被偷 / 恶意软件场景**，任何能读这个文件的进程都可以以你的身份调用 EveryAPI API（包括 MCP 工具，见下文 §钱路 friction step）。建议：
> - 不在共享 / 公共机器上 `everyapi login`
> - macOS 用户：考虑在 FileVault 启用前先 `everyapi logout`
> - Linux 用户：开启 home-dir 加密（`ecryptfs` / LUKS）
> - 怀疑泄漏 → `everyapi logout` 立即清除本机凭证，并到 EveryAPI dashboard rotate API key
>
> Platform keychain backend（macOS Keychain / Windows DPAPI / Linux Secret Service）规划中，未上。

字段：

- `api_base` —— EveryAPI 网关 URL。默认 `https://api.everyapi.ai`。自托管用户 / 本地开发可在 `login` 时用 `--api-base` 覆盖。
- `access_token` —— 所有需鉴权的 API 调用使用的 bearer。
- `relay_key` —— relay API key（`sk-everyapi-…`），用于 `everyapi use` 的子进程 env。从 `/api/token/*` 拉来、缓存于此。
- `user_id` / `username` —— 缓存，使 `status` 在首次 API 往返前就能渲染身份行。

## 开发

在 CLI 源码目录（含本 README、`go.mod`、`Makefile` 的目录）下执行：

```bash
go test ./...
go run . status            # 对生产
go run . login --api-base http://localhost:8787   # 对本地后端
```

本地全平台交叉编译（跟 CI 用同一份配方）：

```bash
make cli-release           # 产物在 dist/（5 平台 × 1 二进制 = 5 个文件）
```

## MCP server (`everyapi mcp` 子命令)

`everyapi` 二进制**内建** [Model Context Protocol](https://modelcontextprotocol.io) server——以子命令形式启动（`everyapi mcp` 读 stdin 写 stdout），AI agent（Claude Code / Cursor / Codex CLI 等任意 MCP client）可直接 invoke 它，**用户不必打开终端**。

> ⚠️ **MCP server 鉴权模型 + 暴露面**
>
> - **不开端口**：`everyapi mcp` 纯 stdio JSON-RPC，由 host CLI fork。**不监听任何 socket / TCP port**——网络层不暴露面。
> - **直接读 `~/.config/everyapi/credentials.json`**：MCP server 没有自己的鉴权流，credentials 文件能读 = 可以以你的身份调用所有暴露的 tool。任何能以你的 user 权限跑进程的 MCP host 都拥有完全访问。
> - **钱路 `everyapi_seller_withdraw` 有 friction step**：调用方必须传 `confirm: "yes"`，确保 AI agent 把转账动作在 UI 上 surface 给人类，避免 silent drain。其它 read-only 工具（status / topup / seller_list）无此要求。
>
> 不信任的 MCP host 不要装。

### 安装

跟 CLI 同一个 binary，装好 CLI 就能用：

```bash
make cli                                              # 本地编译，产物 ./bin/everyapi
# 或直接 go install:
go install github.com/everyapi-ai/everyapi-ai@latest
```

### 接入 Claude Code

`~/.claude/settings.json` 加：

```json
{
  "mcpServers": {
    "everyapi": {
      "command": "/abs/path/to/everyapi",
      "args": ["mcp"]
    }
  }
}
```

接入 Cursor、Codex CLI 等其它 MCP client 类似——`command` 指向 `everyapi` binary、`args: ["mcp"]`。

### 鉴权前提

必须先在终端跑过一次 `everyapi login`——MCP server 是后台进程，没有终端交互能力，没法自己跑 device-code 流。它直接读 `~/.config/everyapi/credentials.json`；缺则每个 tool 都返回 `isError: true` 的 "not logged in" 引导用户去 login。

### v1 暴露的 tools（8 个）

| Tool | 入参 | 作用 |
|---|---|---|
| `everyapi_status` | 无 | 当前余额 / 已用 / 请求数 |
| `everyapi_topup` | 无 | 返回 web 充值 URL |
| `everyapi_seller_list` | 无 | 列出 marketplace seller channels |
| `everyapi_seller_withdraw` | `{confirm: "yes", quota?: int}` | 把 seller_quota 转入主余额；**`confirm: "yes"` 必填**（钱路 friction） |
| `everyapi_seller_add_oauth_codex_start` | `{name, models}` | 起 Codex / ChatGPT 设备授权流，返 `user_code` + `verification_uri` + `flow_id` |
| `everyapi_seller_add_oauth_codex_poll` | `{flow_id}` | 查 Codex 授权状态。`pending`/`slow_down` 继续轮询；`authorized` 拿到 channel id；`expired`/`denied` 终止 |
| `everyapi_seller_add_oauth_claude_start` | `{name, models}` | 起 Anthropic OAuth 授权流，返 `authorize_url`。User 浏览器登完会拿到 `<code>#<state>` 字符串 |
| `everyapi_seller_add_oauth_claude_complete` | `{input}` | 把上一步 user 粘的 `<code>#<state>` 串提交完成，mint channel |

**OAuth tool 使用模式**（AI agent 在对话里这么走）：

```
User: 帮我加一个 ChatGPT Plus 卖家 channel，名字叫 my-chatgpt，models 是 gpt-4
AI    → everyapi_seller_add_oauth_codex_start({name: "my-chatgpt", models: "gpt-4"})
       ← "去 chatgpt.com/codex 输入 USR-789，然后告诉我做完了"
User: 浏览器输完了
AI    → everyapi_seller_add_oauth_codex_poll({flow_id: "..."})
       ← "status=pending，再等几秒"
[继续轮询直到 authorized]
       ← "status=authorized — channel #314 mounted"

User: 加 Claude Pro 那个，my-claude / claude-3-opus
AI    → everyapi_seller_add_oauth_claude_start({...})
       ← "去 [URL] 完成授权后，把 code#state 串给我"
User: code-abc123#state-xyz
AI    → everyapi_seller_add_oauth_claude_complete({input: "code-abc123#state-xyz"})
       ← "Channel #315 mounted"
```

Gemini OAuth（loopback flow）**不在 MCP 提供** —— loopback listener 跨 tool 调用生命周期不匹配。Gemini 仍走 CLI `everyapi seller add-oauth gemini`。

### 手动 smoke

```bash
make cli
./bin/everyapi mcp <<'EOF'
{"jsonrpc":"2.0","id":1,"method":"initialize"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"everyapi_status","arguments":{}}}
EOF
```

应该看到 3 行 JSON 响应：initialize 结果、4 个 tool 的列表、status 文本（或 not-logged-in 的 isError）。

## 这个二进制还**不**包含什么

当前**仍未实现**的（按重要性排序，后续 release 增量补，不破坏已有 v1 surface）：

- ⚠️ OS 级 code signing（macOS notarization / Windows Authenticode）——目前靠 sigstore cosign keyless + SHA256SUMS 双层校验，详见上文「从 GitHub Release 拉二进制 — 务必校验」
- ❌ Platform keychain backend——token 仍明文存盘（mode 0600）

原列于此、**现已落地**的（勿再当未实现）：

- ✅ Local sanitizer proxy —— 命令是 `everyapi proxy {start,stop,status,configure}`（不是 `everyapi start`/`everyapi configure`），引擎 + 6 内置 detector + 自定义 regex + 集成进 `everyapi use`
- ✅ Seller OAuth onboarding 三家 provider（codex device / claude paste / gemini loopback）
- ✅ QR sign-in 主路径 —— `login` 走 device-code **+ QR 主路径**，`--no-qr` 兜底
- ✅ 防钓鱼分层 —— 暗号字符串（`everyapi topup`）、PKCE/state strict-check、cert pinning 均已落地；cert pinning 为 **report-only**（匹配静默 / mismatch 告警 / 永不拒连），enforce 按产品决策定格"只告警不做"

## 报告漏洞

请见 [`SECURITY.md`](./SECURITY.md)。
