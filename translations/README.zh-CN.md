> 🌐 [English](../README.md) · **简体中文** · [日本語](README.ja.md) · [한국어](README.ko.md) · [Español](README.es.md) · [Deutsch](README.de.md) · [Français](README.fr.md)

# `everyapi` CLI

[EveryAPI](https://everyapi.ai) AI API 网关的 buyer onboarding CLI。**一分钟内**通过同一个受审计的注册表启动受支持的编码 agent。

状态：**核心流程已就位** —— buyer onboarding、seller 命令（plain-key + OAuth 三家）、sanitizer proxy、QR sign-in 主路径、防钓鱼分层均已落地。仍未实现的只有 OS 级 code signing 与 platform keychain backend（见文末「这个二进制还不包含什么」）。

## 安装

**macOS（Homebrew）：**

```bash
brew tap everyapi-ai/tap && brew install everyapi
```

之后升级 —— 先跑 `brew update`（不先跑的话，`brew upgrade everyapi` 会用缓存的 formula，明明有新版也会报 "already installed"）：

```bash
brew update && brew upgrade everyapi
```

**Linux / macOS（安装脚本）：**

```bash
curl -fsSL https://dl.everyapi.ai/install.sh | bash
```

脚本自动探测 OS + arch，下载对应的 `everyapi_{os}_{arch}.tar.gz`，校验 SHA256，安装到 `~/.local/bin`（以 root 运行时装到 `/usr/local/bin`）。如果本机装了 [cosign](https://github.com/sigstore/cosign)，还会验证 keyless 签名 —— 传 `--require-signature` 可以把这一步变成强制（CI / 供应链敏感环境推荐）。

一条命令，全球可用：脚本在运行时选择下载源 —— 能连通时走 GitHub Releases，GitHub 慢或被墙时走中国大陆镜像 —— 所以同一行命令在国内外都能装。设置 `EVERYAPI_DOWNLOAD_BASE` 可强制指定镜像。

常用 flag：

```bash
curl -fsSL https://dl.everyapi.ai/install.sh | bash -s -- --version v0.2.2     # 锁定版本
curl -fsSL https://dl.everyapi.ai/install.sh | bash -s -- --prefix /usr/local  # 自定义前缀
curl -fsSL https://dl.everyapi.ai/install.sh | bash -s -- --require-signature  # cosign 校验失败即中止
curl -fsSL https://dl.everyapi.ai/install.sh | bash -s -- --force              # 重装同一版本
```

之后升级重跑同一条命令即可。脚本解析最新 release tag，有更新时就地替换二进制；如果本机已是目标版本，会以 `already at vX.Y.Z — nothing to do` 退出（放进 setup 脚本 / dotfiles 是安全的）。传 `--force` 可覆盖重装（用于验证完整性或修复损坏文件）。脚本本身也发布在本仓库的 [`install.sh`](../install.sh)，你可以先下载读一遍再跑。

**Go 用户（`go install`）：**

```bash
go install github.com/everyapi-ai/everyapi-ai/v3@latest
```

**Windows（PowerShell）：**

```powershell
irm https://dl.everyapi.ai/install.ps1 | iex
```

流程与 shell 脚本一致 —— 解析最新 tag，下载 `everyapi_windows_amd64.zip` + `SHA256SUMS`，校验 hash（`PATH` 上有 cosign 时一并校验签名），把 `everyapi.exe` 装到 `%LOCALAPPDATA%\everyapi\bin` 并加入用户 `PATH`。要锁定版本或传其它参数，先把脚本落地：`& ([scriptblock]::Create((irm https://dl.everyapi.ai/install.ps1))) -Version v0.2.2`。脚本同样发布在本仓库的 [`install.ps1`](../install.ps1)。

**Windows（手动）：** 从 [Releases 页面](https://github.com/everyapi-ai/everyapi-ai/releases/latest) 下载 `everyapi_windows_amd64.zip`（或其它产物），对照 `SHA256SUMS` 校验后再放到 `%PATH%`。

## 命令

在 TTY 上不带参数直接跑 `everyapi`，会打开覆盖同一批命令的交互式启动器；`everyapi help` 则以文本形式打印。

| 命令 | 作用 |
|---|---|
| `everyapi auth <sub>` | 登录 / 登出、查看会话状态（`login` / `logout` / `status`） |
| `everyapi wallet <sub>` | 充值（带反钓鱼暗号 phrase 验证）、支付历史、支付方式 |
| `everyapi checkin <sub>` | 领取今日签到配额；查看本月签到日历 |
| `everyapi account <sub>` | 个人资料、2FA、邀请码、订阅套餐 |
| `everyapi use <tool>` | 配好 env 并 exec 进第三方 CLI（指向 EveryAPI） |
| `everyapi token <sub>` | 管理 relay API key（list / create / key / revoke / switch / …） |
| `everyapi models <sub>` | 模型目录：list / pricing / groups |
| `everyapi stats <sub>` | 用量、请求日志、单模型性能、上游健康度 |
| `everyapi market <sub>` | 需求帖、纠纷、滥用举报 |
| `everyapi inbox <sub>` | 站内通知与私信 |
| `everyapi seller <sub>` | Marketplace 卖家端命令（list / setup / withdraw / add-key / add-oauth） |
| `everyapi edge <sub>` | BYO-GPU supplier agent 一键部署（register / start / status / logs / models / rename / pause / resume / stop / update / remove） |
| `everyapi artifacts <sub>` | 发布与管理自包含 HTML 报告（`share` / `list` / `update` / `delete`） |
| `everyapi events` | 订阅实时事件流（SSE） |
| `everyapi feedback` | 向团队提交 bug 反馈或功能建议 |
| `everyapi proxy <sub>` | 本地脱敏代理（`start` / `stop` / `status` / `configure`） |
| `everyapi computer <sub>` | 通过 Accessibility 读取并操作本机 macOS 应用窗口 |
| `everyapi mcp` | 作为 MCP server 跑（stdin/stdout JSON-RPC） |
| `everyapi doctor` | 自检：凭证、网关、脱敏代理、已安装工具 |
| `everyapi settings <sub>` | 查看 / 修改 CLI 偏好（语言、终端模式） |
| `everyapi admin` | 运维控制台 —— 仅管理员账号可见 |
| `everyapi version [update\|uninstall]` | 构建版本；检查并执行升级；卸载 CLI |
| `everyapi help` | 打印完整命令列表 |

### `everyapi computer <sub>` —— 本机 macOS computer use

macOS 上的 CLI 可以发现正在运行的应用和窗口、返回一份有界的 Accessibility 快照，并执行语义动作或坐标动作。这个能力只在本机可用，不注册进 `everyapi mcp`。Linux 和 Windows 构建会明确返回 `unsupported_platform`。

在 macOS 上，`everyapi computer` 通过本地 Unix socket 驱动一个独立代码签名的小助手应用（`EveryAPI Computer Use.app`，由 `clients/desktop/native/computer-use-macos` 构建）；首次使用时若尚未安装，会自动下载并启动它——如果 EveryAPI Connect 已经装过自带的那一份，CLI 会直接复用，不会再下一份。助手把截图支持报告为 false，因为 macOS 没有通过这个 provider 暴露可靠的公开窗口级捕获标识符；它绝不会用可能拍到其他遮挡应用的整屏区域截图来顶替。

```bash
everyapi computer capabilities --json
everyapi computer permissions --json
everyapi computer list-apps
everyapi computer list-windows --app com.apple.TextEdit
everyapi computer get-app-state --app com.apple.TextEdit --window-index 0 --json
everyapi computer click --app com.apple.TextEdit --window-index 0 --element-index 12
printf '%s' 'safe text' | everyapi computer set-value --app com.apple.TextEdit --window-index 0 --element-index 12 --value-stdin
everyapi computer hotkey --app com.apple.TextEdit --window-index 0 --key cmd+a
```

跑 `everyapi computer permissions --json`，然后在「系统设置 > 隐私与安全性 > 辅助功能」里把权限授予 **EveryAPI Computer Use**——不是授予 `everyapi`、`osascript` 或你的终端。因为助手是拥有自己 bundle 身份的独立签名应用，这份授权被限定在这一项能力上：它不会顺带授权机器上的每个 AppleScript 或 JXA 脚本，而且能在 CLI 与助手升级后继续有效。`permissions` 会直接报告 Accessibility，而把 Automation 报为 `unknown`，因为这个 provider 不依赖 System Events，也没有单独的 Automation 预检可跑。

元素索引来自最近一次 `get-app-state` 快照，两分钟后过期。窗口按索引选择（`--window-index`），但内部用 CoreGraphics 在屏上分配的真实窗口 id 来标识（有的话），最小化窗口则退回到快照内的合成 id；无论哪种方式，provider 都用一个内部指纹来检测可观察的变化，但公开的 Accessibility 属性无法证明属性完全相同的替换窗口或控件就是同一个原生实例。缓存只在 `~/.config/everyapi/computer-use/state/` 下以私有权限保存不透明的应用、进程、窗口、路径、role、frame、动作名和指纹数据。遇到 `app_stale`、`element_stale` 或 `window_stale` 后要重新取快照。GUI 动作成功之后，即使尽力而为的状态刷新失败，它依然算成功；此时 JSON 里会带上 `refreshError`，而不是返回一个可重试的动作错误。如果动作已经交出去之后助手调用被中断或返回了无效回执，`action_outcome_unknown` 意味着动作可能已经发生；先刷新状态再决定要不要重试。

一份维护中的名单会拦截已知的终端应用、密码管理器、钥匙串访问、「密码」、系统设置以及 EveryAPI Connect，作为纵深防御层面的阻力。基于 bundle ID 的拦截并不是一个完备的应用分类器：名单外的应用、内置终端的编辑器、浏览器，以及改名或新发布的应用都可能暴露等价能力。真正的信任边界仍然是显式的 `--app` 目标、macOS TCC，以及调用方本身的同用户权限。读到的文本在输出前会剥掉终端控制序列并扫描凭据；输入或设置的文本若命中内置的密钥检测器会被拒绝。优先用 `--text-stdin` 和 `--value-stdin`，把普通文本挡在 shell 历史之外。

### `everyapi use <tool>` — exec 进第三方 CLI（指向 EveryAPI 网关）

装这个 CLI 的主要理由。它会通过 EveryAPI 配置并启动受支持的编码客户端。原生接入的 `antigravity` 和 `librefang` 保留各自的认证路径，永远不会收到复制过去的 relay key。

```bash
everyapi use claude            # Claude Code → EveryAPI
everyapi use codex             # OpenAI Codex CLI → EveryAPI
everyapi use opencode          # OpenCode → 进程级 EveryAPI provider
everyapi use gemini            # Google Gemini CLI → EveryAPI
everyapi use antigravity       # Antigravity（原生 Google 认证与路由）
everyapi use aider             # Aider → EveryAPI（选一个模型）
everyapi use goose             # Goose CLI → EveryAPI（选一个模型）
everyapi use crush             # Crush CLI → 隔离的 EveryAPI 模型目录
everyapi use cline             # Cline CLI → 生命周期绑定的 provider 配置
everyapi use openclaw          # OpenClaw 本地 TUI → 隔离的 EveryAPI 模型目录
everyapi use continue          # Continue CLI → 隔离的 assistant 配置
everyapi use kilo              # Kilo Code CLI → 进程级 provider 配置
everyapi use pi                # Pi coding agent → 隔离的模型目录
everyapi use pi-web            # Pi Web 浏览器 UI → 写进持久 models.json 的 provider 条目
everyapi use vibe              # Mistral Vibe → 隔离的通用 provider
everyapi use copilot           # GitHub Copilot CLI → 官方进程级 BYOK
everyapi use droid             # Factory Droid → 隔离的运行时设置
everyapi use openhands         # OpenHands CLI → 显式的仅进程 env 覆盖
everyapi use forge             # ForgeCode → 隔离的 OpenAI 兼容会话
everyapi use llxprt            # LLxprt Code → 隔离 home + 固定运行时 flag
everyapi use grok              # xAI Grok Build → EveryAPI
everyapi use qwen-code         # 阿里巴巴 Qwen Code → EveryAPI（选一个模型）
everyapi use kimi-code         # Moonshot Kimi Code → EveryAPI（选一个模型）
everyapi use hermes            # Nous Research Hermes Agent → EveryAPI（选一个模型）
everyapi use librefang         # LibreFang 启动（原生 EveryAPI 凭证流程）
everyapi use open-webui        # Open WebUI server → 以 EveryAPI 作为其 OpenAI 后端
everyapi use deepseek-harness  # DeepSeek Harness web UI（dsh）→ 生成 provider + 凭证
everyapi use hermes --model gpt-5.1      # 锁定模型，跳过选择器
everyapi use claude                      # 默认透明：仍请求 api.anthropic.com
everyapi use codex                       # 仍请求 api.openai.com
everyapi use antigravity                 # 仍请求 Google 官方 Origin
everyapi use claude --transparent=false  # 退出透明模式：注入网关 Base URL + relay key
everyapi use                             # 无参 → 交互式选择已安装的工具
```

每个工具的接入惯例各不相同，CLI 帮你记住：

| 工具 | EveryAPI 接入方式 |
|---|---|
| claude | env：`ANTHROPIC_BASE_URL`、`ANTHROPIC_AUTH_TOKEN`；通过网关发现实时可用的兼容模型 |
| codex | env：`OPENAI_API_KEY` + 持久化的 EveryAPI `CODEX_HOME`（保留会话）+ 生命周期绑定的 `--profile` 与按 key 限定的模型目录（codex 走配置路由，不读 `OPENAI_BASE_URL`） |
| gemini | env：`GEMINI_API_KEY`、`GOOGLE_GEMINI_BASE_URL`、`GEMINI_MODEL`；隔离的 auth-mode 设置覆盖层 |
| antigravity | 原生 Antigravity 启动器（`agy`） |
| aider | OpenAI 兼容 env，外加 `openai/<model>` 的 LiteLLM 模型命名空间 |
| goose | `GOOSE_PROVIDER=openai`、`GOOSE_MODEL`、`OPENAI_API_KEY`、`OPENAI_BASE_URL` |
| crush | 进程级 `CRUSH_GLOBAL_CONFIG`；key 从 env 引用，模型目录实时生成 |
| cline | 生命周期绑定的 `CLINE_PROVIDER_SETTINGS_PATH`，退出后删除 |
| openclaw | 本地内嵌 TUI，进程级配置 + 由 env 支撑的 SecretRef |
| continue | 生命周期绑定的 `CONTINUE_GLOBAL_DIR/config.yaml`；Continue secret 引用由 env 支撑 |
| kilo | 进程级 `KILO_CONFIG_CONTENT`；OpenCode 兼容 provider，key 由 env 支撑 |
| pi | 隔离的 `PI_CODING_AGENT_DIR`，内含 `models.json` 与已选模型设置；启动前 `PI_CODING_AGENT_DIR`（默认 `~/.pi/agent`）里已有的 `{extensions,skills,prompts,themes}` 按绝对路径加载 |
| pi-web | 把 `providers.everyapi` 合并进*持久*的 `PI_CODING_AGENT_DIR/models.json`（默认 `~/.pi/agent`），让会话、项目信任、已选模型以及 Models 面板自己的改动全部保留；relay key 仍只是 env 引用，不落盘 |
| vibe | 隔离的 `VIBE_HOME/config.toml`；带 `api_key_env_var` 的通用 provider |
| copilot | 官方 `COPILOT_PROVIDER_*` BYOK 环境；wire API 依所选模型的能力而定 |
| droid | 官方 `--settings` 仅运行时文件，含一个 `custom:EveryAPI-0` 模型，key 由 env 支撑 |
| openhands | `--override-with-envs` 加仅进程的 `LLM_API_KEY`、`LLM_BASE_URL`、`LLM_MODEL` |
| forge | 隔离的 `FORGE_CONFIG`；OpenAI 兼容 provider/model 固定在配置与进程 env 中 |
| llxprt | 隔离的应用 home，外加保留的 `--provider openai`、`--baseurl`、`--model` 运行时 flag |
| grok | env：`XAI_API_KEY`、`GROK_MODELS_BASE_URL`；隔离的 `GROK_HOME`；过滤后的实时模型发现 |
| qwen-code | env：`OPENAI_API_KEY`、`OPENAI_BASE_URL`、`OPENAI_MODEL`；进程级 `QWEN_HOME` 用户设置与固定的 `--auth-type=openai` |
| kimi-code | env：`KIMI_MODEL_API_KEY`、`KIMI_MODEL_BASE_URL`、`KIMI_MODEL_PROVIDER_TYPE`、`KIMI_MODEL_NAME`；隔离的 `KIMI_CODE_HOME` 与生成的模型别名 |
| hermes | 生成的 `HERMES_HOME/config.yaml`（具名 custom provider、`base_url`、内联 `api_key`）；过滤后的实时模型发现 |
| librefang | 原生 `librefang start`，它会把守护进程 detach 并交还终端（`librefang stop` 结束）；LibreFang 每次请求都解析当前 EveryAPI 凭证 |
| open-webui | 以 `open-webui serve` 启动，带 `OPENAI_API_BASE_URLS`、`OPENAI_API_KEYS`、`ENABLE_PERSISTENT_CONFIG=false`，让进程 env 压过任何已存配置；`DATA_DIR` 固定为 `~/.open-webui` |
| deepseek-harness | 官方 `dsh web` UI；在 `$DSH_HOME/settings.yaml`（默认 `~/.dsh`，mode `0700`）生成 `llm-pi-ai.providers.everyapi` 条目，另加一份 `0600` 的 `.credentials.yaml` 存放 key |

不必再去查每个工具读哪个变量名、要不要拼 `/v1` 后缀、走哪种 auth header。

**relay key 选择**：不带 `--group` 启动时，会解析账号的 auto-group key —— 那把能路由到你可访问的所有 group 的 key —— 并缓存进 `credentials.json`。没有 auto key 的账号（或所在 tier 可能已不能用该 group 的），回退到最新启用的 key。跑 `everyapi token switch` 可以把别的 key 固定为默认，或用 `--group <id>` 单次走另一个池；group 覆盖永远不会写进那个缓存。你用哪把 key 决定下面看到的模型目录：固定到某一个 group 的 key 只会看到该 group 的模型。

之前启动缓存下来的 key 会继续沿用 —— 这个查找刻意是离线的，所以它不会自己重选。如果 `/model` 里只显示某一个 group 的模型，跑一次 `everyapi token switch` 并选 `Auto`。

**模型选择**：启动时 EveryAPI 拉取所选 relay key/group 可用的实时目录，剔除不兼容的媒体/embedding 协议，把结果快照注入每个被路由客户端的原生选择器。在 Claude Code、Codex、Qwen Code、Kimi Code 里用 `/model`；Grok 用 `/model`/`models` 入口，Hermes 用 `hermes model`。非 Claude 的模型 ID 在内部用 Claude 兼容别名表示，但展示和发往上游时都用真实 ID。

带 `ModelEnv` 契约的工具（Gemini、Aider、Goose、Crush、Cline、OpenClaw、Continue、Kilo、Pi、Vibe、GitHub Copilot CLI、Factory Droid、OpenHands、ForgeCode、LLxprt、Hermes、Qwen Code、Kimi Code）会打开 EveryAPI 的选择器；传 `--model <id>` 可跳过。非交互运行时 EveryAPI 确定性地取第一个兼容模型。裸 claude/codex/grok 仍自己决定启动模型。`antigravity` 用 Google 认证启动原生 `agy`，`librefang` 走它自己的一方 EveryAPI 凭证流程。`pi-web`、`open-webui`、`deepseek-harness` 提供的是浏览器 UI：EveryAPI 会提前注册好 provider 与整份兼容目录，模型在那个 UI 里选，而不是走终端选择器。

**reasoning level**：选完模型后，`everyapi use codex` 和 `everyapi use pi` 会问用哪个 reasoning level 启动，并记住答案供下次使用 —— 问一次，之后沿用不再提示，和下面的安全偏好同理。这两个客户端的门槛不同，因为它们掌握的信息不同。Codex 读它自带目录为该模型公布的档位（`low` … `ultra`，各模型不同 —— `gpt-5.6-sol` 到 `ultra`，`gpt-5.5` 到 `xhigh` 为止），并以 `model_reasoning_effort` 接收选择；它不会为此询问网关，所以 Codex 没听说过的模型就没有这一步。Pi 对 custom provider 没有 per-model 表，所以它这一步只在网关已确认该模型接受 effort 时出现（`/v1/models` 上的 `supports_thinking`）；给出的是 `off` … `high`，以 `defaultThinkingLevel` 接收。当前模型不提供的历史档位会被丢弃而不是硬套；这一特性上线后第一次启动时，光标停在 Codex 持久化 home 里已有的 effort 上，所以直接回车不会改变任何东西。两个客户端各自的会话内控制仍然保留 —— Codex 的 `/model`、pi 的 shift+tab —— 而跨启动的选择由启动器保管，因为 Codex 生成的 profile 和 Pi 的隔离 home 都会在退出时删除。

Provider 名不等于 CLI 名：这两家厂商的官方客户端请用 `qwen-code` 或 `kimi-code`，provider 的模型请从受支持客户端的实时模型目录里选。

**hermes 配置隔离**：`everyapi use hermes` 会把 `HERMES_HOME` 重定向到 `~/.config/everyapi/sessions` 下的进程级目录；其中带凭证的配置和实时 proxy URL 在退出时删除，不会与另一个 key/group 冲突。只有最后选择的模型 ID 会作为安全偏好保留。你个人的 `~/.hermes` 保持不动。生成的配置把 EveryAPI 注册为具名 custom provider，这样 `hermes model` 就能发现并切换模型，而不会回落到 OpenRouter。裸 `hermes` 打开交互式对话；要终端 UI 请用 `everyapi use hermes -- --tui`。

**grok 配置隔离**：`everyapi use grok` 会把 `GROK_HOME` 重定向到 `~/.config/everyapi/grok-home`。这能避免缓存的 xAI 浏览器会话覆盖 EveryAPI relay key，并让 EveryAPI 路由的会话与裸 `grok` 分开。Grok 专属 flag 放在 `--` 之后，例如 `everyapi use grok -- --model grok-4.5`。

**Qwen/Kimi 配置隔离**：每次被路由的启动都会拿到 `~/.config/everyapi/sessions` 下的进程级 home，子进程退出即删除，所以并发的 key/group 不会互相覆盖对方的模型目录或 loopback URL。Qwen 真正的系统设置保持不动，并保留管理员优先级。如果管理员或 workspace 设置定义了 `modelProviders.openai`（会遮蔽实时的 EveryAPI 目录），启动会以可操作的冲突提示中止，而不是悄悄显示过期/不兼容的模型。

> ⚠️ **Subprocess env 安全提示**：上面这些环境变量包含你的 relay API key。第三方 CLI 的 debug / verbose 模式可能会把 env 写进日志 —— `everyapi use` 之前确认你打开的 debug flag 不会泄漏 `*_TOKEN` / `*_API_KEY`。分享 debug 日志前先跑 `sed -i 's/sk-everyapi-[A-Za-z0-9]*/REDACTED/g'`。

#### 透明 Connector（默认）

透明模式让受支持的客户端继续请求供应商官方 API Origin，而不是设置第三方 Base URL。所有支持它的工具都默认启用；传 `--transparent=false` 可退出。CLI 会在随机 loopback 端口启动临时 HTTP CONNECT proxy，每次运行生成一张 CA，其私钥只存在于内存中；子进程只收到代理地址、公开 CA bundle 和无秘密的占位凭证。已注册的模型路径在本机解密后携带真实 relay key 转发给 EveryAPI；其它 HTTPS 域名走原样 CONNECT 直通。受保护模型前缀下的未知路径会被阻止，转发失败也绝不会回落到供应商直连。

已针对 Claude Code 和 Codex CLI 验证过 —— 它们也正是默认启用透明模式的工具。原生 Antigravity 和 LibreFang 绕过 connector；其它已注册工具走各自文档化的注入/配置路径，所以对不支持的工具显式传 `--transparent` 会明确报错。

`--sanitize` 与透明模式是组合关系而非冲突：connector 会经由 sanitizer 转发（子进程 → connector → sanitizer → 网关），所以掩码与 Claude 恢复响应守卫在两条启动路径上都生效。

如果你只设置了 `ALL_PROXY` 这一个代理变量，透明模式会被拒绝，启动回落到注入路径 —— Go 的代理解析从不读 `ALL_PROXY`，connector 无法遵守它。设置 `HTTPS_PROXY`（含 socks5，net/http 原生支持）即可保持透明模式开启。

这个模式是实验性的，并且刻意限定在进程范围内：

- 被拦截的客户端侧目前使用 HTTP/1.1，支持普通 JSON/SSE 请求（网关的 HTTP/2 响应会转换成 HTTP/1.1）；客户端侧 HTTP/2、HTTP/3/QUIC、WebSocket、证书固定的客户端，以及忽略 `HTTPS_PROXY` 的客户端都不在覆盖范围内；
- Codex 内置 OpenAI provider 会先探测一次 Responses WebSocket；Connector 返回 HTTP 426，使 Codex 立即回落 HTTPS/SSE 而不消耗重试预算。Codex 仍可能打印这一条探测失败日志；
- Claude Code 仍会把无秘密的占位凭证视为 API-key 认证，因此即使 `ANTHROPIC_BASE_URL` 保持官方的 `https://api.anthropic.com` Origin，claude.ai connectors 仍会被禁用。透明模式规避的是第三方 Origin 判断，不能把 API-key 认证变成 claude.ai OAuth 登录；
- 它不安装系统 CA、不需要管理员权限，也不改变 `everyapi use` 的默认行为；
- 它并非不可检测：客户端仍可检查代理变量、本地证书链、socket、时延或响应差异；
- Connector 能看到解密后的模型内容。它的 CA 签名私钥永不写盘或上传，公开 CA 文件在退出时删除；
- relay key 不进入子进程环境与生成的客户端配置，但现有的 `~/.config/everyapi/credentials.json` 仍能被同一 OS 用户下的任何进程读取。透明模式提供的是凭证注入隔离，不是针对恶意子进程的沙箱。

### `everyapi auth login` — Device Authorization Grant + QR 登录

走 Device Authorization Grant（RFC 8628 形态）+ docs §7-5 Layer 1 「设备到设备 QR 登录」：

1. CLI 创建一个 session，**渲染终端 QR + 打印短码 + URL**
2. 用手机扫码（或在已登录 EveryAPI 的浏览器里打开 URL）—— QR 里的 URL 已带 `?code=USR-789`，dashboard 自动填好 code，用户只用点 Approve
3. CLI 拿到 access token，落地到 `~/.config/everyapi/credentials.json`（mode 0600）

```bash
everyapi auth login                                    # 生产，默认渲染 QR + 自动开浏览器
everyapi settings set gateway_region cn               # 后续命令使用中国加速网关
everyapi auth login --api-base http://localhost:8787   # 本地开发 / 自托管
everyapi auth login --no-browser                       # 不自动开浏览器（用 QR 扫）
everyapi auth login --no-qr                            # 不渲染 QR（非 UTF-8 终端 / piping）
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

- 用户**不需要在新设备输密码** → 钓鱼站没机会骗 credential
- 用户**不需要被弹到陌生的浏览器页面** → web 跳转钓鱼面消失
- 即使 CLI 是恶意 fork 生成假 QR，扫码后的确认页仍是真 everyapi.ai 上的 dashboard（由用户已登录的设备触发），而用户不会去 Approve 一个不认识的 code

docs §7-5 其余 layers（cert pinning / 暗号字符串 / PKCE OAuth）已各自独立 PR 落地（cert pinning 为 report-only，enforce 按产品决策不做）。

### `everyapi seller <sub>` — marketplace 卖家端子命令

把 dashboard 的 channel mount / 提现流程搬到 terminal，方便 scripted onboarding。挂渠道前 `seller setup` 会先查 eligibility（账号激活 / 邮箱验证 / 账号年龄 / 消费记录 / channel 上限），失败的 gate 在**用户输入 key 之前**就列出来，避免提交后才吃一个 422。

```bash
everyapi seller list                          # 列出已挂载的 channel
everyapi seller withdraw                      # 把全部 pending seller 收入转入主余额
everyapi seller withdraw --quota 1000         # 部分转账（DB 单位）
everyapi seller add-key   --type claude --name 'my-pro' \
                        --key 'sk-ant-...' --models 'claude-3-opus,claude-3-sonnet'
everyapi seller add-oauth codex --name 'my-chatgpt' --models 'gpt-4'
                                            # 一键 OAuth：CLI 启动 device flow，用户在浏览器
                                            # 输入 user_code，token 自动落到 channel
everyapi seller add-oauth claude --name 'my-claude' --models 'claude-3-opus,claude-3-sonnet'
                                            # paste flow：CLI 打开 Anthropic 授权页，用户把
                                            # callback 显示的 code#state 粘回 terminal
everyapi seller add-oauth gemini --name 'my-gemini' --models 'gemini-1.5-pro'
                                            # 真一键 loopback：CLI 起 random-port listener，
                                            # Google 把 code 直接送回 CLI，无需粘贴
everyapi seller setup                         # 交互式 wizard：先查 eligibility，再引导 add-key
```

#### `add-key` — 多 key 备份池

`--key` 可以重复，把 N 把等价凭证挂在同一个 channel 上作为 backup pool（B2，PRODUCT §4.5）；当主 key 返回 401/403 时后端自动 failover 到下一把。`--key-remark` 同样可重复，按位置与 `--key` 对齐（第 i 个 `--key-remark` 是第 i 个 `--key` 的标签，便于以后在 dashboard 上识别）。OAuth blob 不能进 backup pool —— 它们仍只能是单 key channel。

```
everyapi seller add-key   --type claude --name 'claude-pool' \
                        --models 'claude-3-opus' \
                        --key 'sk-ant-primary' --key-remark 'primary' \
                        --key 'sk-ant-backup'  --key-remark 'team backup'
```

`add-key` 的 `--type` 接受 alias（`openai` / `claude` / `gemini` / `codex` / `vertex` / `aws` / `xai` / `deepseek`）或数字 id。挂载受 marketplace eligibility 限制（账号激活、邮箱已验证、消费记录、channel 数上限），CLI 在 `add-key` / `add-oauth` / `setup` 三条入口都会先把未通过的 checklist 列出来再做别的事。

#### `add-oauth codex` — 一键 OAuth（device flow）

`everyapi seller add-oauth codex --name 'my-chatgpt' --models 'gpt-4'` 走 Codex / ChatGPT 的 RFC 8628-ish device authorization flow —— seller **全程不接触 token 字符串**：

1. CLI 调 `/api/seller/codex/device/start`，拿到一个短 `user_code` 和 `verification_uri`
2. CLI 默认自动打开浏览器到 `https://auth.openai.com/codex/device`（`--no-browser` 跳过）；用户在浏览器输入 `user_code` 完成授权
3. CLI 轮询 `/api/seller/codex/device/poll`，授权完成后后端建 channel，把 OAuth token 写入 channel 的 `key` 字段
4. 输出 channel id + 已绑定的 ChatGPT 邮箱

授权 cookie 由进程内 `http.CookieJar` 管理，不写盘 —— device flow state 短命且进程绑定，与威胁模型一致。

#### `add-oauth claude` — paste-and-submit OAuth

`everyapi seller add-oauth claude --name … --models …`。Anthropic 的 OAuth provider 在他们那头把 `redirect_uri` 写死成 `https://console.anthropic.com/oauth/code/callback`，CLI 没法用 localhost listener 接 callback。流程：

1. CLI 调 `/api/seller/claude/oauth/start`；后端建 PKCE 对 + state，返回 Anthropic 的 authorize URL
2. CLI 默认打开浏览器（`--no-browser` 跳过）；用户登录 Anthropic 并批准
3. Anthropic 重定向到他们的 callback 页，显示一串 `<code>#<state>`
4. **用户把这串复制粘回 CLI**
5. CLI 调 `/api/seller/claude/oauth/complete`；后端用 code+verifier 换 token 并 mint channel

比 device flow 多一次粘贴，但仍比手工找 `~/.claude/auth.json` 简单太多。session cookie 在 start 时由后端下发，complete 必须命中同一 session —— CLI 的 `http.CookieJar` 在进程内、每次调用隔离。

#### `add-oauth gemini` — 真一键 loopback OAuth

`everyapi seller add-oauth gemini --name … --models … [--no-browser] [--timeout 5m]`。Google 的 gemini-cli installed-app OAuth client 接受 `http://127.0.0.1:<port>/callback` 作为 `redirect_uri`，所以 **CLI 自己起 listener 接 callback** —— 用户在浏览器登录后无需任何粘贴。流程：

1. CLI 在随机 ephemeral port（`127.0.0.1:0`）起一个一次性 HTTP listener，路径固定 `/callback`
2. CLI 调 `/api/seller/gemini/oauth/start`，带 `redirect_uri = http://127.0.0.1:<port>/callback`；后端严格校验 redirect：loopback / port ≥ 1024 / scheme=http / path=/callback / 无 query/fragment/userinfo（防 SSRF + 防 redirect 劫持）
3. CLI 默认打开浏览器；用户在 Google 登录并同意
4. Google 带 `?code=…&state=…` 重定向到 CLI 的 listener
5. CLI 校验 state 匹配（防 stale flow / 伪造），调 `/api/seller/gemini/oauth/complete`
6. 后端用 code + 同一 redirect_uri 换 token 并 mint channel

与另外两个 provider 的对比：

| Provider | 体验 | 原因 |
|---|---|---|
| `codex` | 用户在浏览器输 6 位 user_code，CLI 自动轮询 | OpenAI device flow，无 redirect_uri |
| `claude` | 用户在浏览器登录，把 `code#state` 复制粘回 CLI | Anthropic 把 redirect_uri 写死成自家 callback URL |
| `gemini` | 用户在浏览器登录，关掉 tab 即完成 | Google 接受 loopback redirect |

`--timeout` 控制最长等待时间（默认 5 分钟）。超时时 CLI 退出并干净地关闭 listener。

### `everyapi edge <sub>` — BYO-GPU supplier agent 一键部署

把空闲 GPU 接入 EveryAPI 卖算力。CLI 把整套部署收口成一组命令 —— `register` / `list` / `start` / `status` / `logs` / `models` / `rename` / `pause` / `resume` / `stop` / `update` / `remove` —— 省掉 supplier 手抄 docker-compose、填 `.env`、来回搬 registration token 的步骤。常用路径是这 8 条：

```bash
everyapi auth login                              # 复用现有登录
everyapi edge register --name "rtx-4090"    # 调 /api/seller/edge/nodes 拿 node_id + token，落到 ~/.local/share/everyapi/edge/<id>/
everyapi edge start                         # 自动探测 NVIDIA / ROCm / Apple Silicon / CPU，docker compose up -d
everyapi edge models pull llama3.1:8b       # docker compose exec ollama ollama pull ...
everyapi edge status                        # 本地 docker compose ps + dashboard 端 online/offline
everyapi edge logs -f                       # 跟实时日志
everyapi edge update                        # docker compose pull && up -d
everyapi edge remove                        # down -v + 删本地目录 + 调 backend DELETE
```

`start` 通过 `text/template` 在运行时渲染 `docker-compose.yml`（**不是内嵌的静态 YAML**）—— 这样 container name 可按 node_id 命名空间化，单机多 node 不互踩；GPU passthrough 按 mode 条件渲染（NVIDIA = `deploy.resources.devices` + nvidia driver；ROCm = `/dev/kfd` + `/dev/dri` + `group_add: video`；macOS = 无 ollama 容器，agent 通过 `host.docker.internal` 连宿主机原生 ollama）。

凭证流：CLI 用现有的 `sk-everyapi-` Bearer 调 `POST /api/seller/edge/nodes` → 后端一次性返回 `registration_token`（之后只存 sha256，永不再回显）→ CLI 以 0600 写入 `~/.local/share/everyapi/edge/<id>/node.json` → 渲染进 compose 的 `EVERYAPI_REGISTRATION_TOKEN` env。**注册 token 不会写进任何 .env 文件**（避免 supplier 误 commit）。

依赖：`docker` + `docker compose v2`（v1 已 EOL，不支持）。macOS 需要 `brew install ollama && brew services start ollama`（Metal 加速在 docker 容器里跑不了）。

### `everyapi wallet topup` — 带反钓鱼暗号的充值跳转

`everyapi wallet topup` 打开 dashboard 充值页。跳转前走一层 docs §7-5 Layer 3 验证：

1. CLI 调后端 `POST /api/cli/jump-session`，拿到一个 session id + 4-emoji 暗号串（例如 `🌊 🦊 🍕 🚀`）
2. CLI 把 URL 和暗号都打到终端，提示用户「等一下页面顶部应该显示同样的暗号」
3. 用户按 Enter，CLI 用系统浏览器打开该 URL（带 `?jump_session=<id>`）
4. Dashboard 加载时调后端 `GET /api/cli/jump-session/:id/phrase`，拿到同一个暗号，并**在页面 header 显著展示**
5. 用户做视觉对比：暗号一致 → 真 EveryAPI；不一致或没显示 → 关掉 tab，可能是钓鱼

为什么这能挡钓鱼：暗号存在后端内存里，由随机 32-hex session id 索引；钓鱼站没有 auth path 拿不到，攻击者伪造的 `wallet/topup?jump_session=<id>` 也读不到。短 TTL（10 分钟）+ 单次使用（dashboard 取过一次后 session 即删除）进一步限制复用风险。

```bash
everyapi wallet topup                    # 默认打开浏览器
everyapi wallet topup --no-browser       # 只打印 URL，手动复制
```

### `everyapi auth status` — 当前余额 / 使用量 / 配额

```
$ everyapi auth status

  alice (alice@example.com)
  quota:     $12.34 remaining   $5.67 used
  requests:  1,234
  topup:     https://app.everyapi.ai/wallet
```

### `everyapi version update` — 自动执行升级

顶层没有 `everyapi update` 这个命令；CLI 自身的生命周期动作都挂在 `version` 下（`everyapi version update`、`everyapi version uninstall`）。

它会查 GitHub mirror 上的最新 release，跟当前版本比对，然后把升级交给真正安装了这个二进制的那套工具 —— Homebrew（`brew update && brew upgrade everyapi`）、`go install …@latest`，或已发布的安装脚本。一条命令搞定，不用复制粘贴。

```bash
$ everyapi version update

Update available: v0.2.0 → v0.2.1
Install method:   Homebrew

$ brew update
==> Updated Homebrew from ...

$ brew upgrade everyapi
==> Upgrading everyapi-ai/tap/everyapi
  v0.2.0 -> v0.2.1
...

Done. Run `everyapi version` to confirm.
```

为什么不直接换二进制？Homebrew 和 Go 自身的校验链（SHA / bottle signing / module checksum）比我们在 CLI 里重做的更扎实，而自替换正在运行的可执行文件在 Windows 上基本是地雷区。用安装脚本装的会就地替换 —— 但那是重跑已发布的安装器，它本来就把替换这件事做安全了。

Flag：
- `--check` —— 静默对比，给 CI / cron 用。已最新 exit 0，过时 exit 1，拿不到最新版本号 exit 2（原因打到 stderr）—— 网络抖动不该被读成「有新版本」：
  ```bash
  everyapi version update --check || echo "needs upgrade"
  ```
- `--dry-run` —— 打印将要执行的命令但不实际执行（用于检查）

### `everyapi settings` — CLI 偏好（语言等）

CLI 自带 8 国 i18n：英语、简体中文、繁體中文、日本語、한국어、Español、Deutsch、Français —— CLI 字符串按用户选择的语言渲染。后端 API 错误经 `Accept-Language` 头自动协商，覆盖同样这 8 种。

```bash
$ everyapi settings                          # 交互 picker：选语言
$ everyapi settings list                     # 看当前设置
$ everyapi settings set language zh          # 直接设
$ everyapi settings set language fr          # 法语同理
$ everyapi settings set terminal_mode tmux   # 交互式工具启动保持在 tmux 里
$ everyapi use codex -- resume               # 重新接上唯一存活的项目 tmux，或打开 Codex 的选择器
$ everyapi settings reset                    # 重置为默认（en + LANG 自动探测）
```

**Terminal mode**：第一次交互式 `everyapi use` 会问启动应留在原生终端还是跑在 tmux 里，然后把选择存为 `terminal_mode`。tmux 模式会把完整的 `everyapi use` 进程在一个 `everyapi-v3-*` 会话里重启，该会话由所选工具、工作区文件系统标识和一个随机 128 位启动标识共同确定，因此它的 connector、sanitizer、临时配置和目标工具都能在 detach 后存活；启动信息会打印确切的 `tmux attach -t <session>` 命令。裸 Codex `resume` 会先查这个标识：唯一一个存活的托管 agent pane 会按精确会话名重新校验并接回，而零个或多个匹配时回落到 Codex 正常的 resume 选择器，不做猜测。每次 tmux 启动前，CLI 只考虑严格生成的 `everyapi-v3-*`、`everyapi-v2-*` 或旧式 `everyapi-<pid>-<timestamp>` 会话，并且只有在一条原子 tmux 命令确认某会话唯一的 window 里只含唯一的、已死的 EveryAPI wrapper pane 时才移除它。存活的 detach agent、用户自建的普通 tmux 会话，以及任何含有用户新增 pane 或 window 的会话都会被保留；托管 pane 已死但用户新增 pane 仍存活的会话会被保留但不复用。每个被启动的客户端都能读到 `EVERYAPI_TERMINAL_MODE`、`EVERYAPI_TMUX_SESSION`、`EVERYAPI_TMUX_ATTACH_COMMAND`；Codex、Claude Code、OpenCode 和 Kilo 还会通过各自文档化的模型指令面收到同样的会话上下文，其中包含「不要创建嵌套 tmux 会话」这一条规则。其它客户端保留环境契约但不注入用户消息。已经在 tmux 里的启动不会嵌套，非交互启动始终保持原生。如果没有 tmux，首次运行的 picker 会禁用该选项；已有 tmux 偏好则会带着安装/切回的指引报错，而不是悄悄改变行为。

**自动探测**：未显式设置过时，CLI 启动时按 `EVERYAPI_LANG > LC_ALL > LC_MESSAGES > LANG` 顺序读环境变量。系统 locale 为 `zh_CN.UTF-8` / `ja_JP.UTF-8` / `fr_FR.UTF-8` 等会直接生效 —— 零配置。

**单次覆盖**：

```bash
EVERYAPI_LANG=zh everyapi auth status             # 这次以中文显示，不持久化
```

**翻译效果举例**（未登录错误，8 国 × 同一句）：

```
en    : Error: not logged in — run 'everyapi auth login' first
zh    : 错误: 未登录 — 先运行 'everyapi auth login'
zh-TW : 錯誤: 尚未登入 — 先執行 'everyapi auth login'
ja    : エラー: ログインしていません — まず 'everyapi auth login' を実行してください
ko    : 오류: 로그인되어 있지 않습니다 — 먼저 'everyapi auth login' 을 실행하세요
es    : Error: no has iniciado sesión — ejecuta primero 'everyapi auth login'
de    : Fehler: nicht angemeldet — führe zuerst 'everyapi auth login' aus
fr    : Erreur: non connecté — exécutez d'abord 'everyapi auth login'
```

设置文件落在 `~/.config/everyapi/settings.json`（与 `credentials.json` 同目录，但 mode `0644` —— 不含 secret）。

**改进翻译 / 加新语言**：见 [`internal/i18n/locales/README.md`](../internal/i18n/locales/README.md)。

## 配置文件

凭证落在 `~/.config/everyapi/credentials.json`（若设置了 `$XDG_CONFIG_HOME` 则走 `$XDG_CONFIG_HOME/everyapi/`），文件模式 `0600`。由 `everyapi auth login` 写入，其它命令读取。

> ⚠️ **Token 以明文存储**。文件 mode `0600` + `$HOME` 私有路径与 `gh auth` / `aws configure` 等业界 CLI 同模式，但**对家用电脑被偷 / 恶意软件威胁模型**，任何能读这个文件的进程都可以以你的身份调用 EveryAPI API（包括 MCP 工具，见下文 §钱路 friction step）。建议：
> - 不在共享 / 公共机器上 `everyapi auth login`
> - macOS 用户：考虑在启用 FileVault 前先 `everyapi auth logout`
> - Linux 用户：开启 home 目录加密（`ecryptfs` / LUKS）
> - 怀疑泄漏 → `everyapi auth logout` 立即清除本机凭证，并到 EveryAPI dashboard rotate API key
>
> Platform keychain backend（macOS Keychain / Windows DPAPI / Linux Secret Service）在规划中，尚未发布。

字段：

- `api_base` —— EveryAPI 网关 URL。默认 `https://api.everyapi.ai`。自托管用户 / 本地开发可在 `auth login` 时用 `--api-base` 覆盖。
- `access_token` —— 所有需鉴权的 API 调用使用的 bearer。
- `relay_key` —— relay API key（`sk-everyapi-…`），用于 `everyapi use` 的子进程 env。从 `/api/token/*` 拉取并缓存于此。
- `user_id` / `username` —— 缓存，使 `auth status` 在首次 API 往返前就能渲染身份行。

网关区域是 `settings.json` 里的 CLI 偏好：如果尚未设置，交互式登录会询问一次并保存选择。`everyapi settings set gateway_region cn` 会让官方网关流量走 `https://api-cn.everyapi.ai`；`global` 使用 `https://api.everyapi.ai`。自托管的 `--api-base` 仍然优先。

## 开发

在 CLI 源码目录（含本 README、`go.mod`、`Makefile` 的那个目录）下执行：

```bash
go test ./...
go run . auth status       # 对生产
go run . auth login --api-base http://localhost:8787   # 对本地后端
```

本地全平台交叉编译（跟 CI 用同一份配方）：

```bash
make cli-release           # 产物在 dist/（6 平台 × 1 二进制 = 6 个文件）
```

## MCP server (`everyapi mcp` 子命令)

`everyapi` 二进制**内建** [Model Context Protocol](https://modelcontextprotocol.io) server —— 以子命令形式暴露（`everyapi mcp` 读 stdin、写 stdout）。AI agent（Claude Code / Cursor / Codex CLI / 任意 MCP client）可以直接 invoke 它，**用户不必打开终端**。

> ⚠️ **MCP server 鉴权模型 + 暴露面**
>
> - **不开端口**：`everyapi mcp` 是纯 stdio JSON-RPC，由 host CLI fork。**不监听任何 socket / TCP 端口** —— 没有网络暴露面。
> - **直接读 `~/.config/everyapi/credentials.json`**：MCP server 没有自己的鉴权流程，能读 credentials 文件 = 能以你的身份调用所有暴露的 tool。任何能以你的用户身份跑进程的 MCP host 都拥有完全访问权。
> - **钱路 `everyapi_seller_withdraw` 带 friction step**：调用方必须传 `confirm: "yes"`，确保 AI agent 把转账动作在 UI 上呈现给人类，避免 silent drain。其它只读工具（status / topup / seller_list）无此要求。
>
> 不信任的 MCP host 不要装。

### 安装

跟 CLI 同一个 binary，装好 CLI 就有 MCP server：

```bash
make cli                                              # 本地编译，产物 ./bin/everyapi
# 或直接 go install:
go install github.com/everyapi-ai/everyapi-ai/v3@latest
```

### 接入 Claude Code

在 `~/.claude/settings.json` 加：

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

接入 Cursor、Codex CLI 或其它 MCP client 类似 —— 把 `command` 指向 `everyapi` 二进制，`args: ["mcp"]`。

### 鉴权前提

必须先在终端跑过至少一次 `everyapi auth login` —— MCP server 是后台进程，没有终端交互能力，无法自己跑 device-code 流程。它直接读 `~/.config/everyapi/credentials.json`；文件缺失时每个 tool 都返回 `isError: true` 的 "not logged in" 提示，引导用户去登录。

### 暴露的 tools（15 个）

| Tool | 入参 | 作用 |
|---|---|---|
| `everyapi_status` | 无 | 当前余额 / 已用 / 请求数 |
| `everyapi_topup` | 无 | 返回 web 充值 URL |
| `everyapi_seller_list` | 无 | 列出 marketplace seller channels |
| `everyapi_seller_withdraw` | `{confirm: "yes", quota?: int}` | 把 seller_quota 转入主余额；**必须传 `confirm: "yes"`**（钱路 friction） |
| `everyapi_seller_eligibility` | 无 | 只读的挂载门槛清单（marketplace 是否开放、账号是否正常、邮箱是否验证、账号年龄、历史用量、channel 上限）。在向用户要 key *之前*先调它 |
| `everyapi_seller_add_key` | `{name, type, keys[], models, key_remarks?[], remark?}` | 用明文 API key 挂载 seller channel —— `everyapi seller add-key` 的 MCP 孪生。只允许传用户在对话里明确给出的 key |
| `everyapi_seller_add_oauth_codex_start` | `{name, models}` | 启动 Codex / ChatGPT 设备授权流，返回 `user_code` + `verification_uri` + `flow_id` |
| `everyapi_seller_add_oauth_codex_poll` | `{flow_id}` | 查 Codex 授权状态。`pending`/`slow_down` 继续轮询；`authorized` 返回 channel id；`expired`/`denied` 终止 |
| `everyapi_seller_add_oauth_claude_start` | `{name, models}` | 启动 Anthropic OAuth 流，返回 `authorize_url`。用户在浏览器登录后会拿到一串 `<code>#<state>` |
| `everyapi_seller_add_oauth_claude_complete` | `{input}` | 提交上一步用户粘贴的 `<code>#<state>` 串，mint channel |
| `everyapi_edge_list` | 无 | 列出 BYO-GPU edge 节点：id、名称、在线状态、配对 channel、最后上报时间、已装模型 |
| `everyapi_edge_status` | `{node_id: int}` | 单个节点详情 —— 暂停标记、agent 版本、GPU 型号 / 数量 / 显存、已装模型 |
| `everyapi_edge_remove` | `{node_id: int, confirm: "yes"}` | 删除节点（若它是最后一个，连带删掉配对的 channel）；**必须传 `confirm: "yes"`**（破坏性操作 friction） |
| `everyapi_admin_marketplace_status` | 无 | 读取部署级 `marketplace.enabled` 开关。需要管理员角色 |
| `everyapi_admin_marketplace_set` | `{enabled: bool, confirm: "yes"}` | 对整个部署开启 / 关闭 marketplace；**必须传 `confirm: "yes"`**。关闭后已有节点与 channel 继续服务 |

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

Gemini OAuth（loopback flow）**不在 MCP 暴露** —— loopback listener 的生命周期与跨 tool 调用的生命周期不匹配。Gemini 仍走 CLI 的 `everyapi seller add-oauth gemini`。

### 手动 smoke

```bash
make cli
./bin/everyapi mcp <<'EOF'
{"jsonrpc":"2.0","id":1,"method":"initialize"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"everyapi_status","arguments":{}}}
EOF
```

应该看到 3 行 JSON 响应：initialize 结果、15 个 tool 的列表、status 文本（或 not-logged-in 的 isError）。

## 这个二进制还**不**包含什么

当前**仍未实现**的（按重要性排序，后续 release 增量补，不破坏已有 v1 surface）：

- ⚠️ OS 级 code signing（macOS notarization / Windows Authenticode）—— 目前靠 sigstore cosign keyless + SHA256SUMS 双层校验，每个 GitHub Release 都附带，Homebrew 安装时自动验证
- ❌ Platform keychain backend —— token 仍明文存盘（mode 0600）

原列于此、**现已落地**的（勿再当未实现）：

- ✅ Local sanitizer proxy —— 命令是 `everyapi proxy {start,stop,status,configure}`（不是 `everyapi start`/`everyapi configure`）；引擎 + 6 个内置 detector + 自定义 regex，并已集成进 `everyapi use`
- ✅ Seller OAuth onboarding 三家 provider（codex device / claude paste / gemini loopback）
- ✅ QR sign-in 主路径 —— `auth login` 走 device-code **+ QR 主路径**，`--no-qr` 兜底
- ✅ 防钓鱼分层 —— 暗号字符串（`everyapi wallet topup`）、PKCE/state strict-check、cert pinning 均已落地；cert pinning 为 **report-only**（匹配静默 / mismatch 告警 / 永不拒连），产品决策定为「只告警不强制」

## 报告漏洞

请见 [`SECURITY.md`](../SECURITY.md)。
