> 🌐 [English](../README.md) · [简体中文](README.zh-CN.md) · **日本語** · [한국어](README.ko.md) · [Español](README.es.md) · [Deutsch](README.de.md) · [Français](README.fr.md)

# `everyapi` CLI

[EveryAPI](https://everyapi.ai) AI API ゲートウェイの buyer onboarding CLI。監査済みの単一レジストリ経由で、対応コーディングエージェントを **1 分以内に** 起動します。

ステータス：**コアフローは出荷済み** —— buyer onboarding、seller コマンド（plain-key + 3 プロバイダーの OAuth）、sanitizer proxy、QR sign-in メインパス、アンチフィッシング層はすべて実装済みです。未実装なのは OS レベルのコード署名とプラットフォーム keychain バックエンドだけです（末尾の「このバイナリにまだ含まれていないもの」を参照）。

## インストール

**macOS（Homebrew）：**

```bash
brew tap everyapi-ai/tap && brew install everyapi
```

以降のアップグレードは先に `brew update` を実行してください（実行しないと `brew upgrade everyapi` はキャッシュされた formula を使い、新しいリリースがあっても "already installed" と報告します）：

```bash
brew update && brew upgrade everyapi
```

**Linux / macOS（インストールスクリプト）：**

```bash
curl -fsSL https://dl.everyapi.ai/install.sh | bash
```

スクリプトは OS + arch を自動判定し、対応する `everyapi_{os}_{arch}.tar.gz` をダウンロードし、SHA256 を検証して `~/.local/bin`（root 実行時は `/usr/local/bin`）にインストールします。[cosign](https://github.com/sigstore/cosign) が入っていれば keyless 署名も検証します —— `--require-signature` を渡すとこの検証が必須になります（CI / サプライチェーンに敏感な環境で推奨）。

1 コマンドで世界中どこでも：スクリプトは実行時にダウンロード元を選択します —— 到達可能なら GitHub Releases、GitHub が遅い / ブロックされている場合は中国本土ミラー —— なので同じ 1 行が中国国内でも海外でも動きます。`EVERYAPI_DOWNLOAD_BASE` を設定すると特定のミラーを強制できます。

よく使うフラグ：

```bash
curl -fsSL https://dl.everyapi.ai/install.sh | bash -s -- --version v0.2.2     # バージョン固定
curl -fsSL https://dl.everyapi.ai/install.sh | bash -s -- --prefix /usr/local  # プレフィックス指定
curl -fsSL https://dl.everyapi.ai/install.sh | bash -s -- --require-signature  # cosign 検証失敗で中止
curl -fsSL https://dl.everyapi.ai/install.sh | bash -s -- --force              # 同一バージョンを再インストール
```

アップグレードは同じコマンドを再実行するだけです。スクリプトは最新リリースタグを解決し、新しいものがあればバイナリをその場で置き換えます。すでに目標バージョンなら `already at vX.Y.Z — nothing to do` で終了します（セットアップスクリプト / dotfiles に入れても安全）。`--force` を渡すと上書き再インストールします（整合性確認や破損ファイルの復旧に便利）。スクリプト自体もこのリポジトリの [`install.sh`](../install.sh) で公開しているので、先にダウンロードして読んでから実行することもできます。

**Go ユーザー（`go install`）：**

```bash
go install github.com/everyapi-ai/everyapi-ai/v3@latest
```

**Windows（PowerShell）：**

```powershell
irm https://dl.everyapi.ai/install.ps1 | iex
```

シェルスクリプトと同じ流れです —— 最新タグを解決し、`everyapi_windows_amd64.zip` + `SHA256SUMS` をダウンロードし、ハッシュ（`PATH` に cosign があれば署名も）を検証し、`everyapi.exe` を `%LOCALAPPDATA%\everyapi\bin` にインストールしてユーザー `PATH` に追加します。バージョン固定などのオプションを渡すには、先にスクリプトを実体化してください：`& ([scriptblock]::Create((irm https://dl.everyapi.ai/install.ps1))) -Version v0.2.2`。このスクリプトもリポジトリの [`install.ps1`](../install.ps1) にあります。

**Windows（手動）：** [Releases ページ](https://github.com/everyapi-ai/everyapi-ai/releases/latest) から `everyapi_windows_amd64.zip`（または他の成果物）を取得し、`SHA256SUMS` と照合してからバイナリを `%PATH%` に配置してください。

## コマンド

TTY 上で引数なしに `everyapi` を実行すると、同じコマンド群を対象にした対話ランチャーが開きます。`everyapi help` は同じ内容をテキストで表示します。

| コマンド | 用途 |
|---|---|
| `everyapi auth <sub>` | サインイン / サインアウトとセッション状態（`login` / `logout` / `status`） |
| `everyapi wallet <sub>` | チャージ（アンチフィッシング phrase 確認付き）、支払い履歴、支払い方法 |
| `everyapi checkin <sub>` | 今日のデイリー付与クォータを受け取る／今月のカレンダーを表示 |
| `everyapi account <sub>` | プロフィール、2FA、アフィリエイトコード、サブスクリプションプラン |
| `everyapi use <tool>` | env を設定してサードパーティ CLI に exec（EveryAPI を指す） |
| `everyapi token <sub>` | relay API キーの管理（list / create / key / revoke / switch / …） |
| `everyapi models <sub>` | モデルカタログ：list / pricing / groups |
| `everyapi stats <sub>` | 使用量、リクエストログ、モデル別パフォーマンス、上流の健全性 |
| `everyapi market <sub>` | 需要投稿、係争、不正利用の報告 |
| `everyapi inbox <sub>` | アプリ内通知とダイレクトメッセージ |
| `everyapi seller <sub>` | Marketplace セラー側コマンド（list / setup / withdraw / add-key / add-oauth） |
| `everyapi edge <sub>` | BYO-GPU supplier agent のワンコマンド展開（register / start / status / logs / models / rename / pause / resume / stop / update / remove） |
| `everyapi artifacts <sub>` | 自己完結型 HTML レポートの公開と管理（`share` / `list` / `update` / `delete`） |
| `everyapi events` | ライブイベントストリーム（SSE）を購読 |
| `everyapi feedback` | バグ報告や機能要望をチームへ送信 |
| `everyapi proxy <sub>` | ローカル sanitizer proxy（`start` / `stop` / `status` / `configure`） |
| `everyapi computer <sub>` | Accessibility 経由でローカル macOS アプリのウィンドウを読み取り・操作 |
| `everyapi mcp` | MCP server として動作（stdin/stdout JSON-RPC） |
| `everyapi doctor` | セルフチェック：資格情報、ゲートウェイ、sanitizer、インストール済みツール |
| `everyapi settings <sub>` | CLI 設定の表示 / 変更（言語、ターミナルモード） |
| `everyapi admin` | オペレーターコンソール —— 管理者アカウントにのみ表示 |
| `everyapi version [update\|uninstall]` | ビルドバージョン；更新の確認と実行；CLI のアンインストール |
| `everyapi help` | コマンド一覧を表示 |

### `everyapi computer <sub>` —— ローカル macOS の computer use

macOS 版 CLI は実行中のアプリとウィンドウを列挙し、上限付きの Accessibility スナップショットを返し、セマンティック操作や座標操作を実行できます。この機能はローカル専用で、`everyapi mcp` には登録されません。Linux と Windows のビルドは明示的に `unsupported_platform` を返します。

macOS では `everyapi computer` が、独立してコード署名された小さなヘルパーアプリ（`clients/desktop/native/computer-use-macos` からビルドされる `EveryAPI Computer Use.app`）をローカル Unix ソケット経由で駆動します。未インストールなら初回利用時に自動でダウンロードして起動しますが、EveryAPI Connect が同梱版をすでにインストール済みの場合は、この CLI は二重にダウンロードせずそれを再利用します。ヘルパーはスクリーンショット対応を false として報告します。macOS はこの provider を通じて信頼できる公開のウィンドウ単位キャプチャ識別子を提供していないためで、重なった別アプリが写り込みうる画面領域キャプチャで代用することは決してありません。

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

`everyapi computer permissions --json` を実行し、システム設定 > プライバシーとセキュリティ > アクセシビリティ で **EveryAPI Computer Use** にアクセシビリティ権限を付与してください——`everyapi` や `osascript`、ターミナルにではありません。ヘルパーは独自の bundle 識別子を持つ独立した署名済みアプリなので、この付与はこの一機能に限定されます。マシン上のすべての AppleScript や JXA スクリプトまで許可することはなく、CLI とヘルパーの更新をまたいでも維持されます。`permissions` はアクセシビリティを直接報告し、Automation は `unknown` として報告します。この provider は System Events に依存せず、別途 Automation の事前チェックを行わないためです。

要素インデックスは最新の `get-app-state` スナップショット由来で、2 分で失効します。ウィンドウはインデックス（`--window-index`）で選びますが、内部的には画面上で CoreGraphics が割り当てる実際のウィンドウ単位 id で識別し（利用できる場合）、最小化されたウィンドウではスナップショット内の合成 id にフォールバックします。いずれの場合も provider は内部フィンガープリントで観測可能な変化を検出しますが、公開の Accessibility 属性では、属性が同一の置き換わったウィンドウやコントロールが同じネイティブインスタンスであることを証明できません。キャッシュは `~/.config/everyapi/computer-use/state/` 配下にプライベート権限で、不透明なアプリケーション・プロセス・ウィンドウ・パス・role・frame・アクション名・フィンガープリントのデータだけを保存します。`app_stale`、`element_stale`、`window_stale` の後は新しいスナップショットを取り直してください。GUI 操作が成功した後にベストエフォートの状態更新が失敗しても、その操作は成功のままです。その場合 JSON にはリトライ可能な操作エラーではなく `refreshError` が含まれます。操作を引き渡した後にヘルパー呼び出しが中断されたり不正なレシートを返したりした場合、`action_outcome_unknown` は操作がすでに発生している可能性を意味します。リトライするか判断する前に状態を更新してください。

既知のターミナルアプリ、パスワードマネージャー、キーチェーンアクセス、パスワード、システム設定、EveryAPI Connect のメンテナンス済みリストは、多層防御の摩擦としてブロックされます。bundle ID によるブロックは網羅的なアプリ分類器ではありません。リスト外のアプリ、ターミナルを内蔵したエディタ、ブラウザ、改名・新規リリースされたアプリは同等の機能を露出しうるからです。実際の信頼境界は、明示的な `--app` の対象、macOS の TCC、そして呼び出し側の同一ユーザー権限のままです。読み取ったテキストは出力前に端末制御シーケンスを除去され、資格情報がスキャンされます。入力または設定するテキストが組み込みのシークレット検出器に一致した場合は拒否されます。通常のテキストをシェル履歴に残さないために `--text-stdin` と `--value-stdin` を優先してください。

### `everyapi use <tool>` —— サードパーティ CLI に exec（EveryAPI ゲートウェイを指す）

この CLI を入れる最大の理由です。対応するコーディングクライアントを EveryAPI 経由で設定・起動します。ネイティブ統合（`antigravity`、`librefang`）は各自の認証パスを保ち、コピーされた relay key を受け取ることはありません。

```bash
everyapi use claude            # Claude Code → EveryAPI
everyapi use codex             # OpenAI Codex CLI → EveryAPI
everyapi use opencode          # OpenCode → プロセススコープの EveryAPI provider
everyapi use gemini            # Google Gemini CLI → EveryAPI
everyapi use antigravity       # Antigravity（ネイティブ Google 認証とルーティング）
everyapi use aider             # Aider → EveryAPI（モデルを選択）
everyapi use goose             # Goose CLI → EveryAPI（モデルを選択）
everyapi use crush             # Crush CLI → 隔離された EveryAPI モデルカタログ
everyapi use cline             # Cline CLI → ライフサイクル連動の provider 設定
everyapi use openclaw          # OpenClaw ローカル TUI → 隔離された EveryAPI カタログ
everyapi use continue          # Continue CLI → 隔離された assistant 設定
everyapi use kilo              # Kilo Code CLI → プロセススコープの provider 設定
everyapi use pi                # Pi coding agent → 隔離されたモデルカタログ
everyapi use pi-web            # Pi Web ブラウザ UI → 永続 models.json への provider 登録
everyapi use vibe              # Mistral Vibe → 隔離された汎用 provider
everyapi use copilot           # GitHub Copilot CLI → 公式のプロセススコープ BYOK
everyapi use droid             # Factory Droid → 隔離されたランタイム設定
everyapi use openhands         # OpenHands CLI → 明示的なプロセス限定 env 上書き
everyapi use forge             # ForgeCode → 隔離された OpenAI 互換セッション
everyapi use llxprt            # LLxprt Code → 隔離された home + 固定ランタイムフラグ
everyapi use grok              # xAI Grok Build → EveryAPI
everyapi use qwen-code         # Alibaba Qwen Code → EveryAPI（モデルを選択）
everyapi use kimi-code         # Moonshot Kimi Code → EveryAPI（モデルを選択）
everyapi use hermes            # Nous Research Hermes Agent → EveryAPI（モデルを選択）
everyapi use librefang         # LibreFang 起動（ネイティブ EveryAPI 資格情報プロセス）
everyapi use open-webui        # Open WebUI サーバー → EveryAPI を OpenAI バックエンドに
everyapi use deepseek-harness  # DeepSeek Harness web UI（dsh）→ provider と資格情報を生成
everyapi use hermes --model gpt-5.1      # モデルを固定してピッカーをスキップ
everyapi use claude                      # 既定で透過：api.anthropic.com のまま
everyapi use codex                       # api.openai.com のまま
everyapi use antigravity                 # Google 公式 Origin のまま
everyapi use claude --transparent=false  # 透過を無効化：ゲートウェイ Base URL + relay key を注入
everyapi use                             # 引数なし → インストール済みツールの対話ピッカー
```

ツールごとに流儀が違いますが、CLI が覚えています：

| ツール | EveryAPI への向け方 |
|---|---|
| claude | env：`ANTHROPIC_BASE_URL`、`ANTHROPIC_AUTH_TOKEN`；ゲートウェイ探索によるライブ互換モデル |
| codex | env：`OPENAI_API_KEY` + セッション保持用の永続 EveryAPI `CODEX_HOME` + ライフサイクル連動の `--profile` と key スコープのモデルカタログ（codex は `OPENAI_BASE_URL` ではなく設定でルーティング） |
| gemini | env：`GEMINI_API_KEY`、`GOOGLE_GEMINI_BASE_URL`、`GEMINI_MODEL`；隔離された auth-mode 設定オーバーレイ |
| antigravity | ネイティブ Antigravity ランチャー（`agy`） |
| aider | OpenAI 互換 env に加えて `openai/<model>` の LiteLLM モデル名前空間 |
| goose | `GOOSE_PROVIDER=openai`、`GOOSE_MODEL`、`OPENAI_API_KEY`、`OPENAI_BASE_URL` |
| crush | プロセススコープの `CRUSH_GLOBAL_CONFIG`；key は env から参照、モデルカタログはライブ生成 |
| cline | ライフサイクル連動の `CLINE_PROVIDER_SETTINGS_PATH`、終了後に削除 |
| openclaw | ローカル埋め込み TUI、プロセススコープ設定と env 由来の SecretRef |
| continue | ライフサイクル連動の `CONTINUE_GLOBAL_DIR/config.yaml`；Continue の secret 参照は env 由来 |
| kilo | プロセススコープの `KILO_CONFIG_CONTENT`；OpenCode 互換 provider、key は env 由来 |
| pi | `models.json` と選択モデル設定を含む隔離 `PI_CODING_AGENT_DIR`。起動前の `PI_CODING_AGENT_DIR`（既定 `~/.pi/agent`）にある `{extensions,skills,prompts,themes}` は絶対パスで読み込み |
| pi-web | `providers.everyapi` を*永続*の `PI_CODING_AGENT_DIR/models.json`（既定 `~/.pi/agent`）へマージ。セッション、プロジェクトの信頼設定、選択モデル、Models パネル自身の編集がすべて残ります。relay key は env 参照のままでディスクに書きません |
| vibe | 隔離された `VIBE_HOME/config.toml`；`api_key_env_var` を持つ汎用 provider |
| copilot | 公式 `COPILOT_PROVIDER_*` BYOK 環境；wire API は選択モデルの能力に従う |
| droid | 公式 `--settings` のランタイム専用ファイル。`custom:EveryAPI-0` モデル 1 つと env 由来の key |
| openhands | `--override-with-envs` とプロセス限定の `LLM_API_KEY`、`LLM_BASE_URL`、`LLM_MODEL` |
| forge | 隔離された `FORGE_CONFIG`；OpenAI 互換の provider/model を設定とプロセス env に固定 |
| llxprt | 隔離されたアプリケーション home と予約済みの `--provider openai`、`--baseurl`、`--model` ランタイムフラグ |
| grok | env：`XAI_API_KEY`、`GROK_MODELS_BASE_URL`；隔離された `GROK_HOME`；フィルタ済みライブモデル探索 |
| qwen-code | env：`OPENAI_API_KEY`、`OPENAI_BASE_URL`、`OPENAI_MODEL`；プロセススコープの `QWEN_HOME` ユーザー設定と固定 `--auth-type=openai` |
| kimi-code | env：`KIMI_MODEL_API_KEY`、`KIMI_MODEL_BASE_URL`、`KIMI_MODEL_PROVIDER_TYPE`、`KIMI_MODEL_NAME`；生成されたモデルエイリアス付きの隔離 `KIMI_CODE_HOME` |
| hermes | 生成された `HERMES_HOME/config.yaml`（名前付き custom provider、`base_url`、インライン `api_key`）；フィルタ済みライブモデル探索 |
| librefang | ネイティブ `librefang start`。デーモンを detach してターミナルを返します（`librefang stop` で終了）。LibreFang はリクエストごとに現在の EveryAPI 資格情報を解決します |
| open-webui | `open-webui serve` として起動し、`OPENAI_API_BASE_URLS`、`OPENAI_API_KEYS`、`ENABLE_PERSISTENT_CONFIG=false` を渡してプロセス env を保存済み設定より優先させます。`DATA_DIR` は `~/.open-webui` に固定 |
| deepseek-harness | 公式の `dsh web` UI。`$DSH_HOME/settings.yaml`（既定 `~/.dsh`、mode `0700`）に `llm-pi-ai.providers.everyapi` を生成し、key は `0600` の `.credentials.yaml` に保存 |

どのツールがどの変数名を読むのか、`/v1` を付けるべきか、どの auth ヘッダー形式か —— もう調べる必要はありません。

**relay key の選択**：`--group` なしで起動すると、アカウントの auto-group key —— 到達可能なすべてのグループにルーティングできる唯一の key —— を解決し、`credentials.json` にキャッシュします。auto key を持たないアカウント（またはそのグループを使えなくなった可能性のある tier）は、有効な最新の key にフォールバックします。別の key を既定に固定するには `everyapi token switch`、別プールで一度だけ起動するには `--group <id>` を渡します。グループ上書きがこのキャッシュに書かれることはありません。どの key を使っているかが以下のカタログを決めます：1 つのグループに固定された key は、そのグループのモデルしか見えません。

以前の起動でキャッシュされた key はそのまま使われ続けます —— この参照は意図的にオフラインなので、勝手に選び直すことはありません。`/model` に 1 グループ分のモデルしか出ない場合は、`everyapi token switch` を実行して一度 `Auto` を選んでください。

**モデル選択**：起動時に EveryAPI は選択された relay key/group で利用可能なライブカタログを取得し、非互換のメディア/embedding プロトコルを除外し、そのスナップショットをルーティング先クライアントのネイティブセレクタに注入します。Claude Code、Codex、Qwen Code、Kimi Code では `/model` を、Grok では `/model`/`models` を、Hermes では `hermes model` を使ってください。Claude 以外のモデル ID は内部的に Claude 互換エイリアスで表現されますが、表示と上流送信は実 ID で行われます。

`ModelEnv` 契約を持つツール（Gemini、Aider、Goose、Crush、Cline、OpenClaw、Continue、Kilo、Pi、Vibe、GitHub Copilot CLI、Factory Droid、OpenHands、ForgeCode、LLxprt、Hermes、Qwen Code、Kimi Code）は EveryAPI のピッカーを開きます。`--model <id>` を渡すとスキップできます。非対話実行では EveryAPI が決定的に最初の互換モデルを使います。素の claude/codex/grok は自前の起動モデル挙動を保ちます。`antigravity` は Google 認証でネイティブ `agy` を起動し、`librefang` は自社の EveryAPI 資格情報プロセスを使います。`pi-web`、`open-webui`、`deepseek-harness` はブラウザ UI を提供します。EveryAPI が provider と互換カタログ全体を事前に登録するので、モデルはターミナルのピッカーではなくその UI 内で選びます。

**reasoning level**：モデルの次に、`everyapi use codex` と `everyapi use pi` はどの reasoning level で起動するかを尋ね、その答えを次回のために記憶します —— 一度尋ねたら以降は確認なしで再利用され、後述の安全設定と同じ扱いです。両クライアントで条件が異なるのは、知っている情報が違うためです。Codex は自前のバンドルカタログがそのモデルに対して公開する段階（`low` … `ultra`。モデルごとに異なり、`gpt-5.6-sol` は `ultra` まで、`gpt-5.5` は `xhigh` まで）を読み、選択を `model_reasoning_effort` として受け取ります。この件でゲートウェイに問い合わせることはないので、Codex が知らないモデルにはこのステップが出ません。Pi は custom provider 向けのモデル別テーブルを持たないため、ゲートウェイがそのモデルは effort を受け付けると確認済みの場合（`/v1/models` の `supports_thinking`）にのみ表示されます。選択肢は `off` … `high` で、`defaultThinkingLevel` として受け取られます。現在のモデルが提供しない記憶済みレベルは固定せずに破棄されます。この機能の出荷後の初回起動では、カーソルは Codex が永続 home にすでに持っていた effort 上から始まるので、既定のまま確定すれば何も変わりません。両クライアントのセッション内コントロール —— Codex の `/model`、pi の shift+tab —— はそのまま残り、起動をまたぐ選択はランチャーが保持します。Codex の生成 profile と Pi の隔離 home は終了時に削除されるためです。

プロバイダー名は CLI 名ではありません：これらのベンダーの公式クライアントには `qwen-code` / `kimi-code` を使い、プロバイダーのモデルは対応クライアントのライブモデルカタログから選択してください。

**hermes の設定隔離**：`everyapi use hermes` は `HERMES_HOME` を `~/.config/everyapi/sessions` 配下のプロセススコープディレクトリへリダイレクトします。資格情報を含む設定とライブ proxy URL は終了時に削除され、別の key/group と衝突しません。安全な設定として保持されるのは最後に選んだモデル ID だけです。個人の `~/.hermes` は変更されません。生成された設定は EveryAPI を名前付き custom provider として登録するため、`hermes model` が OpenRouter に落ちることなくモデルを探索・切り替えできます。素の `hermes` は対話チャットを開きます。ターミナル UI が必要なら `everyapi use hermes -- --tui` を使ってください。

**grok の設定隔離**：`everyapi use grok` は `GROK_HOME` を `~/.config/everyapi/grok-home` にリダイレクトします。これによりキャッシュされた xAI ブラウザセッションが EveryAPI relay key を上書きするのを防ぎ、EveryAPI 経由セッションを素の `grok` と分離します。Grok 固有のフラグは `--` の後に渡してください。例：`everyapi use grok -- --model grok-4.5`。

**Qwen/Kimi の設定隔離**：ルーティングされた起動ごとに `~/.config/everyapi/sessions` 配下のプロセススコープ home が与えられ、子プロセス終了時に削除されるため、並行する key/group が互いのカタログや loopback URL を上書きすることはありません。Qwen の実際のシステム設定は変更されず、管理者優先度も保たれます。管理者設定またはワークスペース設定が `modelProviders.openai` を定義していてライブの EveryAPI カタログを隠す場合、古い/非互換のモデルを黙って表示するのではなく、対処可能な競合として起動を中止します。

> ⚠️ **サブプロセス env の安全上の注意**：上記の環境変数にはあなたの relay API key が含まれます。サードパーティ CLI は debug / verbose モードで env をログに書くことがあります —— `everyapi use` の前に、有効化する debug フラグが `*_TOKEN` / `*_API_KEY` を漏らさないか確認してください。debug ログを共有する前に `sed -i 's/sk-everyapi-[A-Za-z0-9]*/REDACTED/g'` を実行してください。

#### 透過 Connector（既定）

透過モードは、サードパーティの Base URL を設定する代わりに、対応クライアントをベンダー公式の API Origin に留めます。対応するすべてのツールで既定です。無効化するには `--transparent=false` を渡してください。CLI はランダムな loopback ポートで一時的な HTTP CONNECT proxy を起動し、実行ごとに CA を生成します（その秘密鍵はメモリ内のみ）。子プロセスには proxy URL、公開 CA バンドル、秘密でないプレースホルダー資格情報だけが渡されます。登録済みモデルルートはローカルで復号され、実際の relay key を付けて EveryAPI にリレーされます。他の HTTPS ホストは素の CONNECT パススルーです。保護されたモデルプレフィックス配下の未知パスはブロックされ、リレー失敗時にベンダーへフォールバックすることはありません。

Claude Code と Codex CLI で検証済みで、既定で有効になるのもこの 2 つです。ネイティブの Antigravity と LibreFang は connector をバイパスします。その他の登録済みツールは文書化された注入/設定パスを使うため、非対応ツールに明示的に `--transparent` を渡すと明確に失敗します。

`--sanitize` は透過モードと競合せず組み合わさります：connector は sanitizer 経由でリレーするため（子プロセス → connector → sanitizer → ゲートウェイ）、マスキングと Claude のリカバリ応答ガードはどちらの起動パスでも有効です。

代理変数が `ALL_PROXY` だけの場合、透過モードは辞退され注入パスにフォールバックします —— Go の proxy 解決は `ALL_PROXY` を読まないため、connector がそれを尊重できません。透過モードを維持するには `HTTPS_PROXY`（socks5 も可。net/http がネイティブに接続します）を設定してください。

このモードは実験的で、意図的にプロセススコープです：

- 傍受されるクライアント側は現在 HTTP/1.1 を使い、通常の JSON/SSE リクエストに対応します（ゲートウェイの HTTP/2 応答は HTTP/1.1 に変換されます）。クライアント側 HTTP/2、HTTP/3/QUIC、WebSocket、証明書ピン留めクライアント、`HTTPS_PROXY` を無視するクライアントは対象外です；
- Codex の組み込み OpenAI provider は Responses WebSocket を一度プローブします。Connector が HTTP 426 を返すため、Codex はリトライ予算を消費せず即座に HTTPS/SSE にフォールバックします。Codex はこの失敗プローブのログ行を出力することがあります；
- Claude Code は秘密でないプレースホルダーを API-key 認証として扱うため、`ANTHROPIC_BASE_URL` が公式の `https://api.anthropic.com` Origin のままでも claude.ai connectors は無効になります。透過モードが回避するのはサードパーティ Origin の検出であり、API-key 認証を claude.ai の OAuth ログインのように振る舞わせることはできません；
- システム CA をインストールせず、管理者権限も不要で、`everyapi use` の既定挙動も変えません；
- 検出不能ではありません：クライアントは proxy 変数、ローカル証明書チェーン、ソケット、タイミング、応答の差異を調べられます；
- Connector は復号後のモデル内容を見ます。CA 署名鍵は書き出しもアップロードもされず、公開 CA ファイルは終了時に削除されます；
- relay key は子プロセス環境と生成されたクライアント設定には含まれませんが、既存の `~/.config/everyapi/credentials.json` は同じ OS ユーザーで動く任意のプロセスから読めます。透過モードは資格情報注入の隔離であって、敵対的な子プロセスに対するサンドボックスではありません。

### `everyapi auth login` —— Device Authorization Grant + QR サインイン

Device Authorization Grant（RFC 8628 形式）+ docs §7-5 Layer 1「デバイス間 QR サインイン」を使います：

1. CLI がセッションを作成し、**ターミナルに QR を描画 + 短いコードと URL を出力**
2. スマートフォンで QR をスキャン（または EveryAPI にサインイン済みのブラウザで URL を開く）—— QR 内の URL には `?code=USR-789` が含まれており、dashboard がコードを自動入力するので、ユーザーは Approve を押すだけです
3. CLI が access token を受け取り、`~/.config/everyapi/credentials.json`（mode 0600）に保存します

```bash
everyapi auth login                                    # 本番。既定で QR 描画 + ブラウザ起動
everyapi settings set gateway_region cn               # 以降のコマンドで中国高速化ゲートウェイを使用
everyapi auth login --api-base http://localhost:8787   # ローカル開発 / セルフホスト
everyapi auth login --no-browser                       # ブラウザを自動で開かない（QR をスキャン）
everyapi auth login --no-qr                            # QR を描画しない（非 UTF-8 ターミナル / パイプ）
```

QR のターミナル描画例（Unicode 半ブロック文字、約 18-20 行）：

```
█▀▀▀▀▀█  ▀▀ ▄  █▀▀▀▀▀█
█ ███ █  ▀▄█▀  █ ███ █
█ ▀▀▀ █ ▄ ▀ █▀ █ ▀▀▀ █
▀▀▀▀▀▀▀ █▄█▄█▄ ▀▀▀▀▀▀▀
... (実際の QR は verification_uri?code=USR-789 をエンコード)
```

これがより強いアンチフィッシング経路である理由：

- ユーザーは**新しいデバイスでパスワードを入力しない** → フィッシングサイトが資格情報を奪う機会がない
- ユーザーは**見慣れないブラウザページにリダイレクトされない** → web リダイレクト型フィッシングの面が消える
- CLI が悪意ある fork で偽 QR を出したとしても、スキャン後の承認ページは本物の everyapi.ai dashboard（ユーザーがすでにサインイン済みのデバイスから起動）であり、見慣れないコードをユーザーが Approve することはありません

docs §7-5 の残りの層（cert pinning / phrase 文字列 / PKCE OAuth）は独立した PR で実装済みです（cert pinning は report-only。enforce は製品判断として出荷しません）。

### `everyapi seller <sub>` —— marketplace セラー側サブコマンド

dashboard のチャネル登録・出金フローをターミナルに持ち込み、スクリプト化された onboarding を可能にします。チャネル登録前に `seller setup` が資格（アカウント有効 / メール確認済み / アカウント年齢 / 消費履歴 / チャネル上限）を確認し、失敗したゲートは**ユーザーが key を入力する前に**列挙されます。送信後に 422 で気付くのを避けるためです。

```bash
everyapi seller list                          # 登録済みチャネル一覧
everyapi seller withdraw                      # 保留中のセラー収益をすべてメイン残高へ移動
everyapi seller withdraw --quota 1000         # 部分送金（DB 単位）
everyapi seller add-key   --type claude --name 'my-pro' \
                        --key 'sk-ant-...' --models 'claude-3-opus,claude-3-sonnet'
everyapi seller add-oauth codex --name 'my-chatgpt' --models 'gpt-4'
                                            # ワンクリック OAuth：CLI が device flow を開始し、ユーザーが
                                            # ブラウザで user_code を入力、token がチャネルに着地
everyapi seller add-oauth claude --name 'my-claude' --models 'claude-3-opus,claude-3-sonnet'
                                            # paste flow：CLI が Anthropic の認可ページを開き、ユーザーが
                                            # callback に表示された code#state をターミナルへ貼り戻す
everyapi seller add-oauth gemini --name 'my-gemini' --models 'gemini-1.5-pro'
                                            # 真のワンクリック loopback：CLI がランダムポートで listener を起動し、
                                            # Google が code を CLI へ直接送るので貼り付け不要
everyapi seller setup                         # 対話ウィザード：まず資格を確認し、add-key を案内
```

#### `add-key` —— マルチキー バックアップ プール

`--key` は繰り返し指定でき、同一チャネルに N 個の等価な資格情報をバックアッププールとして登録できます（B2、PRODUCT §4.5）。主 key が 401/403 を返すと、バックエンドが自動的に次へフェイルオーバーします。`--key-remark` も繰り返し可能で、`--key` と位置で対応します（i 番目の `--key-remark` が i 番目の `--key` のラベルになり、後で dashboard 上の識別に使えます）。OAuth blob はバックアッププールに入れられません —— 単一 key チャネルのままです。

```
everyapi seller add-key   --type claude --name 'claude-pool' \
                        --models 'claude-3-opus' \
                        --key 'sk-ant-primary' --key-remark 'primary' \
                        --key 'sk-ant-backup'  --key-remark 'team backup'
```

`add-key` の `--type` はエイリアス（`openai` / `claude` / `gemini` / `codex` / `vertex` / `aws` / `xai` / `deepseek`）または数値 id を受け付けます。登録は marketplace の資格条件（アカウント有効、メール確認済み、消費履歴、チャネル上限）に従い、CLI は 3 つの入口（`add-key` / `add-oauth` / `setup`）すべてで、他の処理より先に失敗したチェックリストを列挙します。

#### `add-oauth codex` —— ワンクリック OAuth（device flow）

`everyapi seller add-oauth codex --name 'my-chatgpt' --models 'gpt-4'` は Codex / ChatGPT の RFC 8628 相当の device authorization flow を進めます —— セラーは**トークン文字列に一切触れません**：

1. CLI が `/api/seller/codex/device/start` を呼び、短い `user_code` と `verification_uri` を受け取る
2. CLI が既定で `https://auth.openai.com/codex/device` をブラウザで開く（`--no-browser` でスキップ）。ユーザーがブラウザで `user_code` を入力して認可を完了
3. CLI が `/api/seller/codex/device/poll` をポーリング。認可されるとバックエンドがチャネルを作成し、OAuth token をチャネルの `key` フィールドに書き込む
4. 出力：チャネル id + 紐づいた ChatGPT のメールアドレス

認可 cookie はプロセス内 `http.CookieJar` が管理し、永続化されません —— device flow の状態は短命かつプロセス束縛で、脅威モデルと整合します。

#### `add-oauth claude` —— paste-and-submit OAuth

`everyapi seller add-oauth claude --name … --models …`。Anthropic の OAuth provider は自社側で `redirect_uri` を `https://console.anthropic.com/oauth/code/callback` にハードコードしているため、CLI は localhost listener で callback を受け取れません。流れ：

1. CLI が `/api/seller/claude/oauth/start` を呼ぶ。バックエンドが PKCE ペア + state を作り、Anthropic の authorize URL を返す
2. CLI が既定でブラウザを開く（`--no-browser` でスキップ）。ユーザーが Anthropic にサインインして承認
3. Anthropic が `<code>#<state>` 文字列を表示する callback ページへリダイレクト
4. **ユーザーがその文字列を CLI に貼り戻す**
5. CLI が `/api/seller/claude/oauth/complete` を呼ぶ。バックエンドが code+verifier を token と交換してチャネルを作成

device flow より貼り付けが 1 手増えますが、手作業で `~/.claude/auth.json` を探すよりはるかに簡単です。セッション cookie は start でバックエンドが発行し、complete は同一セッションに当てる必要があります —— CLI の `http.CookieJar` はプロセス内で、呼び出しごとに隔離されています。

#### `add-oauth gemini` —— 真のワンクリック loopback OAuth

`everyapi seller add-oauth gemini --name … --models … [--no-browser] [--timeout 5m]`。Google の gemini-cli installed-app OAuth クライアントは `http://127.0.0.1:<port>/callback` を `redirect_uri` として受け付けるため、**CLI が自前の listener で callback を受けます** —— ユーザーはブラウザでサインインするだけで、貼り付けは不要です。流れ：

1. CLI がランダムな ephemeral port（`127.0.0.1:0`）で使い捨て HTTP listener を起動、パスは固定 `/callback`
2. CLI が `redirect_uri = http://127.0.0.1:<port>/callback` を付けて `/api/seller/gemini/oauth/start` を呼ぶ。バックエンドはリダイレクトを厳密検証：loopback / port ≥ 1024 / scheme=http / path=/callback / query・fragment・userinfo なし（SSRF とリダイレクト乗っ取りを防止）
3. CLI が既定でブラウザを開く。ユーザーが Google にサインインして同意
4. Google が `?code=…&state=…` を CLI の listener にリダイレクト
5. CLI が state の一致を検証し（古いフロー / 偽造を防止）、`/api/seller/gemini/oauth/complete` を呼ぶ
6. バックエンドが code + 同一 redirect_uri を token と交換してチャネルを作成

他の 2 プロバイダーとの比較：

| Provider | UX | 理由 |
|---|---|---|
| `codex` | ユーザーがブラウザで 6 桁の user_code を入力、CLI が自動ポーリング | OpenAI の device flow、redirect_uri なし |
| `claude` | ユーザーがブラウザでサインインし、`code#state` を CLI に貼り戻す | Anthropic が redirect_uri を自社 callback URL にハードコード |
| `gemini` | ユーザーがブラウザでサインインしてタブを閉じれば完了 | Google が loopback リダイレクトを受け付ける |

`--timeout` で待機時間を制限します（既定 5 分）。タイムアウト時は CLI が終了し、listener をきれいに閉じます。

### `everyapi edge <sub>` —— BYO-GPU supplier agent ワンコマンド展開

遊休 GPU を EveryAPI 経由で販売できるようにします。CLI は展開作業を 1 つのコマンド群 —— `register` / `list` / `start` / `status` / `logs` / `models` / `rename` / `pause` / `resume` / `stop` / `update` / `remove` —— に凝縮し、docker-compose の手写し、`.env` の記入、登録トークンの持ち回りを不要にします。通常の流れは次の 8 コマンドです：

```bash
everyapi auth login                              # 既存ログインを再利用
everyapi edge register --name "rtx-4090"    # /api/seller/edge/nodes を呼んで node_id + token を取得し、~/.local/share/everyapi/edge/<id>/ に保存
everyapi edge start                         # NVIDIA / ROCm / Apple Silicon / CPU を自動判定し docker compose up -d
everyapi edge models pull llama3.1:8b       # docker compose exec ollama ollama pull ...
everyapi edge status                        # ローカルの docker compose ps + dashboard 側の online/offline
everyapi edge logs -f                       # ログを追尾
everyapi edge update                        # docker compose pull && up -d
everyapi edge remove                        # down -v + ローカルディレクトリ削除 + backend DELETE
```

`start` は `text/template` で実行時に `docker-compose.yml` をレンダリングします（**埋め込みの静的 YAML ではありません**）—— これによりコンテナ名を node_id で名前空間化でき、1 ホスト上の複数ノードが衝突しません。GPU パススルーはモードごとに条件付きでレンダリングされます（NVIDIA = `deploy.resources.devices` + nvidia driver、ROCm = `/dev/kfd` + `/dev/dri` + `group_add: video`、macOS = ollama コンテナなしで agent が `host.docker.internal` 経由でホストのネイティブ ollama に接続）。

資格情報の流れ：CLI が既存の `sk-everyapi-` Bearer で `POST /api/seller/edge/nodes` を呼ぶ → バックエンドが `registration_token` を一度だけ返す（以後は sha256 のみ保存、再表示しない）→ CLI が 0600 で `~/.local/share/everyapi/edge/<id>/node.json` に書く → compose の `EVERYAPI_REGISTRATION_TOKEN` env にレンダリング。**登録トークンはいかなる .env ファイルにも書かれません**（供給者が誤ってコミットしないように）。

要件：`docker` + `docker compose v2`（v1 は EOL で非対応）。macOS では `brew install ollama && brew services start ollama`（Metal アクセラレーションは docker コンテナ内で動きません）。

### `everyapi wallet topup` —— anti-phishing phrase 付きのチャージリダイレクト

`everyapi wallet topup` は dashboard のチャージページを開きます。リダイレクト前に docs §7-5 Layer 3 の検証を通ります：

1. CLI がバックエンドの `POST /api/cli/jump-session` を呼び、セッション id + 4 絵文字の phrase 文字列（例：`🌊 🦊 🍕 🚀`）を受け取る
2. CLI が URL と phrase の両方をターミナルに表示し、「まもなくページ上部に同じ phrase が出るはず」と伝える
3. ユーザーが Enter を押すと、CLI がシステムブラウザで URL を開く（`?jump_session=<id>` 付き）
4. dashboard は読み込み時にバックエンドの `GET /api/cli/jump-session/:id/phrase` を呼び、同じ phrase を受け取って**ページヘッダーに目立つ形で表示**
5. ユーザーが目視比較：一致 → 本物の EveryAPI、不一致または非表示 → タブを閉じる（フィッシングの可能性）

これがフィッシングを防ぐ理由：phrase はランダムな 32-hex のセッション id をキーとしてバックエンドのメモリ上に存在します。フィッシングサイトにはそれを取得する認証経路がなく、攻撃者が偽造した `wallet/topup?jump_session=<id>` からも phrase は読めません。短い TTL（10 分）+ 単回使用（dashboard が一度取得するとセッションは削除）で再利用リスクをさらに抑えます。

```bash
everyapi wallet topup                    # 既定でブラウザを開く
everyapi wallet topup --no-browser       # URL を表示するだけ（手動でコピー）
```

### `everyapi auth status` —— 現在の残高 / 使用量 / クォータ

```
$ everyapi auth status

  alice (alice@example.com)
  quota:     $12.34 remaining   $5.67 used
  requests:  1,234
  topup:     https://app.everyapi.ai/wallet
```

### `everyapi version update` —— アップグレードを自動実行

トップレベルに `everyapi update` はありません。CLI 自身のライフサイクル操作は `version` の下にあります（`everyapi version update`、`everyapi version uninstall`）。

GitHub ミラー上の最新リリースを確認し、現在のバージョンと比較したうえで、そのバイナリを実際にインストールした仕組み —— Homebrew（`brew update && brew upgrade everyapi`）、`go install …@latest`、または公開済みのインストールスクリプト —— にアップグレードを委ねます。1 コマンドで完了、コピペ不要です。

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

なぜバイナリを直接差し替えないのか？Homebrew と Go の検証チェーン（SHA / bottle 署名 / module checksum）は CLI 内で作り直すどんな仕組みより堅牢で、実行中の実行ファイルの自己置換は Windows では地雷原だからです。インストールスクリプト経由の場合はその場で置き換わりますが、それは公開済みインストーラーを再実行するためで、置き換えは元々安全に行われます。

フラグ：
- `--check` —— 静かに比較。CI / cron 向け。最新なら exit 0、古ければ exit 1、最新バージョンを取得できなければ exit 2（理由は stderr）—— ネットワークの一時的な失敗が「更新あり」と読まれてはいけません：
  ```bash
  everyapi version update --check || echo "needs upgrade"
  ```
- `--dry-run` —— 実行されるコマンドを表示するだけで実行しない（確認用）

### `everyapi settings` —— CLI 設定（言語など）

CLI は 8 言語の i18n を同梱します：英語、簡体中国語、繁体中国語、日本語、韓国語、スペイン語、ドイツ語、フランス語 —— CLI の文字列はユーザーが選んだ言語で描画されます。バックエンド API のエラーは `Accept-Language` ヘッダーで自動ネゴシエートし、同じ 8 言語をカバーします。

```bash
$ everyapi settings                          # 対話ピッカー：言語を選択
$ everyapi settings list                     # 現在の設定を表示
$ everyapi settings set language zh          # 直接設定
$ everyapi settings set language fr          # フランス語も同様
$ everyapi settings set terminal_mode tmux   # 対話ツールの起動を tmux 内に保つ
$ everyapi use codex -- resume               # 唯一生存中のプロジェクト tmux に再接続、または Codex のピッカーを開く
$ everyapi settings reset                    # 既定に戻す（en + LANG 自動判定）
```

**Terminal mode**：最初の対話的な `everyapi use` は、起動をネイティブターミナルに留めるか tmux で走らせるかを尋ね、その選択を `terminal_mode` として保存します。tmux モードでは `everyapi use` プロセス全体を `everyapi-v3-*` セッション内で再起動します。このセッションは選択したツール、ワークスペースのファイルシステム同一性、ランダムな 128 ビットの起動同一性で識別されるため、connector、sanitizer、一時設定、対象ツールのすべてが detach を生き延びます。起動メッセージには正確な `tmux attach -t <session>` コマンドが表示されます。素の Codex `resume` はまずこの同一性を探します：生存する管理 agent ペインが 1 つならその正確なセッション名で再検証して再接続し、0 個または複数ならば推測せず通常の Codex resume ピッカーにフォールバックします。tmux 起動のたびに、CLI は厳密に生成された `everyapi-v3-*`、`everyapi-v2-*`、旧式 `everyapi-<pid>-<timestamp>` セッションのみを候補とし、単一の tmux コマンドで「唯一の window に唯一かつ死んだ EveryAPI ラッパーペインしかない」ことを原子的に再検証できた場合にのみ削除します。生存中の detach 済み agent、ユーザーが作った通常の tmux セッション、ユーザーが追加したペインや window を含むセッションはすべて保持されます。管理ペインは死んだがユーザー追加ペインが生きているセッションは保持されますが再利用されません。起動された各クライアントは `EVERYAPI_TERMINAL_MODE`、`EVERYAPI_TMUX_SESSION`、`EVERYAPI_TMUX_ATTACH_COMMAND` を参照できます。Codex、Claude Code、OpenCode、Kilo はさらに、文書化されたモデル指示面を通じて同じセッションコンテキストを受け取り、その中には「入れ子の tmux セッションを作らない」というルールも含まれます。他のクライアントは環境契約のみを保持し、ユーザーメッセージは注入されません。すでに tmux 内での起動は入れ子にならず、非対話起動は常にネイティブのままです。tmux が使えない場合、初回のピッカーはその選択肢を無効化します。既存の tmux 設定がある場合は、黙って挙動を変えるのではなく、インストール / 切り戻しの案内付きで失敗します。

**自動判定**：明示的に設定していない場合、CLI は起動時に `EVERYAPI_LANG > LC_ALL > LC_MESSAGES > LANG` の順で環境変数を読みます。システムロケールが `zh_CN.UTF-8` / `ja_JP.UTF-8` / `fr_FR.UTF-8` などならすぐに反映されます —— 設定不要です。

**一回限りの上書き**：

```bash
EVERYAPI_LANG=zh everyapi auth status             # この呼び出しだけ中国語表示。永続化しない
```

**翻訳例**（未ログインエラー、8 言語 × 同一行）：

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

設定は `~/.config/everyapi/settings.json` に保存されます（`credentials.json` と同じディレクトリですが mode は `0644` —— 秘密情報を含みません）。

**翻訳の改善 / 言語追加**：[`internal/i18n/locales/README.md`](../internal/i18n/locales/README.md) を参照してください。

## 設定ファイル

資格情報は `~/.config/everyapi/credentials.json`（`$XDG_CONFIG_HOME` が設定されていれば `$XDG_CONFIG_HOME/everyapi/`）に、ファイルモード `0600` で保存されます。`everyapi auth login` が書き込み、他のすべてのコマンドが読み取ります。

> ⚠️ **トークンは平文で保存されます**。ファイルモード `0600` + 非公開の `$HOME` パスは `gh auth` / `aws configure` など業界 CLI の慣行と同じですが、**自宅マシンの盗難 / マルウェアの脅威モデルでは**、このファイルを読めるプロセスはあなたとして EveryAPI API を呼べます（MCP ツールを含む —— 後述の §money-path friction step を参照）。推奨事項：
> - 共有 / 公共マシンで `everyapi auth login` しない
> - macOS ユーザー：FileVault を有効化する前に `everyapi auth logout` を検討
> - Linux ユーザー：ホームディレクトリ暗号化（`ecryptfs` / LUKS）を有効化
> - 漏洩が疑われる場合 → `everyapi auth logout` で直ちにローカル資格情報を消去し、EveryAPI dashboard で API key をローテーション
>
> プラットフォーム keychain バックエンド（macOS Keychain / Windows DPAPI / Linux Secret Service）は計画中で未出荷です。

フィールド：

- `api_base` —— EveryAPI ゲートウェイ URL。既定は `https://api.everyapi.ai`。セルフホストユーザー / ローカル開発は `auth login` の `--api-base` で上書きできます。
- `access_token` —— 認証が必要なすべての API 呼び出しで使う bearer。
- `relay_key` —— relay API key（`sk-everyapi-…`）。`everyapi use` のサブプロセス env に使います。`/api/token/*` から取得してここにキャッシュされます。
- `user_id` / `username` —— `auth status` が最初の API 往復前に ID 行を描画できるようにするためのキャッシュ。

ゲートウェイのリージョンは `settings.json` 内の CLI 設定です：未設定なら対話ログイン時に一度尋ねて選択を保存します。`everyapi settings set gateway_region cn` は公式ゲートウェイのトラフィックを `https://api-cn.everyapi.ai` に切り替え、`global` は `https://api.everyapi.ai` を使います。セルフホスト用のカスタム `--api-base` は依然として優先されます。

## 開発

CLI のソースディレクトリ（この README、`go.mod`、`Makefile` があるディレクトリ）で：

```bash
go test ./...
go run . auth status       # 本番に対して
go run . auth login --api-base http://localhost:8787   # ローカルバックエンドに対して
```

全プラットフォーム向けのローカルクロスコンパイル（CI と同じレシピ）：

```bash
make cli-release           # 成果物は dist/（6 プラットフォーム × 1 バイナリ = 6 ファイル）
```

## MCP server (`everyapi mcp` サブコマンド)

`everyapi` バイナリは [Model Context Protocol](https://modelcontextprotocol.io) server を**内蔵**しています —— サブコマンドとして公開されます（`everyapi mcp` が stdin を読み stdout に書きます）。AI エージェント（Claude Code / Cursor / Codex CLI / 任意の MCP クライアント）が直接呼び出せ、**ユーザーがターミナルを開く必要はありません**。

> ⚠️ **MCP server の認証モデル + 露出面**
>
> - **ポートを開かない**：`everyapi mcp` は純粋な stdio JSON-RPC で、ホスト CLI が fork します。**ソケット / TCP ポートを一切 listen しません** —— ネットワーク面の露出はありません。
> - **`~/.config/everyapi/credentials.json` を直接読む**：MCP server には独自の認証フローがなく、資格情報ファイルを読めること = 公開されたすべてのツールをあなたとして呼べることです。あなたのユーザー権限でプロセスを実行できる MCP ホストは完全なアクセス権を持ちます。
> - **money path の `everyapi_seller_withdraw` には friction step があります**：呼び出し側は `confirm: "yes"` を渡す必要があり、AI エージェントが送金操作を UI 上で人間に提示することを保証し、無言の資金流出を防ぎます。他の読み取り専用ツール（status / topup / seller_list）にはこの要件はありません。
>
> 信頼できない MCP ホストはインストールしないでください。

### インストール

CLI と同じバイナリです。CLI を入れれば MCP server も使えます：

```bash
make cli                                              # ローカルビルド、./bin/everyapi を生成
# または go install で：
go install github.com/everyapi-ai/everyapi-ai/v3@latest
```

### Claude Code への接続

`~/.claude/settings.json` に追加：

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

Cursor、Codex CLI、その他 MCP クライアントへの接続も同様です —— `command` を `everyapi` バイナリに向け、`args: ["mcp"]` を指定します。

### 認証の前提

ターミナルで少なくとも一度 `everyapi auth login` を実行しておく必要があります —— MCP server はターミナル対話能力を持たないバックグラウンドプロセスなので、device-code フローを自分で実行できません。`~/.config/everyapi/credentials.json` を直接読み、存在しない場合はすべてのツールが `isError: true` の "not logged in" メッセージを返し、ログインへ誘導します。

### 公開しているツール（15 個）

| Tool | 入力 | 用途 |
|---|---|---|
| `everyapi_status` | なし | 現在の残高 / 使用量 / リクエスト数 |
| `everyapi_topup` | なし | web チャージ URL を返す |
| `everyapi_seller_list` | なし | marketplace のセラーチャネル一覧 |
| `everyapi_seller_withdraw` | `{confirm: "yes", quota?: int}` | seller_quota をメイン残高へ移動；**`confirm: "yes"` 必須**（money-path friction） |
| `everyapi_seller_eligibility` | なし | マウント要件の読み取り専用チェックリスト（marketplace の開放状況、アカウント状態、メール検証、アカウント年齢、過去の利用実績、チャネル上限）。key を求める*前*に呼びます |
| `everyapi_seller_add_key` | `{name, type, keys[], models, key_remarks?[], remark?}` | 平文の API key でセラーチャネルをマウント —— `everyapi seller add-key` の MCP 版。会話の中でユーザーが明示的に渡した key 以外を送ってはいけません |
| `everyapi_seller_add_oauth_codex_start` | `{name, models}` | Codex / ChatGPT のデバイス認可フローを開始し、`user_code` + `verification_uri` + `flow_id` を返す |
| `everyapi_seller_add_oauth_codex_poll` | `{flow_id}` | Codex の認可状態を確認。`pending`/`slow_down` は継続ポーリング、`authorized` はチャネル id を返す、`expired`/`denied` は終了 |
| `everyapi_seller_add_oauth_claude_start` | `{name, models}` | Anthropic の OAuth フローを開始し `authorize_url` を返す。ユーザーはブラウザでサインイン後 `<code>#<state>` 文字列を受け取る |
| `everyapi_seller_add_oauth_claude_complete` | `{input}` | 前ステップでユーザーが貼り付けた `<code>#<state>` 文字列を送信し、チャネルを作成 |
| `everyapi_edge_list` | なし | BYO-GPU edge ノード一覧：id、名前、オンライン状態、ペアのチャネル、最終確認時刻、インストール済みモデル |
| `everyapi_edge_status` | `{node_id: int}` | 単一ノードの詳細 —— 一時停止フラグ、agent バージョン、GPU 型番 / 台数 / VRAM、インストール済みモデル |
| `everyapi_edge_remove` | `{node_id: int, confirm: "yes"}` | ノードを削除（最後の 1 台ならペアのチャネルも）；**`confirm: "yes"` 必須**（破壊的操作の friction） |
| `everyapi_admin_marketplace_status` | なし | デプロイ全体の `marketplace.enabled` フラグを読み取る。管理者ロールが必要 |
| `everyapi_admin_marketplace_set` | `{enabled: bool, confirm: "yes"}` | デプロイ全体で marketplace を開放 / 閉鎖；**`confirm: "yes"` 必須**。閉鎖後も既存ノードとチャネルは稼働を続けます |

**OAuth ツールの使い方パターン**（AI エージェントが会話内でこう進めます）：

```
User: ChatGPT Plus のセラーチャネルを追加して。名前は my-chatgpt、models は gpt-4
AI    → everyapi_seller_add_oauth_codex_start({name: "my-chatgpt", models: "gpt-4"})
       ← "chatgpt.com/codex で USR-789 を入力して、終わったら教えてください"
User: ブラウザで完了しました
AI    → everyapi_seller_add_oauth_codex_poll({flow_id: "..."})
       ← "status=pending、あと数秒待ってください"
[authorized になるまでポーリング継続]
       ← "status=authorized — channel #314 mounted"

User: Claude Pro のも追加して。my-claude / claude-3-opus
AI    → everyapi_seller_add_oauth_claude_start({...})
       ← "[URL] で認可を完了し、code#state 文字列を教えてください"
User: code-abc123#state-xyz
AI    → everyapi_seller_add_oauth_claude_complete({input: "code-abc123#state-xyz"})
       ← "Channel #315 mounted"
```

Gemini OAuth（loopback flow）は **MCP では公開していません** —— loopback listener の生存期間がツール呼び出しをまたぐライフサイクルと合わないためです。Gemini は引き続き CLI の `everyapi seller add-oauth gemini` を使います。

### 手動スモークテスト

```bash
make cli
./bin/everyapi mcp <<'EOF'
{"jsonrpc":"2.0","id":1,"method":"initialize"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"everyapi_status","arguments":{}}}
EOF
```

3 行の JSON レスポンスが出るはずです：initialize の結果、15 個のツール一覧、status テキスト（または未ログインの isError）。

## このバイナリにまだ**含まれていない**もの

現時点で**未実装**のもの（重要度順。以降のリリースで v1 サーフェスを壊さずに順次追加します）：

- ⚠️ OS レベルのコード署名（macOS notarization / Windows Authenticode）—— 現状は sigstore cosign keyless + SHA256SUMS の二層検証に依存。両方が全 GitHub Release に同梱され、Homebrew が自動で検証します
- ❌ プラットフォーム keychain バックエンド —— トークンは依然としてディスク上に平文保存（mode 0600）

以前ここに記載していたが**すでに出荷済み**のもの（未実装として扱わないでください）：

- ✅ ローカル sanitizer proxy —— コマンドは `everyapi proxy {start,stop,status,configure}`（`everyapi start`/`everyapi configure` ではありません）。エンジン + 6 個の組み込み detector + カスタム regex、`everyapi use` に統合済み
- ✅ 3 プロバイダー全部のセラー OAuth onboarding（codex device / claude paste / gemini loopback）
- ✅ QR サインインのメインパス —— `auth login` は device-code **+ QR をメインパス**として使い、`--no-qr` がフォールバック
- ✅ アンチフィッシング層 —— phrase 文字列（`everyapi wallet topup`）、PKCE/state の厳格チェック、cert pinning すべて実装済み。cert pinning は **report-only**（一致時は無言 / 不一致は警告 / 接続拒否はしない）で、「警告のみ、強制はしない」という製品判断です

## 脆弱性報告

[`SECURITY.md`](../SECURITY.md) を参照してください。
