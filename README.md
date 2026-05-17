# `relaya` CLI

[Relaya](https://relaya.pro) AI API 网关的 buyer onboarding CLI。让任何 Claude Code / Codex / Gemini CLI **一分钟内**接到网关。

状态：**v1 —— buyer onboarding 最短闭环**。Seller 端命令、sanitizer proxy、QR sign-in 以及 [`docs/cli/channel-marketplace.md`](https://github.com/relaya-ai/relaya/blob/main/docs/cli/channel-marketplace.md) 描述的其余工具链由后续 PR 增量补齐。

## 安装

当前要么 clone + 自行 build，要么等 `v*` tag 后从 GitHub Release 拉二进制。

```bash
git clone https://github.com/relaya-ai/relaya
cd relaya
make cli                    # 产物在 ./bin/relaya
```

或者直接走 Go：

```bash
go install github.com/relaya-ai/relaya-ai@latest
# 二进制落在 $(go env GOPATH)/bin/relaya
```

## 命令

```
relaya login              当前设备登录 Relaya
relaya logout             清除本设备凭证
relaya status             查看余额、使用量、配额
relaya use <tool>         配好 env 并 exec 进第三方 CLI（指向 Relaya）
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

### `relaya status`

```
$ relaya status

  alice (alice@example.com)
  quota:     $12.34 remaining   $5.67 used
  requests:  1,234
  topup:     https://app.relaya.pro/wallet
```

## 配置文件

凭证落在 `~/.config/relaya/credentials.json`（若设置了 `$XDG_CONFIG_HOME` 则走 `$XDG_CONFIG_HOME/relaya/`），文件模式 `0600`。由 `relaya login` 写、其它命令读。

字段：

- `api_base` —— Relaya 网关 URL。默认 `https://api.relaya.pro`。自托管用户 / 本地开发可在 `login` 时用 `--api-base` 覆盖。
- `access_token` —— 所有需鉴权的 API 调用使用的 bearer。
- `user_id` / `username` —— 缓存，使 `status` 在首次 API 往返前就能渲染身份行。

## 开发

```bash
cd clients/cli
go test ./...
go run . status            # 对生产
go run . login --api-base http://localhost:3000   # 对本地后端
```

本地全平台交叉编译（跟 CI 用同一份配方）：

```bash
make cli-release           # 产物在 clients/cli/dist/（5 平台 × 1 二进制 = 5 个文件）
```

## MCP server (`relaya mcp` 子命令)

`relaya` 二进制**内建** [Model Context Protocol](https://modelcontextprotocol.io) server——以子命令形式启动（`relaya mcp` 读 stdin 写 stdout），AI agent（Claude Code / Cursor / Codex CLI 等任意 MCP client）可直接 invoke 它，**用户不必打开终端**。

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

### v0 暴露的 tools（4 个）

| Tool | 入参 | 作用 |
|---|---|---|
| `relaya_status` | 无 | 当前余额 / 已用 / 请求数 |
| `relaya_topup` | 无 | 返回 web 充值 URL |
| `relaya_seller_list` | 无 | 列出 marketplace seller channels |
| `relaya_seller_withdraw` | `{quota?: int}` | 把 seller_quota 转入主余额，无参 = 全转 |

[`docs/mcp/channel-marketplace.md`](https://github.com/relaya-ai/relaya/blob/main/docs/mcp/channel-marketplace.md) §7-6 spec 还规划了 5 个 tool（sanitizer / seller setup / add-key / add-oauth），等对应 CLI / 后端能力落地后逐个加。

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

完整目标设计见 [`docs/cli/channel-marketplace.md`](https://github.com/relaya-ai/relaya/blob/main/docs/cli/channel-marketplace.md)。v1 故意砍掉的：

- ❌ `relaya start` —— 客户端敏感字段脱敏的 local sanitizer proxy
- ❌ `relaya seller …` —— channel marketplace 卖家端命令（OAuth flow、提现等）
- ❌ QR sign-in 主路径 —— 当前 `login` 走 device-code
- ❌ Cert pinning、暗号字符串等防钓鱼分层
- ❌ Release 二进制 code signing（macOS notarization / Windows Authenticode / Linux sigstore）

后续 PR 按子节增量补，不破坏上面已有的 v1 surface。
