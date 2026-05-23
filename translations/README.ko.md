> 🌐 [English](../README.md) · [简体中文](README.zh-CN.md) · [日本語](README.ja.md) · **한국어** · [Español](README.es.md) · [Deutsch](README.de.md) · [Français](README.fr.md)

# `everyapi` CLI

[EveryAPI](https://everyapi.ai) AI API 게이트웨이의 buyer onboarding CLI. 임의의 Claude Code / Codex / Gemini CLI 를 **1 분 안에** 게이트웨이에 연결합니다.

상태: **핵심 흐름 구현 완료** —— buyer onboarding, seller 명령 (plain-key + OAuth 3 개 provider), sanitizer proxy, QR sign-in 메인 경로, anti-phishing 계층까지 모두 구현 완료. 미구현 항목은 OS 레벨 code signing 과 platform keychain backend 뿐입니다 (마지막 「이 바이너리에 아직 포함되지 않은 것」 참조).

## 설치

```bash
brew tap everyapi-ai/tap && brew install everyapi
```

이후 업그레이드 —— 반드시 먼저 `brew update`:

```bash
brew update && brew upgrade everyapi
```

`brew update` 를 먼저 실행하지 않으면 `brew upgrade everyapi` 는 로컬 캐시된 formula 를 사용해 새 release 가 있어도 "already installed" 를 표시합니다.

## 명령

| 명령 | 역할 |
|---|---|
| `everyapi login` | 이 디바이스에서 EveryAPI 에 로그인 |
| `everyapi logout` | 이 디바이스의 자격 증명 제거 |
| `everyapi status` | 잔액, 사용량, 쿼터 보기 |
| `everyapi topup` | 충전 페이지 열기 (anti-phishing phrase 검증 포함) |
| `everyapi use <tool>` | env 설정 후 서드파티 CLI 로 exec (EveryAPI 를 가리킴) |
| `everyapi seller <sub>` | Marketplace 셀러 측 명령 (list / withdraw / add-key / setup) |
| `everyapi edge <sub>` | BYO-GPU supplier agent 원클릭 배포 (register / start / status / logs / models / stop / update / remove) |
| `everyapi mcp` | MCP server 로 실행 (stdin/stdout JSON-RPC) |
| `everyapi update` | 새 버전 확인, 설치 방법에 맞는 업그레이드 명령 출력 |
| `everyapi version` | 빌드 버전 표시 |
| `everyapi help` | 도움말 |

### `everyapi use <tool>` — 서드파티 CLI 로 exec (EveryAPI 게이트웨이를 가리킴)

이 CLI 를 설치하는 주된 이유. 대상 서드파티 도구의 환경 변수를 해당 도구의 관례에 맞춰 설정하고 exec 합니다 —— 기존 Claude Code / OpenAI Codex CLI / Gemini CLI 는 **설정 변경 없이** EveryAPI 게이트웨이를 가리키게 됩니다.

```bash
everyapi use claude         # Claude Code → EveryAPI
everyapi use codex          # OpenAI Codex CLI → EveryAPI
everyapi use gemini         # Gemini CLI → EveryAPI
everyapi use                # 인자 없음 → 설치된 도구 중 대화형 선택
```

도구마다 env 관례가 다르며, CLI 가 대신 기억해 줍니다:

| 도구 | 설정되는 환경 변수 |
|---|---|
| claude | `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN` |
| codex | `OPENAI_BASE_URL`, `OPENAI_API_KEY` |
| gemini | `GEMINI_API_KEY`, `GOOGLE_GEMINI_BASE_URL` |

어떤 변수 이름을 읽는지, `/v1` 접미사가 필요한지, 어떤 auth header 방식인지 매번 찾아볼 필요가 없습니다.

> ⚠️ **서브프로세스 env 보안 주의**: 위 환경 변수에는 relay API key 가 포함됩니다. 서드파티 CLI 의 debug / verbose 모드는 env 를 로그에 쓸 수 있으므로 —— `everyapi use` 전에 켜는 debug flag 가 `*_TOKEN` / `*_API_KEY` 를 노출하지 않는지 확인하세요. debug 로그를 공유하기 전에 `sed -i 's/sk-everyapi-[A-Za-z0-9]*/REDACTED/g'` 를 실행하세요.

### `everyapi login` — Device Authorization Grant + QR 로그인

Device Authorization Grant (RFC 8628 형태) + docs §7-5 Layer 1 「디바이스 간 QR 로그인」 사용:

1. CLI 가 세션을 만들고, **터미널 QR 렌더링 + 짧은 코드와 URL 출력**
2. 폰으로 스캔 (또는 이미 EveryAPI 에 로그인된 브라우저에서 URL 열기) —— QR 안의 URL 은 이미 `?code=USR-789` 파라미터를 포함, dashboard 가 자동으로 코드를 채우고 user 는 Approve 만 클릭
3. CLI 가 access token 을 받아 `~/.config/everyapi/credentials.json` 에 저장 (mode 0600)

```bash
everyapi login                                    # 프로덕션, 기본 QR 렌더링 + 자동 브라우저 열기
everyapi login --api-base http://localhost:8787   # 로컬 개발 / 셀프호스트
everyapi login --no-browser                       # 자동 브라우저 열기 없음 (QR 로 스캔)
everyapi login --no-qr                            # QR 비렌더링 (UTF-8 이 아닌 터미널 / 파이핑)
```

터미널 QR 렌더링 예시 (Unicode 반블록 문자, 약 18-20 줄):

```
█▀▀▀▀▀█  ▀▀ ▄  █▀▀▀▀▀█
█ ███ █  ▀▄█▀  █ ███ █
█ ▀▀▀ █ ▄ ▀ █▀ █ ▀▀▀ █
▀▀▀▀▀▀▀ █▄█▄█▄ ▀▀▀▀▀▀▀
... (실제 QR 은 verification_uri?code=USR-789 을 인코딩)
```

왜 더 강력한 anti-phishing 경로인가:

- User 가 **새 디바이스에서 비밀번호를 입력할 필요 없음** → phishing 사이트가 credential 을 가로챌 기회 없음
- User 가 **낯선 브라우저 페이지로 리다이렉트될 필요 없음** → web 리다이렉트 phishing 면적 소멸
- 설령 CLI 가 악성 fork 가 가짜 QR 을 생성해도, 스캔 후 확인 페이지는 진짜 everyapi.ai dashboard (user 가 로그인된 디바이스에서 트리거)이며, 모르는 코드를 user 가 Approve 하지 않음

docs §7-5 의 다른 layer (cert pinning / phrase 문자열 / PKCE OAuth) 는 각각 독립 PR 로 구현 완료 (cert pinning 은 report-only, enforce 는 제품 결정으로 미적용).

### `everyapi seller <sub>` — marketplace 셀러 측 서브 명령

dashboard 의 channel mount / 출금 작업을 터미널로 옮겨 scripted onboarding 을 편리하게 합니다. channel 마운트 전 `seller setup` 이 eligibility (계정 활성 / 이메일 인증 / 계정 연령 / 소비 기록 / channel 상한) 를 먼저 확인하고, 실패한 gate 를 **키 입력 전에** 먼저 나열하여 폼 제출 후 422 가 발생하지 않도록 합니다.

```bash
everyapi seller list                          # 마운트된 channel 목록
everyapi seller withdraw                      # pending seller 수익 전체를 메인 잔액으로 이동
everyapi seller withdraw --quota 1000         # 부분 송금 (DB 단위)
everyapi seller add-key   --type claude --name 'my-pro' \
                        --key 'sk-ant-...' --models 'claude-3-opus,claude-3-sonnet'
everyapi seller add-oauth codex --name 'my-chatgpt' --models 'gpt-4'
                                            # 원클릭 OAuth: CLI 가 device flow 시작, user 가 브라우저에서
                                            # user_code 입력, token 이 자동으로 channel 에 들어옴
everyapi seller add-oauth claude --name 'my-claude' --models 'claude-3-opus,claude-3-sonnet'
                                            # paste flow: CLI 가 Anthropic 인증 페이지를 열고, user 가
                                            # callback 에 표시된 code#state 를 터미널에 붙여넣음
everyapi seller add-oauth gemini --name 'my-gemini' --models 'gemini-1.5-pro'
                                            # 진정한 원클릭 loopback: CLI 가 랜덤 포트 listener 를 시작,
                                            # Google 이 코드를 직접 CLI 로 보냄, 붙여넣기 불필요
everyapi seller setup                         # 대화형 wizard: 먼저 eligibility 확인 후 add-key 안내
```

#### `add-key` — 멀티 키 백업 풀

`--key` 는 반복 가능하며, N 개의 동등한 자격 증명을 동일 channel 에 백업 풀로 마운트할 수 있습니다 (B2, PRODUCT §4.5). 기본 키가 401/403 을 반환하면 backend 가 자동으로 다음 키로 failover 합니다. `--key-remark` 도 반복 가능하며, 위치로 `--key` 와 대응 (i 번째 `--key-remark` 가 i 번째 `--key` 의 라벨, 추후 dashboard 식별용). OAuth blob 은 백업 풀에 들어갈 수 없으며 —— 여전히 단일 키 channel 만 가능.

```
everyapi seller add-key   --type claude --name 'claude-pool' \
                        --models 'claude-3-opus' \
                        --key 'sk-ant-primary' --key-remark 'primary' \
                        --key 'sk-ant-backup'  --key-remark 'team backup'
```

`add-key` 의 `--type` 은 alias (`openai` / `claude` / `gemini` / `codex` / `vertex` / `aws` / `xai` / `deepseek`) 또는 숫자 id 를 받습니다. 마운트는 marketplace eligibility (계정 활성, 이메일 인증, 소비 기록, channel 수 상한) 의 제한을 받으며, CLI 는 `add-key` / `add-oauth` / `setup` 세 진입점 모두에서 먼저 eligibility 를 확인하고 실패 시 checklist 를 나열합니다.

#### `add-oauth codex` — 원클릭 OAuth (device flow)

`everyapi seller add-oauth codex --name 'my-chatgpt' --models 'gpt-4'` 는 Codex / ChatGPT 의 RFC 8628 풍 device authorization flow 를 수행합니다 —— seller 는 **token 문자열을 한 번도 접하지 않습니다**:

1. CLI 가 `/api/seller/codex/device/start` 를 호출, 짧은 `user_code` 와 `verification_uri` 를 받음
2. CLI 가 기본적으로 `https://auth.openai.com/codex/device` 를 브라우저로 자동 오픈 (`--no-browser` 로 스킵). user 가 브라우저에서 `user_code` 입력하여 인증 완료
3. CLI 가 `/api/seller/codex/device/poll` 폴링. 인증 완료되면 backend 가 자동으로 channel 생성, OAuth token 을 channel 의 `key` 필드에 저장
4. 출력: channel id + 바인딩된 ChatGPT 이메일

인증 cookie 는 프로세스 내 `http.CookieJar` 가 관리, 디스크에 저장하지 않음 —— device flow state 는 단명이고 프로세스 바인딩이라 위협 모델과 일치.

#### `add-oauth claude` — paste-and-submit OAuth

`everyapi seller add-oauth claude --name … --models …`. Anthropic OAuth provider 가 자신들 쪽에서 `redirect_uri` 를 `https://console.anthropic.com/oauth/code/callback` 으로 하드코드하고 있어, CLI 가 localhost listener 로 callback 을 자동 수신할 수 없습니다. 흐름:

1. CLI 가 `/api/seller/claude/oauth/start` 호출, backend 가 PKCE 쌍 + state 생성, Anthropic 의 authorize URL 반환
2. CLI 가 기본적으로 브라우저를 엶 (`--no-browser` 로 스킵). user 가 Anthropic 에 로그인, 승인
3. Anthropic 이 user 를 자신들의 callback 페이지로 리다이렉트, `<code>#<state>` 문자열 표시
4. **user 가 이 문자열을 복사하여 CLI 에 붙여넣음**
5. CLI 가 `/api/seller/claude/oauth/complete` 호출, backend 가 code+verifier 를 exchange 하여 token 획득, channel mint

device flow 보다 붙여넣기 1 단계 더 많지만, 여전히 `~/.claude/auth.json` 을 손수 찾는 것보다 훨씬 간단합니다. session cookie 는 start 시 backend 가 발급하며, complete 는 동일 세션에 적중해야 합니다 —— CLI 의 `http.CookieJar` 는 프로세스 내 관리, 호출당 분리.

#### `add-oauth gemini` — 진정한 원클릭 loopback OAuth

`everyapi seller add-oauth gemini --name … --models … [--no-browser] [--timeout 5m]`. Google 의 gemini-cli installed-app OAuth client 가 `http://127.0.0.1:<port>/callback` 을 redirect_uri 로 받아주므로, **CLI 자체가 listener 를 띄워 callback 을 수신**합니다. user 는 브라우저 로그인 후 어떤 붙여넣기도 필요 없습니다. 흐름:

1. CLI 가 랜덤 ephemeral port (`127.0.0.1:0`) 에 1 회용 HTTP listener 를 띄우고, 경로는 고정 `/callback`
2. CLI 가 `redirect_uri = http://127.0.0.1:<port>/callback` 을 첨부하여 `/api/seller/gemini/oauth/start` 호출. backend 가 redirect 를 엄격 검증 (loopback / port ≥ 1024 / scheme=http / path=/callback / query/fragment/userinfo 없음, SSRF + redirect 하이재킹 방지)
3. CLI 가 기본적으로 브라우저를 엶, user 가 Google 에 로그인 + 동의
4. Google 이 `?code=…&state=…` 를 CLI 의 listener 로 리다이렉트
5. CLI 가 state 일치 검증 (stale flow / 위조 방지) 후 `/api/seller/gemini/oauth/complete` 호출
6. Backend 가 code + 동일 redirect_uri 를 exchange 하여 token 획득, channel mint

다른 두 provider 와의 비교:

| Provider | 사용자 경험 | 이유 |
|---|---|---|
| `codex` | user 가 6 자리 user_code 를 브라우저에 입력, CLI 가 자동 폴링 | OpenAI device flow, redirect_uri 없음 |
| `claude` | user 가 브라우저 로그인 + `code#state` 복사하여 CLI 에 붙여넣기 | Anthropic 이 redirect_uri 를 자사 callback URL 로 하드코드 |
| `gemini` | user 가 브라우저 로그인 + 탭을 닫으면 완료 | Google 이 loopback redirect 를 받아줌 |

`--timeout` 으로 최대 대기 시간 제어 (기본 5 분). 타임아웃 시 exit + listener 깨끗이 close.

### `everyapi edge <sub>` — BYO-GPU supplier agent 원클릭 배포

유휴 GPU 를 EveryAPI 에 연결하여 compute 를 판매. CLI 가 전체 배포를 8 개 서브 명령으로 응축, supplier 가 직접 docker-compose 를 복사하거나 `.env` 를 채우거나 registration token 을 옮기는 수작업을 없앱니다:

```bash
everyapi login                              # 기존 로그인 재사용
everyapi edge register --name "rtx-4090"    # /api/seller/edge/nodes 호출, node_id + token 획득, ~/.local/share/everyapi/edge/<id>/ 에 저장
everyapi edge start                         # NVIDIA / ROCm / Apple Silicon / CPU 자동 감지, docker compose up -d
everyapi edge models pull llama3.1:8b       # docker compose exec ollama ollama pull ...
everyapi edge status                        # 로컬 docker compose ps + dashboard 측 online/offline
everyapi edge logs -f                       # 실시간 로그 추적
everyapi edge update                        # docker compose pull && up -d
everyapi edge remove                        # down -v + 로컬 dir 삭제 + backend DELETE
```

`start` 는 `text/template` 을 사용하여 `docker-compose.yml` 을 런타임에 렌더링합니다 (**embed 의 정적 YAML 이 아님**) —— 이렇게 하면 container name 이 node_id 로 네임스페이스화되어 단일 호스트의 여러 node 가 충돌하지 않으며, GPU passthrough 는 mode 에 따라 조건부 렌더링 (NVIDIA = `deploy.resources.devices` + nvidia driver; ROCm = `/dev/kfd` + `/dev/dri` + `group_add: video`; macOS = ollama container 없음, agent 가 `host.docker.internal` 을 통해 호스트의 native ollama 에 연결).

자격 증명 흐름: cli 가 기존 `sk-everyapi-` Bearer 로 `POST /api/seller/edge/nodes` 호출 → backend 가 `registration_token` 을 한 번 반환 (이후 backend 는 sha256 만 저장, 다시 표시하지 않음) → cli 가 0600 으로 `~/.local/share/everyapi/edge/<id>/node.json` 에 저장 → compose 의 `EVERYAPI_REGISTRATION_TOKEN` env 에 렌더링. **registration token 은 어떤 .env 파일에도 쓰지 않음** (supplier 가 실수로 commit 하지 않도록).

의존: `docker` + `docker compose v2` (v1 은 EOL 로 지원하지 않음). macOS 는 `brew install ollama && brew services start ollama` 필요 (Metal 가속이 docker container 안에서 동작하지 않음).

### `everyapi topup` — anti-phishing phrase 가 있는 충전 리다이렉트

`everyapi topup` 은 dashboard 의 충전 페이지를 엽니다. 리다이렉트 전 docs §7-5 Layer 3 검증을 한 단계 거칩니다:

1. CLI 가 backend `POST /api/cli/jump-session` 을 호출하여 session id + 4 개 이모지 phrase 문자열 (예: `🌊 🦊 🍕 🚀`) 을 받음
2. CLI 가 URL 과 phrase 를 둘 다 터미널에 출력하고, user 에게 "다음 페이지 상단에 같은 phrase 가 표시되어야 함" 안내
3. User 가 Enter 누르면 CLI 가 시스템 브라우저로 URL 을 엶 (`?jump_session=<id>` 포함)
4. Dashboard 가 로드 시 backend `GET /api/cli/jump-session/:id/phrase` 호출, 같은 phrase 문자열 획득, 페이지 헤더에 **눈에 띄게 표시**
5. User 가 시각 비교: phrase 일치 → 진짜 EveryAPI; 불일치 또는 미표시 → 탭 닫기, phishing 가능성

왜 phishing 을 막을 수 있는가: phrase 는 backend 메모리 안에 랜덤 32-hex session id 로 키잉되어 저장. phishing 사이트는 auth path 가 없어 가져올 수 없으며, 공격자가 만든 가짜 `wallet/topup?jump_session=<id>` 도 phrase 를 읽을 수 없습니다. 짧은 TTL (10 min) + single-use (dashboard 가 한 번 가져가면 세션 삭제) 로 재사용 위험을 더 제한.

```bash
everyapi topup                    # 기본적으로 브라우저 열기
everyapi topup --no-browser       # URL 만 출력, 수동 복사
```

### `everyapi status` — 현재 잔액 / 사용량 / 쿼터

```
$ everyapi status

  alice (alice@example.com)
  quota:     $12.34 remaining   $5.67 used
  requests:  1,234
  topup:     https://app.everyapi.ai/wallet
```

### `everyapi update` —— brew 업그레이드 명령 자동 실행

GitHub mirror 의 최신 release 를 확인하고 현재 버전과 비교한 후, **`brew update && brew upgrade everyapi` 를 자동 실행** —— 한 명령으로 완료, 복사/붙여넣기 불필요.

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

왜 바이너리를 직접 교체하지 않는가? Homebrew 자체 검증 체인 (SHA / bottle signing) 이 CLI 내에서 다시 구현하는 것보다 견고하고, 실행 중인 executable 을 자가 교체하는 것은 Windows 플랫폼에서 지뢰밭이기 때문입니다.

Flag:
- `--check` —— 사일런트 비교, 최신이면 exit 0, 구버전이면 exit 1. CI / cron 용:
  ```bash
  everyapi update --check || echo "needs upgrade"
  ```
- `--dry-run` —— 실행 예정 명령을 출력하지만 실제 실행하지 않음 (인스펙션용)

### `everyapi settings` — CLI 기본 설정 (언어 등)

CLI 는 7 개 언어 i18n 기본 탑재: 영어, 간체 중국어, 일본어, 한국어, Español, Deutsch, Français — CLI 자체 문자열은 사용자 언어로 렌더링됩니다. 백엔드 API 오류는 `Accept-Language` 헤더로 자동 협상되며 8 개 지원 — 위 7 개 + 번체 중국어.

```bash
$ everyapi settings                          # 대화형 picker: 언어 선택
$ everyapi settings list                     # 현재 설정 보기
$ everyapi settings set language zh          # 직접 설정
$ everyapi settings set language fr          # 프랑스어 동일
$ everyapi settings reset                    # 기본값으로 초기화 (en + LANG 자동 감지)
```

**자동 감지**: 명시적으로 설정하지 않은 경우 → 시작 시 `EVERYAPI_LANG > LC_ALL > LC_MESSAGES > LANG` 순으로 환경 변수를 읽습니다. 시스템 locale 이 `zh_CN.UTF-8` / `ja_JP.UTF-8` / `fr_FR.UTF-8` 등이면 즉시 적용, 제로 설정.

**일회성 오버라이드**:

```bash
EVERYAPI_LANG=zh everyapi status             # 이 호출은 중국어로 표시, 영구 저장하지 않음
```

**번역 예시** (로그인 안됨 오류, 7 개 언어 × 동일 문장):

```
en : Error: not logged in — run 'everyapi login' first
zh : 错误: 未登录 — 先运行 'everyapi login'
ja : エラー: ログインしていません — まず 'everyapi login' を実行してください
ko : 오류: 로그인되어 있지 않습니다 — 먼저 'everyapi login' 을 실행하세요
es : Error: no has iniciado sesión — ejecuta primero 'everyapi login'
de : Fehler: nicht angemeldet — führe zuerst 'everyapi login' aus
fr : Erreur: non connecté — exécutez d'abord 'everyapi login'
```

설정 파일은 `~/.config/everyapi/settings.json` 에 저장 (`credentials.json` 과 같은 디렉토리지만 mode `0644` —— secret 없음).

**번역 개선 / 새 언어 추가**: [`internal/i18n/locales/README.md`](internal/i18n/locales/README.md) 참조.

## 설정 파일

자격 증명은 `~/.config/everyapi/credentials.json` 에 저장 (`$XDG_CONFIG_HOME` 이 설정되어 있으면 `$XDG_CONFIG_HOME/everyapi/`), 파일 모드 `0600`. `everyapi login` 이 쓰고, 다른 명령들이 읽음.

> ⚠️ **Token 은 평문으로 저장**. 파일 모드 `0600` + `$HOME` 사설 경로는 `gh auth` / `aws configure` 같은 업계 CLI 와 동일 관행이지만, **가정용 PC 도난 / 악성 코드 시나리오**에서는 이 파일을 읽을 수 있는 어떤 프로세스든 당신으로서 EveryAPI API 를 호출할 수 있습니다 (MCP 도구 포함, 아래 §money-path friction step 참조). 권장:
> - 공유 / 공용 머신에서 `everyapi login` 하지 말 것
> - macOS 사용자: FileVault 활성화 전 `everyapi logout` 고려
> - Linux 사용자: home-dir 암호화 활성화 (`ecryptfs` / LUKS)
> - 유출 의심 시 → `everyapi logout` 으로 즉시 로컬 자격 증명 제거하고, EveryAPI dashboard 에서 API key 를 rotate
>
> Platform keychain backend (macOS Keychain / Windows DPAPI / Linux Secret Service) 는 계획 중, 미적용.

필드:

- `api_base` —— EveryAPI 게이트웨이 URL. 기본 `https://api.everyapi.ai`. 셀프호스트 / 로컬 개발은 `login` 시 `--api-base` 로 오버라이드 가능.
- `access_token` —— 인증 필요한 모든 API 호출에 사용되는 bearer.
- `relay_key` —— relay API key (`sk-everyapi-…`), `everyapi use` 의 서브프로세스 env 용. `/api/token/*` 에서 가져와 여기에 캐시.
- `user_id` / `username` —— 캐시, `status` 가 첫 API 라운드트립 전에 identity 라인을 렌더링할 수 있도록.

## 개발

CLI 소스 디렉토리 (이 README, `go.mod`, `Makefile` 이 포함된 디렉토리) 에서 실행:

```bash
go test ./...
go run . status            # 프로덕션 대상
go run . login --api-base http://localhost:8787   # 로컬 backend 대상
```

로컬 전 플랫폼 크로스 컴파일 (CI 와 같은 레시피):

```bash
make cli-release           # 산출물은 dist/ (5 플랫폼 × 1 바이너리 = 5 파일)
```

## MCP server (`everyapi mcp` 서브 명령)

`everyapi` 바이너리는 [Model Context Protocol](https://modelcontextprotocol.io) server 를 **내장**합니다 —— 서브 명령 형태로 시작 (`everyapi mcp` 는 stdin 읽고 stdout 씀), AI 에이전트 (Claude Code / Cursor / Codex CLI 등 임의의 MCP 클라이언트) 가 직접 invoke 가능, **사용자가 터미널을 열 필요 없음**.

> ⚠️ **MCP server 인증 모델 + 노출 면**
>
> - **포트 열지 않음**: `everyapi mcp` 는 순수 stdio JSON-RPC, host CLI 에서 fork. **어떤 socket / TCP port 도 listen 하지 않음** —— 네트워크 계층 노출 면 없음.
> - **`~/.config/everyapi/credentials.json` 직접 읽음**: MCP server 는 자체 인증 흐름이 없으며, credentials 파일을 읽을 수 있음 = 노출된 모든 도구를 당신으로서 호출 가능. 당신의 user 권한으로 프로세스를 실행할 수 있는 모든 MCP host 는 완전한 접근 권한을 가짐.
> - **돈 경로 `everyapi_seller_withdraw` 에는 friction step**: 호출자는 `confirm: "yes"` 를 전달해야 하며, AI 에이전트가 송금 동작을 UI 에 사람에게 surface 하도록 보장하여 silent drain 을 회피. 다른 read-only 도구 (status / topup / seller_list) 는 이 요구 없음.
>
> 신뢰할 수 없는 MCP host 는 설치하지 마세요.

### 설치

CLI 와 같은 바이너리, CLI 를 설치하면 사용 가능:

```bash
make cli                                              # 로컬 빌드, 산출물 ./bin/everyapi
# 또는 go install 직접:
go install github.com/everyapi-ai/everyapi-ai@latest
```

### Claude Code 에 연결

`~/.claude/settings.json` 에 추가:

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

Cursor, Codex CLI 등 다른 MCP 클라이언트에 연결도 유사 —— `command` 를 `everyapi` 바이너리로, `args: ["mcp"]`.

### 인증 전제

미리 터미널에서 한 번 `everyapi login` 을 실행해야 합니다 —— MCP server 는 백그라운드 프로세스로 터미널 상호작용 능력이 없어 device-code 흐름을 직접 실행할 수 없습니다. `~/.config/everyapi/credentials.json` 을 직접 읽으며, 누락 시 각 도구가 `isError: true` 의 "not logged in" 을 반환하여 사용자에게 login 을 안내합니다.

### v1 노출 도구 (8 개)

| Tool | 입력 | 역할 |
|---|---|---|
| `everyapi_status` | 없음 | 현재 잔액 / 사용량 / 요청 수 |
| `everyapi_topup` | 없음 | web 충전 URL 반환 |
| `everyapi_seller_list` | 없음 | marketplace seller channel 목록 |
| `everyapi_seller_withdraw` | `{confirm: "yes", quota?: int}` | seller_quota 를 메인 잔액으로 이동; **`confirm: "yes"` 필수** (돈 경로 friction) |
| `everyapi_seller_add_oauth_codex_start` | `{name, models}` | Codex / ChatGPT 디바이스 인증 흐름 시작, `user_code` + `verification_uri` + `flow_id` 반환 |
| `everyapi_seller_add_oauth_codex_poll` | `{flow_id}` | Codex 인증 상태 확인. `pending`/`slow_down` 은 폴링 계속; `authorized` 면 channel id 획득; `expired`/`denied` 면 종료 |
| `everyapi_seller_add_oauth_claude_start` | `{name, models}` | Anthropic OAuth 흐름 시작, `authorize_url` 반환. User 가 브라우저 로그인 후 `<code>#<state>` 문자열 획득 |
| `everyapi_seller_add_oauth_claude_complete` | `{input}` | 이전 단계에서 user 가 붙여넣은 `<code>#<state>` 문자열을 제출하여 완료, channel mint |

**OAuth 도구 사용 패턴** (AI 에이전트가 대화에서 이렇게 진행):

```
User: ChatGPT Plus 셀러 channel 을 추가해 줘, 이름은 my-chatgpt, models 는 gpt-4
AI    → everyapi_seller_add_oauth_codex_start({name: "my-chatgpt", models: "gpt-4"})
       ← "chatgpt.com/codex 가서 USR-789 입력하고, 끝나면 알려줘"
User: 브라우저에서 입력 완료
AI    → everyapi_seller_add_oauth_codex_poll({flow_id: "..."})
       ← "status=pending, 몇 초 더 대기"
[authorized 가 될 때까지 폴링 계속]
       ← "status=authorized — channel #314 mounted"

User: Claude Pro 도 추가, my-claude / claude-3-opus
AI    → everyapi_seller_add_oauth_claude_start({...})
       ← "[URL] 에서 인증 완료 후, code#state 문자열을 줘"
User: code-abc123#state-xyz
AI    → everyapi_seller_add_oauth_claude_complete({input: "code-abc123#state-xyz"})
       ← "Channel #315 mounted"
```

Gemini OAuth (loopback flow) 는 **MCP 에서 제공하지 않음** —— loopback listener 가 크로스 도구 호출 라이프사이클과 일치하지 않음. Gemini 는 CLI `everyapi seller add-oauth gemini` 를 계속 사용.

### 수동 스모크 테스트

```bash
make cli
./bin/everyapi mcp <<'EOF'
{"jsonrpc":"2.0","id":1,"method":"initialize"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"everyapi_status","arguments":{}}}
EOF
```

3 줄의 JSON 응답이 보여야 합니다: initialize 결과, 4 개 도구 목록, status 텍스트 (또는 not-logged-in 의 isError).

## 이 바이너리에 아직 **포함되지 않은** 것

현재 **미구현** (중요도 순, 후속 release 에서 증분 추가, 기존 v1 surface 를 깨뜨리지 않음):

- ⚠️ OS 레벨 code signing (macOS notarization / Windows Authenticode) —— 현재 sigstore cosign keyless + SHA256SUMS 이중 검증에 의존. 둘 다 모든 GitHub Release 에 첨부되며 Homebrew 가 설치 시 자동 검증합니다
- ❌ Platform keychain backend —— token 은 여전히 평문으로 디스크에 저장 (mode 0600)

이전에 여기 나열되었으나 **이미 구현 완료** (미구현으로 취급하지 마세요):

- ✅ Local sanitizer proxy —— 명령은 `everyapi proxy {start,stop,status,configure}` (`everyapi start`/`everyapi configure` 가 아님). 엔진 + 6 개 내장 detector + 커스텀 regex + `everyapi use` 에 통합
- ✅ Seller OAuth onboarding 3 개 provider (codex device / claude paste / gemini loopback)
- ✅ QR sign-in 메인 경로 —— `login` 은 device-code **+ QR 메인 경로** 사용, `--no-qr` 로 fallback
- ✅ Anti-phishing 계층 —— phrase 문자열 (`everyapi topup`), PKCE/state 엄격 체크, cert pinning 모두 구현 완료. cert pinning 은 **report-only** (일치 시 침묵 / 불일치 시 경고 / 연결 거부하지 않음), enforce 는 제품 결정으로 "경고만 하고 적용하지 않음"

## 취약점 보고

[`SECURITY.md`](../SECURITY.md) 참조.
