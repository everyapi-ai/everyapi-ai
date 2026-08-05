> 🌐 [English](../README.md) · [简体中文](README.zh-CN.md) · **日本語** · [한국어](README.ko.md) · [Español](README.es.md) · [Deutsch](README.de.md) · [Français](README.fr.md)

# `everyapi` CLI

[EveryAPI](https://everyapi.ai) AI API ゲートウェイの buyer onboarding CLI。Claude Code、Codex、Antigravity、Grok Build、Qwen Code、Kimi Code を**1 分以内に**起動できます。

ステータス：**コアフロー実装済み** —— buyer onboarding、seller コマンド（plain-key + OAuth 3 プロバイダー）、sanitizer proxy、QR sign-in メインパス、anti-phishing レイヤーすべて実装済み。残る未実装は OS レベルの code signing と platform keychain backend のみ（末尾「このバイナリにまだ含まれていないもの」を参照）。

## インストール

```bash
brew tap everyapi-ai/tap && brew install everyapi
```

その後のアップグレード —— 必ず先に `brew update`：

```bash
brew update && brew upgrade everyapi
```

`brew update` を先に実行しないと、`brew upgrade everyapi` はキャッシュ済み formula を使い、新リリースがあっても「already installed」と表示されます。

## コマンド

| コマンド | 役割 |
|---|---|
| `everyapi login` | このデバイスで EveryAPI にサインイン |
| `everyapi logout` | このデバイスの認証情報を消去 |
| `everyapi status` | 残高、使用量、クォータを表示 |
| `everyapi topup` | 充填ページを開く（anti-phishing phrase 検証付き） |
| `everyapi use <tool>` | env を設定してサードパーティ CLI に exec（EveryAPI を指す） |
| `everyapi seller <sub>` | Marketplace セラー側コマンド（list / withdraw / add-key / setup） |
| `everyapi edge <sub>` | BYO-GPU supplier agent の 1 コマンド展開（register / start / status / logs / models / stop / update / remove） |
| `everyapi mcp` | MCP server として実行（stdin/stdout JSON-RPC） |
| `everyapi update` | 新バージョン確認、インストール方法に応じたアップグレードコマンドを表示 |
| `everyapi version` | ビルドバージョンを表示 |
| `everyapi help` | ヘルプ |

### `everyapi use <tool>` —— サードパーティ CLI に exec（EveryAPI ゲートウェイを指す）

この CLI は、対応するコーディングクライアントを EveryAPI 経由で設定・起動します。`gemini` エントリは認証済みの Antigravity CLI を起動します。

```bash
everyapi use claude         # Claude Code → EveryAPI
everyapi use codex          # OpenAI Codex CLI → EveryAPI
everyapi use gemini         # Antigravity を起動
everyapi use grok           # xAI Grok Build → EveryAPI
everyapi use qwen-code      # Alibaba Qwen Code → EveryAPI
everyapi use kimi-code      # Moonshot Kimi Code → EveryAPI
everyapi use                # 引数なし → インストール済みツールから対話的に選択
```

各ツールの env 規約は異なり、CLI が代わりに覚えてくれます：

| ツール | 設定する環境変数 |
|---|---|
| claude | `ANTHROPIC_BASE_URL`、`ANTHROPIC_AUTH_TOKEN` |
| codex | `OPENAI_BASE_URL`、`OPENAI_API_KEY` |
| gemini | Antigravity ネイティブランチャー (`agy`) |
| grok | `XAI_API_KEY`、`GROK_MODELS_BASE_URL` |
| qwen-code | `OPENAI_API_KEY`、`OPENAI_BASE_URL`、`OPENAI_MODEL`；分離された `QWEN_HOME` |
| kimi-code | `KIMI_MODEL_API_KEY`、`KIMI_MODEL_BASE_URL`、`KIMI_MODEL_NAME`；分離された `KIMI_CODE_HOME` |

どの変数名を読むのか、`/v1` を付けるべきか、どの auth ヘッダー形式かを毎回調べる必要はもうありません。

> ⚠️ **サブプロセス env のセキュリティ注意**：上記の環境変数には relay API key が含まれます。サードパーティ CLI の debug / verbose モードは env をログに書き出す可能性があるため —— `everyapi use` 前に有効化する debug フラグが `*_TOKEN` / `*_API_KEY` を漏らさないことを確認してください。debug ログを共有する前に `sed -i 's/sk-everyapi-[A-Za-z0-9]*/REDACTED/g'` を実行してください。

### `everyapi login` —— Device Authorization Grant + QR ログイン

Device Authorization Grant（RFC 8628 形態）+ docs §7-5 Layer 1「デバイス間 QR ログイン」を使用：

1. CLI がセッションを作成し、**ターミナル QR を表示 + 短いコードと URL を表示**
2. スマホでスキャン（または既に EveryAPI にサインイン済みのブラウザで URL を開く）—— QR 内の URL は `?code=USR-789` を含み、dashboard がコードを自動入力、ユーザーは Approve をクリックするだけ
3. CLI が access token を取得し、`~/.config/everyapi/credentials.json` に保存（mode 0600）

```bash
everyapi login                                    # 本番、デフォルトで QR 表示 + 自動ブラウザ起動
everyapi login --api-base http://localhost:8787   # ローカル開発 / セルフホスト
everyapi login --no-browser                       # ブラウザ自動起動なし（QR でスキャン）
everyapi login --no-qr                            # QR 非表示（非 UTF-8 ターミナル / パイプ用）
```

ターミナル QR 描画サンプル（Unicode 半ブロック文字、約 18-20 行）：

```
█▀▀▀▀▀█  ▀▀ ▄  █▀▀▀▀▀█
█ ███ █  ▀▄█▀  █ ███ █
█ ▀▀▀ █ ▄ ▀ █▀ █ ▀▀▀ █
▀▀▀▀▀▀▀ █▄█▄█▄ ▀▀▀▀▀▀▀
... (実際の QR は verification_uri?code=USR-789 をエンコード)
```

なぜこれが強力な anti-phishing パスなのか：

- ユーザーが**新デバイスでパスワードを入力する必要なし** → phishing サイトが credential を騙し取る隙がない
- ユーザーが**ブラウザを見知らぬページに飛ばされる必要なし** → web リダイレクト phishing 面が消える
- 仮に CLI が悪意ある fork で偽 QR を生成しても、スキャン後の確認ページは本物の everyapi.ai dashboard（ユーザーがサインイン済みのデバイス上のもの）—— 見覚えのないコードをユーザーは Approve しません

docs §7-5 のその他 layer（cert pinning / phrase 文字列 / PKCE OAuth）は各々独立 PR で実装済み（cert pinning は report-only、enforce は製品判断で行わない）。

### `everyapi seller <sub>` —— marketplace セラー側サブコマンド

dashboard の channel mount / 出金操作をターミナルに移植し、scripted onboarding を容易にします。channel をマウントする前に `seller setup` が eligibility（アカウント有効 / メール認証済み / アカウント年齢 / 消費履歴 / channel 上限）をチェックし、失敗した gate を**キー入力前に**先にリストして、フォーム送信後 422 を回避します。

```bash
everyapi seller list                          # マウント済み channel を一覧
everyapi seller withdraw                      # pending seller 収益すべてをメイン残高に移動
everyapi seller withdraw --quota 1000         # 部分送金（DB 単位）
everyapi seller add-key   --type claude --name 'my-pro' \
                        --key 'sk-ant-...' --models 'claude-3-opus,claude-3-sonnet'
everyapi seller add-oauth codex --name 'my-chatgpt' --models 'gpt-4'
                                            # ワンクリック OAuth：CLI が device flow を開始、ユーザーがブラウザで
                                            # user_code 入力、token が自動で channel に着地
everyapi seller add-oauth claude --name 'my-claude' --models 'claude-3-opus,claude-3-sonnet'
                                            # paste flow：CLI が Anthropic 認可ページを開き、ユーザーが
                                            # callback ページに表示される code#state をターミナルに貼り戻す
everyapi seller add-oauth gemini --name 'my-gemini' --models 'gemini-1.5-pro'
                                            # 真のワンクリック loopback：CLI がランダムポート listener を起動、
                                            # Google がコードを直接 CLI に送信、貼り付け不要
everyapi seller setup                         # 対話ウィザード：先に eligibility 確認、次に add-key を案内
```

#### `add-key` —— マルチキーバックアップ プール

`--key` は繰り返し可能で、N 個の等価な認証情報を同一 channel にバックアップ プールとしてマウントできます（B2、PRODUCT §4.5）。プライマリキーが 401/403 を返すと backend が自動で次のキーに failover します。`--key-remark` も繰り返し可能で、位置で `--key` と対応します（i 番目の `--key-remark` が i 番目の `--key` のラベルになり、後の dashboard 識別に使用）。OAuth blob はバックアップ プールには入れられず、単一キー channel に留まります。

```
everyapi seller add-key   --type claude --name 'claude-pool' \
                        --models 'claude-3-opus' \
                        --key 'sk-ant-primary' --key-remark 'primary' \
                        --key 'sk-ant-backup'  --key-remark 'team backup'
```

`add-key` の `--type` は alias（`openai` / `claude` / `gemini` / `codex` / `vertex` / `aws` / `xai` / `deepseek`）または数値 id を受け付けます。マウントは marketplace eligibility（アカウント有効、メール認証済み、消費履歴、channel 数上限）の制限を受け、CLI は `add-key` / `add-oauth` / `setup` の 3 つの入り口すべてで先に eligibility をチェックし、失敗時にチェックリストを表示します。

#### `add-oauth codex` —— ワンクリック OAuth（device flow）

`everyapi seller add-oauth codex --name 'my-chatgpt' --models 'gpt-4'` は Codex / ChatGPT の RFC 8628 風 device authorization flow を実行 —— セラーは**一度も token 文字列に触れません**：

1. CLI が `/api/seller/codex/device/start` を呼び、短い `user_code` と `verification_uri` を取得
2. CLI がデフォルトで `https://auth.openai.com/codex/device` をブラウザで自動オープン（`--no-browser` でスキップ）、ユーザーがブラウザで `user_code` を入力して認可
3. CLI が `/api/seller/codex/device/poll` をポーリング、認可完了で backend が自動的に channel を作成、OAuth token を channel の `key` フィールドに保存
4. 出力：channel id + バインドされた ChatGPT メール

認可 cookie はプロセス内 `http.CookieJar` で管理、ディスク非永続 —— device flow state は短命でプロセスにバインド、脅威モデルと整合。

#### `add-oauth claude` —— paste-and-submit OAuth

`everyapi seller add-oauth claude --name … --models …`。Anthropic OAuth provider は彼ら側で `redirect_uri` を `https://console.anthropic.com/oauth/code/callback` にハードコードしているため、CLI は localhost listener で callback を自動受信できません。フロー：

1. CLI が `/api/seller/claude/oauth/start` を呼び、backend が PKCE ペア + state を作成し、Anthropic の authorize URL を返却
2. CLI がデフォルトでブラウザを開く（`--no-browser` でスキップ）、ユーザーが Anthropic にログインして承認
3. Anthropic がユーザーを彼らの callback ページにリダイレクトし、`<code>#<state>` の文字列を表示
4. **ユーザーがこの文字列をコピーして CLI に貼り戻す**
5. CLI が `/api/seller/claude/oauth/complete` を呼び、backend が code+verifier を exchange して token を取得、channel を mint

device flow より貼り付け 1 ステップ多いものの、`~/.claude/auth.json` を手探りで探すよりもはるかに簡単です。session cookie は start 時に backend が発行、complete は同一セッションにヒットする必要があります —— CLI の `http.CookieJar` はプロセス内管理、呼び出しごとに分離。

#### `add-oauth gemini` —— 真のワンクリック loopback OAuth

`everyapi seller add-oauth gemini --name … --models … [--no-browser] [--timeout 5m]`。Google の gemini-cli installed-app OAuth client は `http://127.0.0.1:<port>/callback` を redirect_uri として受け入れるため、**CLI 自身が listener を起動して callback を受信**、ユーザーはブラウザログイン後に貼り付け不要です。フロー：

1. CLI がランダム ephemeral port (`127.0.0.1:0`) に 1 回限りの HTTP listener を起動、パス固定 `/callback`
2. CLI が `redirect_uri = http://127.0.0.1:<port>/callback` を付けて `/api/seller/gemini/oauth/start` を呼ぶ；backend が redirect を厳密検証 loopback / port ≥ 1024 / scheme=http / path=/callback / query/fragment/userinfo なし（SSRF + redirect 乗っ取り防止）
3. CLI がデフォルトでブラウザを開く、ユーザーが Google にログインして同意
4. Google が `?code=…&state=…` を CLI の listener にリダイレクト
5. CLI が state 一致を検証（stale flow / 偽造防止）、`/api/seller/gemini/oauth/complete` を呼ぶ
6. Backend が code + 同一 redirect_uri を exchange して token を取得、channel を mint

他 2 プロバイダーとの比較：

| Provider | 体験 | 理由 |
|---|---|---|
| `codex` | ユーザーが 6 桁 user_code をブラウザに入力、CLI が自動ポーリング | OpenAI device flow、redirect_uri なし |
| `claude` | ユーザーがブラウザでログイン + `code#state` をコピーして CLI に貼り戻す | Anthropic が redirect_uri を自社 callback URL にハードコード |
| `gemini` | ユーザーがブラウザでログイン + タブを閉じれば完了 | Google が loopback redirect を受け入れる |

`--timeout` で最大待機時間を制御（デフォルト 5 分）。タイムアウト時に exit + listener を clean に close。

### `everyapi edge <sub>` —— BYO-GPU supplier agent ワンクリック展開

遊休 GPU を EveryAPI に接続して compute を販売。CLI が一連の展開を 8 つのサブコマンドに集約し、supplier 自身が docker-compose をコピーしたり `.env` を埋めたり registration token を運ぶといった手作業を不要にします：

```bash
everyapi login                              # 既存ログインを再利用
everyapi edge register --name "rtx-4090"    # /api/seller/edge/nodes を呼んで node_id + token を取得、~/.local/share/everyapi/edge/<id>/ に保存
everyapi edge start                         # NVIDIA / ROCm / Apple Silicon / CPU を自動検出、docker compose up -d
everyapi edge models pull llama3.1:8b       # docker compose exec ollama ollama pull ...
everyapi edge status                        # ローカル docker compose ps + dashboard 側 online/offline
everyapi edge logs -f                       # ログをリアルタイム追跡
everyapi edge update                        # docker compose pull && up -d
everyapi edge remove                        # down -v + ローカル dir 削除 + backend DELETE
```

`start` は `text/template` を使って `docker-compose.yml` をランタイム描画します（**embed の静的 YAML ではない**）—— これによりコンテナ名が node_id でネームスペース化され、単一ホスト上の複数 node が衝突しません。GPU passthrough は mode に応じて条件描画（NVIDIA = `deploy.resources.devices` + nvidia ドライバ；ROCm = `/dev/kfd` + `/dev/dri` + `group_add: video`；macOS = ollama コンテナなし、agent が `host.docker.internal` 経由でホストのネイティブ ollama に接続）。

認証情報の流れ：cli が既存の `sk-everyapi-` Bearer で `POST /api/seller/edge/nodes` を呼ぶ → backend が 1 回だけ `registration_token` を返す（以降 backend は sha256 のみ保存、再表示しない）→ cli が 0600 で `~/.local/share/everyapi/edge/<id>/node.json` に保存 → compose の `EVERYAPI_REGISTRATION_TOKEN` env に描画。**registration token は .env ファイルには一切書きません**（supplier が誤って commit するのを防ぐため）。

依存：`docker` + `docker compose v2`（v1 は EOL でサポート外）。macOS は `brew install ollama && brew services start ollama` が必要（Metal アクセラレーションは docker コンテナ内では動作しない）。

### `everyapi topup` —— anti-phishing phrase 付きの充填リダイレクト

`everyapi topup` は dashboard の充填ページを開きます。リダイレクト前に docs §7-5 Layer 3 検証を 1 段挟みます：

1. CLI が backend `POST /api/cli/jump-session` を呼び、session id + 4 絵文字の phrase 文字列（例 `🌊 🦊 🍕 🚀`）を取得
2. CLI が URL と phrase 両方をターミナルに表示、ユーザーに「次のページ上部に同じ phrase が表示されるはず」と通知
3. ユーザーが Enter を押すと、CLI がシステムブラウザで URL を開く（`?jump_session=<id>` 付き）
4. Dashboard が読み込み時に backend `GET /api/cli/jump-session/:id/phrase` を呼び、同じ phrase 文字列を取得、ページヘッダで**目立つ位置に表示**
5. ユーザーが視覚的に比較：phrase 一致 → 本物の EveryAPI；不一致または非表示 → タブを閉じる、phishing の可能性

なぜ phishing を防げるか：phrase は backend のメモリ内にランダム 32-hex session id をキーに格納；phishing サイトには auth path がないため取得不可、攻撃者の偽 `wallet/topup?jump_session=<id>` も phrase を読めません。短い TTL (10 min) + single-use（dashboard が一度取得するとセッションは削除）により再利用リスクをさらに制限。

```bash
everyapi topup                    # デフォルトでブラウザを開く
everyapi topup --no-browser       # URL を表示のみ、手動でコピー
```

### `everyapi status` —— 現在の残高 / 使用量 / クォータ

```
$ everyapi status

  alice (alice@example.com)
  quota:     $12.34 remaining   $5.67 used
  requests:  1,234
  topup:     https://app.everyapi.ai/wallet
```

### `everyapi update` —— brew アップグレードコマンドを自動実行

GitHub mirror の最新 release を確認し、現バージョンと比較して、**`brew update && brew upgrade everyapi` を自動実行**します —— 1 コマンドで完了、コピー&ペースト不要。

```bash
$ everyapi update

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

なぜバイナリを直接置き換えないのか？Homebrew 自身の検証チェーン（SHA / bottle signing）は CLI 内で再実装するものより堅牢で、実行中の executable を自己置換するのは Windows プラットフォームでは地雷だらけだからです。

Flag:
- `--check` —— サイレント比較、最新なら exit 0、古ければ exit 1。CI / cron 用：
  ```bash
  everyapi update --check || echo "needs upgrade"
  ```
- `--dry-run` —— 実行予定のコマンドを表示するが実行しない（インスペクション用）

### `everyapi settings` —— CLI 設定（言語など）

CLI は 7 言語の i18n を標準装備：英語、簡体字中国語、日本語、한국어、Español、Deutsch、Français — CLI 自身の文字列はユーザー言語でレンダリングされます。バックエンド API エラーは `Accept-Language` ヘッダーで自動ネゴシエートされ、8 言語をカバー — 上記 7 言語 + 繁体字中国語。

```bash
$ everyapi settings                          # 対話 picker：言語を選択
$ everyapi settings list                     # 現在の設定を表示
$ everyapi settings set language zh          # 直接設定
$ everyapi settings set language fr          # フランス語も同様
$ everyapi settings reset                    # デフォルトに戻す（en + LANG 自動検出）
```

**自動検出**：明示的に設定されていない場合 → 起動時に `EVERYAPI_LANG > LC_ALL > LC_MESSAGES > LANG` の順に環境変数を読みます。システム locale が `zh_CN.UTF-8` / `ja_JP.UTF-8` / `fr_FR.UTF-8` 等であれば即座に有効、ゼロ設定。

**1 回限りの上書き**：

```bash
EVERYAPI_LANG=zh everyapi status             # この呼び出しは中国語で表示、永続化なし
```

**翻訳例**（未ログインエラー、7 言語 × 同一文）：

```
en : Error: not logged in — run 'everyapi login' first
zh : 错误: 未登录 — 先运行 'everyapi login'
ja : エラー: ログインしていません — まず 'everyapi login' を実行してください
ko : 오류: 로그인되어 있지 않습니다 — 먼저 'everyapi login' 을 실행하세요
es : Error: no has iniciado sesión — ejecuta primero 'everyapi login'
de : Fehler: nicht angemeldet — führe zuerst 'everyapi login' aus
fr : Erreur: non connecté — exécutez d'abord 'everyapi login'
```

設定ファイルは `~/.config/everyapi/settings.json` に保存（`credentials.json` と同ディレクトリだが mode `0644` —— secret なし）。

**翻訳の改善 / 新言語追加**：[`internal/i18n/locales/README.md`](internal/i18n/locales/README.md) を参照。

## 設定ファイル

認証情報は `~/.config/everyapi/credentials.json` に保存（`$XDG_CONFIG_HOME` が設定されている場合は `$XDG_CONFIG_HOME/everyapi/`）、ファイルモード `0600`。`everyapi login` が書き、その他のコマンドが読む。

> ⚠️ **Token は平文で保存**。ファイルモード `0600` + `$HOME` プライベートパスは `gh auth` / `aws configure` 等の業界 CLI と同じ慣行ですが、**家庭用マシンの盗難 / マルウェアシナリオ**ではこのファイルを読める任意のプロセスがあなたとして EveryAPI API を呼べます（MCP ツール含む、後述 §money-path friction step 参照）。推奨：
> - 共有 / 公共マシンで `everyapi login` しない
> - macOS ユーザー：FileVault 有効化前に `everyapi logout` を検討
> - Linux ユーザー：home-dir 暗号化を有効化（`ecryptfs` / LUKS）
> - 漏洩疑い時 → `everyapi logout` で即時ローカル credentials を消去し、EveryAPI dashboard で API key を rotate
>
> Platform keychain backend（macOS Keychain / Windows DPAPI / Linux Secret Service）は計画中、未実装。

フィールド：

- `api_base` —— EveryAPI ゲートウェイ URL。デフォルト `https://api.everyapi.ai`。セルフホスト / ローカル開発は `login` 時に `--api-base` で上書き可能。
- `access_token` —— 認証が必要な全 API 呼び出しに使われる bearer。
- `relay_key` —— relay API key（`sk-everyapi-…`）、`everyapi use` のサブプロセス env 用。`/api/token/*` から取得してここにキャッシュ。
- `user_id` / `username` —— キャッシュ、`status` が最初の API 往復前に identity 行を描画できるように。

## 開発

CLI ソースディレクトリ（この README、`go.mod`、`Makefile` が含まれるディレクトリ）で実行：

```bash
go test ./...
go run . status            # 本番に対して
go run . login --api-base http://localhost:8787   # ローカル backend に対して
```

ローカル全プラットフォーム クロスコンパイル（CI と同じレシピ）：

```bash
make cli-release           # 成果物は dist/（5 プラットフォーム × 1 バイナリ = 5 ファイル）
```

## MCP server (`everyapi mcp` サブコマンド)

`everyapi` バイナリは [Model Context Protocol](https://modelcontextprotocol.io) server を**内蔵**しています —— サブコマンドとして起動（`everyapi mcp` が stdin を読み stdout に書く）、AI エージェント（Claude Code / Cursor / Codex CLI など任意の MCP クライアント）が直接呼び出せます、**ユーザーがターミナルを開く必要はありません**。

> ⚠️ **MCP server の認証モデル + 露出面**
>
> - **ポートを開かない**：`everyapi mcp` は純粋な stdio JSON-RPC、host CLI から fork される。**socket / TCP port を一切リッスンしない** —— ネットワーク層に露出面なし。
> - **`~/.config/everyapi/credentials.json` を直接読む**：MCP server には独自の認証フローがなく、credentials ファイルを読める = 公開された全ツールをあなたとして呼び出せる。あなたのユーザー権限でプロセスを実行できる MCP host はすべてフルアクセスを持つ。
> - **Money path `everyapi_seller_withdraw` には friction step**：呼び出し側は `confirm: "yes"` を渡す必要があり、AI エージェントが転送アクションを UI で人間に surface することを保証し、silent drain を回避。他の read-only ツール（status / topup / seller_list）にはこの要件なし。
>
> 信頼できない MCP host にはインストールしないでください。

### インストール

CLI と同じバイナリ、CLI をインストールすれば使えます：

```bash
make cli                                              # ローカルビルド、成果物 ./bin/everyapi
# または go install で直接：
go install github.com/everyapi-ai/everyapi-ai@latest
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

Cursor、Codex CLI などその他の MCP クライアントへの接続も同様 —— `command` を `everyapi` バイナリに、`args: ["mcp"]`。

### 認証前提

事前にターミナルで一度 `everyapi login` を実行している必要があります —— MCP server はバックグラウンドプロセスで、ターミナル対話能力がなく、device-code フローを自分で実行できません。`~/.config/everyapi/credentials.json` を直接読みます；欠落していると、各ツールが `isError: true` の「not logged in」を返してユーザーに login を促します。

### v1 で公開するツール（8 個）

| Tool | 入力 | 役割 |
|---|---|---|
| `everyapi_status` | なし | 現在の残高 / 使用量 / リクエスト数 |
| `everyapi_topup` | なし | web 充填 URL を返す |
| `everyapi_seller_list` | なし | marketplace seller channel を一覧 |
| `everyapi_seller_withdraw` | `{confirm: "yes", quota?: int}` | seller_quota をメイン残高に移動；**`confirm: "yes"` 必須**（money-path friction） |
| `everyapi_seller_add_oauth_codex_start` | `{name, models}` | Codex / ChatGPT デバイス認可フローを開始、`user_code` + `verification_uri` + `flow_id` を返す |
| `everyapi_seller_add_oauth_codex_poll` | `{flow_id}` | Codex 認可状態を確認。`pending`/`slow_down` ポーリング継続；`authorized` で channel id 取得；`expired`/`denied` で終了 |
| `everyapi_seller_add_oauth_claude_start` | `{name, models}` | Anthropic OAuth フローを開始、`authorize_url` を返す。ブラウザログイン後、ユーザーは `<code>#<state>` 文字列を取得 |
| `everyapi_seller_add_oauth_claude_complete` | `{input}` | 前ステップでユーザーが貼った `<code>#<state>` 文字列を提出して完了、channel を mint |

**OAuth tool 使用パターン**（AI エージェントが会話でこう辿る）：

```
User: ChatGPT Plus のセラー channel を追加して、名前は my-chatgpt、models は gpt-4
AI    → everyapi_seller_add_oauth_codex_start({name: "my-chatgpt", models: "gpt-4"})
       ← "chatgpt.com/codex に行って USR-789 を入力、終わったら教えて"
User: ブラウザで入力完了
AI    → everyapi_seller_add_oauth_codex_poll({flow_id: "..."})
       ← "status=pending、もう少し待つ"
[authorized になるまでポーリング継続]
       ← "status=authorized — channel #314 mounted"

User: Claude Pro も追加、my-claude / claude-3-opus
AI    → everyapi_seller_add_oauth_claude_start({...})
       ← "[URL] で認可を完了したら code#state 文字列をください"
User: code-abc123#state-xyz
AI    → everyapi_seller_add_oauth_claude_complete({input: "code-abc123#state-xyz"})
       ← "Channel #315 mounted"
```

Gemini OAuth（loopback flow）は **MCP では提供しません** —— loopback listener はクロスツール呼び出しのライフサイクルと一致しないため。Gemini は CLI の `everyapi seller add-oauth gemini` を引き続き使用。

### 手動スモークテスト

```bash
make cli
./bin/everyapi mcp <<'EOF'
{"jsonrpc":"2.0","id":1,"method":"initialize"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"everyapi_status","arguments":{}}}
EOF
```

3 行の JSON 応答が見えるはずです：initialize 結果、4 ツールのリスト、status テキスト（または not-logged-in の isError）。

## このバイナリにまだ**含まれていない**もの

現在**未実装**（重要度順、後続 release で増分追加、既存 v1 surface を破壊しない）：

- ⚠️ OS レベルの code signing（macOS notarization / Windows Authenticode）—— 現状は sigstore cosign keyless + SHA256SUMS 二層検証に依存。両方とも各 GitHub Release に同梱され、Homebrew がインストール時に自動検証します
- ❌ Platform keychain backend —— token は平文ディスク保存のまま（mode 0600）

以前はここに記載していたが**既に実装済み**（未実装扱いしないでください）：

- ✅ Local sanitizer proxy —— コマンドは `everyapi proxy {start,stop,status,configure}`（`everyapi start`/`everyapi configure` ではない）、エンジン + 6 内蔵 detector + カスタム regex + `everyapi use` への統合
- ✅ Seller OAuth onboarding 全 3 プロバイダー（codex device / claude paste / gemini loopback）
- ✅ QR sign-in メインパス —— `login` は device-code **+ QR メインパス**、`--no-qr` でフォールバック
- ✅ Anti-phishing レイヤー —— phrase 文字列（`everyapi topup`）、PKCE/state 厳格チェック、cert pinning すべて実装済み；cert pinning は **report-only**（マッチ時静か / mismatch 時警告 / 接続拒否しない）、enforce は製品判断で「警告のみ実施しない」

## 脆弱性報告

[`SECURITY.md`](../SECURITY.md) を参照。
