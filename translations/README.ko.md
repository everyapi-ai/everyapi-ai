> 🌐 [English](../README.md) · [简体中文](README.zh-CN.md) · [日本語](README.ja.md) · **한국어** · [Español](README.es.md) · [Deutsch](README.de.md) · [Français](README.fr.md)

# `everyapi` CLI

[EveryAPI](https://everyapi.ai) AI API 게이트웨이의 buyer onboarding CLI. 감사된 단일 레지스트리를 통해 지원되는 코딩 에이전트를 **1분 안에** 실행합니다.

상태: **핵심 플로우 출시 완료** —— buyer onboarding, seller 명령(plain-key + 3개 프로바이더 OAuth), sanitizer proxy, QR sign-in 메인 경로, 안티피싱 계층이 모두 갖춰져 있습니다. 아직 구현되지 않은 것은 OS 수준 코드 서명과 플랫폼 keychain 백엔드뿐입니다(끝의 "이 바이너리에 아직 포함되지 않은 것" 참고).

## 설치

**macOS (Homebrew):**

```bash
brew tap everyapi-ai/tap && brew install everyapi
```

이후 업그레이드는 먼저 `brew update` 를 실행하세요(하지 않으면 `brew upgrade everyapi` 가 캐시된 formula 를 사용해, 새 릴리스가 있어도 "already installed" 라고 보고합니다):

```bash
brew update && brew upgrade everyapi
```

**Linux / macOS (설치 스크립트):**

```bash
curl -fsSL https://dl.everyapi.ai/install.sh | bash
```

스크립트는 OS + arch 를 자동 감지해 해당하는 `everyapi_{os}_{arch}.tar.gz` 를 내려받고, SHA256 을 검증한 뒤 `~/.local/bin`(root 로 실행하면 `/usr/local/bin`)에 설치합니다. [cosign](https://github.com/sigstore/cosign) 이 설치돼 있으면 keyless 서명도 검증합니다 —— `--require-signature` 를 넘기면 이 단계가 필수가 됩니다(CI / 공급망에 민감한 환경에 권장).

한 줄로 전 세계에서: 스크립트는 실행 시점에 다운로드 소스를 고릅니다 —— 도달 가능하면 GitHub Releases, GitHub 가 느리거나 차단되면 중국 본토 미러 —— 그래서 같은 명령이 중국 안에서도 밖에서도 동작합니다. 특정 미러를 강제하려면 `EVERYAPI_DOWNLOAD_BASE` 를 설정하세요.

자주 쓰는 플래그:

```bash
curl -fsSL https://dl.everyapi.ai/install.sh | bash -s -- --version v0.2.2     # 버전 고정
curl -fsSL https://dl.everyapi.ai/install.sh | bash -s -- --prefix /usr/local  # 접두 경로 지정
curl -fsSL https://dl.everyapi.ai/install.sh | bash -s -- --require-signature  # cosign 검증 실패 시 중단
curl -fsSL https://dl.everyapi.ai/install.sh | bash -s -- --force              # 같은 버전 재설치
```

나중에 업그레이드하려면 같은 명령을 다시 실행하면 됩니다. 스크립트는 최신 릴리스 태그를 해석해 더 새로운 게 있으면 바이너리를 제자리에서 교체합니다. 이미 목표 버전이면 `already at vX.Y.Z — nothing to do` 로 종료합니다(설치 스크립트 / dotfiles 에 넣어도 안전). `--force` 를 넘기면 덮어써서 재설치합니다(무결성 확인이나 손상 파일 복구에 유용). 스크립트 자체도 이 저장소의 [`install.sh`](../install.sh) 에 게시돼 있어, 먼저 내려받아 읽어본 뒤 실행할 수 있습니다.

**Go 사용자 (`go install`):**

```bash
go install github.com/everyapi-ai/everyapi-ai/v3@latest
```

**Windows (PowerShell):**

```powershell
irm https://dl.everyapi.ai/install.ps1 | iex
```

셸 스크립트와 같은 흐름입니다 —— 최신 태그를 해석하고 `everyapi_windows_amd64.zip` + `SHA256SUMS` 를 내려받아 해시(그리고 `PATH` 에 cosign 이 있으면 서명까지)를 검증한 뒤, `everyapi.exe` 를 `%LOCALAPPDATA%\everyapi\bin` 에 설치하고 사용자 `PATH` 에 추가합니다. 버전을 고정하거나 다른 옵션을 넘기려면 스크립트를 먼저 실체화하세요: `& ([scriptblock]::Create((irm https://dl.everyapi.ai/install.ps1))) -Version v0.2.2`. 이 스크립트도 저장소의 [`install.ps1`](../install.ps1) 에 있습니다.

**Windows (수동):** [Releases 페이지](https://github.com/everyapi-ai/everyapi-ai/releases/latest) 에서 `everyapi_windows_amd64.zip`(또는 다른 산출물)을 받아 `SHA256SUMS` 와 대조해 검증한 뒤 바이너리를 `%PATH%` 에 두세요.

## 명령

TTY 에서 인자 없이 `everyapi` 를 실행하면 같은 명령 집합을 대상으로 하는 대화형 런처가 열립니다. `everyapi help` 는 같은 내용을 텍스트로 출력합니다.

| 명령 | 용도 |
|---|---|
| `everyapi auth <sub>` | 로그인 / 로그아웃 및 세션 상태(`login` / `logout` / `status`) |
| `everyapi wallet <sub>` | 충전(안티피싱 phrase 확인 포함), 결제 내역, 결제 수단 |
| `everyapi checkin <sub>` | 오늘의 출석 쿼터 수령; 이번 달 출석 달력 |
| `everyapi account <sub>` | 프로필, 2FA, 추천 코드, 구독 플랜 |
| `everyapi use <tool>` | env 를 설정하고 서드파티 CLI 로 exec(EveryAPI 를 향함) |
| `everyapi token <sub>` | relay API 키 관리(list / create / key / revoke / switch / …) |
| `everyapi models <sub>` | 모델 카탈로그: list / pricing / groups |
| `everyapi stats <sub>` | 사용량, 요청 로그, 모델별 성능, 업스트림 상태 |
| `everyapi market <sub>` | 수요 게시글, 분쟁, 남용 신고 |
| `everyapi inbox <sub>` | 인앱 알림과 다이렉트 메시지 |
| `everyapi seller <sub>` | Marketplace 판매자 명령(list / setup / withdraw / add-key / add-oauth) |
| `everyapi edge <sub>` | BYO-GPU supplier agent 원커맨드 배포(register / start / status / logs / models / rename / pause / resume / stop / update / remove) |
| `everyapi artifacts <sub>` | 자체 완결형 HTML 리포트 발행 및 관리(`share` / `update` / `delete`) |
| `everyapi events` | 실시간 이벤트 스트림(SSE) 구독 |
| `everyapi feedback` | 버그 리포트나 기능 요청을 팀에 전송 |
| `everyapi proxy <sub>` | 로컬 sanitizer proxy(`start` / `stop` / `status` / `configure`) |
| `everyapi computer <sub>` | Accessibility 로 로컬 macOS 앱 창을 읽고 조작 |
| `everyapi mcp` | MCP server 로 실행(stdin/stdout JSON-RPC) |
| `everyapi doctor` | 자체 점검: 자격 증명, 게이트웨이, sanitizer, 설치된 도구 |
| `everyapi settings <sub>` | CLI 환경설정 보기 / 변경(언어, 터미널 모드) |
| `everyapi admin` | 운영자 콘솔 —— 관리자 계정에만 노출 |
| `everyapi version [update\|uninstall]` | 빌드 버전; 업그레이드 확인 및 실행; CLI 제거 |
| `everyapi help` | 전체 명령 목록 출력 |

### `everyapi computer <sub>` —— 로컬 macOS computer use

macOS 용 CLI 는 실행 중인 앱과 창을 찾아내고, 상한이 있는 Accessibility 스냅샷을 반환하며, 시맨틱 동작이나 좌표 동작을 수행할 수 있습니다. 이 기능은 로컬 전용이며 `everyapi mcp` 에는 등록되지 않습니다. Linux 와 Windows 빌드는 명시적으로 `unsupported_platform` 을 반환합니다.

macOS 에서 `everyapi computer` 는 독립적으로 코드 서명된 작은 헬퍼 앱(`clients/desktop/native/computer-use-macos` 에서 빌드되는 `EveryAPI Computer Use.app`)을 로컬 Unix 소켓으로 구동합니다. 설치돼 있지 않으면 처음 사용할 때 자동으로 내려받아 실행하며, EveryAPI Connect 가 이미 번들 사본을 설치해 뒀다면 두 번 내려받지 않고 그것을 재사용합니다. 헬퍼는 스크린샷 지원을 false 로 보고합니다. macOS 가 이 provider 를 통해 신뢰할 만한 공개 창 단위 캡처 식별자를 노출하지 않기 때문이며, 겹쳐 있는 다른 앱이 찍힐 수 있는 화면 영역 캡처로 대체하는 일은 결코 없습니다.

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

`everyapi computer permissions --json` 을 실행한 뒤 시스템 설정 > 개인정보 보호 및 보안 > 손쉬운 사용 에서 **EveryAPI Computer Use** 에 권한을 부여하세요 —— `everyapi` 나 `osascript`, 터미널이 아닙니다. 헬퍼는 자체 번들 아이덴티티를 가진 별도의 서명된 앱이므로 이 권한은 이 한 가지 기능으로 한정됩니다. 머신의 모든 AppleScript 나 JXA 스크립트까지 허용하지 않으며, CLI 와 헬퍼가 업데이트돼도 유지됩니다. `permissions` 는 손쉬운 사용 권한은 그대로 보고하고 Automation 은 `unknown` 으로 보고합니다. 이 provider 는 System Events 에 의존하지 않고 별도의 Automation 사전 점검도 없기 때문입니다.

요소 인덱스는 가장 최근 `get-app-state` 스냅샷에서 나오며 2 분 뒤 만료됩니다. 창은 인덱스(`--window-index`)로 선택하지만 내부적으로는 화면에서 CoreGraphics 가 부여하는 실제 창 단위 id 로 식별하며(사용 가능한 경우), 최소화된 창은 스냅샷 범위의 합성 id 로 대체합니다. 어느 쪽이든 provider 는 내부 지문으로 관측 가능한 변화를 감지하지만, 공개 Accessibility 속성만으로는 속성이 동일한 교체된 창이나 컨트롤이 같은 네이티브 인스턴스임을 증명할 수 없습니다. 캐시는 `~/.config/everyapi/computer-use/state/` 아래에 비공개 권한으로 불투명한 애플리케이션 · 프로세스 · 창 · 경로 · role · frame · 액션 이름 · 지문 데이터만 저장합니다. `app_stale`, `element_stale`, `window_stale` 이후에는 새 스냅샷을 받으세요. GUI 동작이 성공한 뒤 최선 노력 방식의 상태 갱신이 실패해도 그 동작은 여전히 성공입니다. 이때 JSON 에는 재시도 가능한 동작 오류 대신 `refreshError` 가 포함됩니다. 동작을 넘긴 뒤 헬퍼 호출이 중단되거나 잘못된 영수증을 반환하면, `action_outcome_unknown` 은 그 동작이 이미 일어났을 수 있다는 뜻입니다. 재시도 여부를 정하기 전에 상태를 갱신하세요.

알려진 터미널 앱, 비밀번호 관리자, 키체인 접근, 암호, 시스템 설정, EveryAPI Connect 목록은 심층 방어 차원의 마찰로서 차단되며 계속 관리됩니다. 번들 ID 차단은 완전한 애플리케이션 분류기가 아닙니다. 목록에 없는 앱, 터미널이 내장된 편집기, 브라우저, 이름이 바뀌었거나 새로 출시된 앱이 동등한 기능을 노출할 수 있습니다. 실제 신뢰 경계는 여전히 명시적인 `--app` 대상, macOS TCC, 그리고 호출자의 동일 사용자 권한입니다. 읽어 들인 텍스트는 출력 전에 터미널 제어 시퀀스가 제거되고 자격 증명 스캔을 거칩니다. 입력하거나 설정하는 텍스트가 내장 시크릿 탐지기에 걸리면 거부됩니다. 평범한 텍스트를 셸 히스토리에 남기지 않으려면 `--text-stdin` 과 `--value-stdin` 을 우선 사용하세요.

### `everyapi use <tool>` —— 서드파티 CLI 로 exec(EveryAPI 게이트웨이를 향함)

이 CLI 를 설치하는 주된 이유입니다. 지원되는 코딩 클라이언트를 EveryAPI 를 통해 설정하고 실행합니다. 네이티브 통합(`antigravity`, `librefang`)은 자체 인증 경로를 유지하며 복사된 relay key 를 받지 않습니다.

```bash
everyapi use claude            # Claude Code → EveryAPI
everyapi use codex             # OpenAI Codex CLI → EveryAPI
everyapi use opencode          # OpenCode → 프로세스 범위 EveryAPI provider
everyapi use gemini            # Google Gemini CLI → EveryAPI
everyapi use antigravity       # Antigravity(네이티브 Google 인증 및 라우팅)
everyapi use aider             # Aider → EveryAPI(모델 선택)
everyapi use goose             # Goose CLI → EveryAPI(모델 선택)
everyapi use crush             # Crush CLI → 격리된 EveryAPI 모델 카탈로그
everyapi use cline             # Cline CLI → 수명주기에 묶인 provider 설정
everyapi use openclaw          # OpenClaw 로컬 TUI → 격리된 EveryAPI 카탈로그
everyapi use continue          # Continue CLI → 격리된 assistant 설정
everyapi use kilo              # Kilo Code CLI → 프로세스 범위 provider 설정
everyapi use pi                # Pi coding agent → 격리된 모델 카탈로그
everyapi use pi-web            # Pi Web 브라우저 UI → 영속 models.json 에 provider 등록
everyapi use vibe              # Mistral Vibe → 격리된 범용 provider
everyapi use copilot           # GitHub Copilot CLI → 공식 프로세스 범위 BYOK
everyapi use droid             # Factory Droid → 격리된 런타임 설정
everyapi use openhands         # OpenHands CLI → 명시적 프로세스 전용 env 오버라이드
everyapi use forge             # ForgeCode → 격리된 OpenAI 호환 세션
everyapi use llxprt            # LLxprt Code → 격리된 home + 고정 런타임 플래그
everyapi use grok              # xAI Grok Build → EveryAPI
everyapi use qwen-code         # Alibaba Qwen Code → EveryAPI(모델 선택)
everyapi use kimi-code         # Moonshot Kimi Code → EveryAPI(모델 선택)
everyapi use hermes            # Nous Research Hermes Agent → EveryAPI(모델 선택)
everyapi use librefang         # LibreFang 시작(네이티브 EveryAPI 자격 증명 프로세스)
everyapi use open-webui        # Open WebUI 서버 → EveryAPI 를 OpenAI 백엔드로
everyapi use deepseek-harness  # DeepSeek Harness web UI(dsh) → provider 와 자격 증명 생성
everyapi use hermes --model gpt-5.1      # 모델 고정, 선택기 건너뛰기
everyapi use claude                      # 기본은 투명 모드: api.anthropic.com 유지
everyapi use codex                       # api.openai.com 유지
everyapi use antigravity                 # Google 공식 Origin 유지
everyapi use claude --transparent=false  # 투명 모드 해제: 게이트웨이 Base URL + relay key 주입
everyapi use                             # 인자 없음 → 설치된 도구 대화형 선택기
```

도구마다 관례가 다르지만 CLI 가 대신 기억합니다:

| 도구 | EveryAPI 연결 방식 |
|---|---|
| claude | env: `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`; 게이트웨이 탐색으로 실시간 호환 모델 |
| codex | env: `OPENAI_API_KEY` + 세션 유지를 위한 영구 EveryAPI `CODEX_HOME` + 수명주기에 묶인 `--profile` 과 key 범위 모델 카탈로그(codex 는 `OPENAI_BASE_URL` 이 아니라 설정으로 라우팅) |
| gemini | env: `GEMINI_API_KEY`, `GOOGLE_GEMINI_BASE_URL`, `GEMINI_MODEL`; 격리된 auth-mode 설정 오버레이 |
| antigravity | 네이티브 Antigravity 런처(`agy`) |
| aider | OpenAI 호환 env + `openai/<model>` LiteLLM 모델 네임스페이스 |
| goose | `GOOSE_PROVIDER=openai`, `GOOSE_MODEL`, `OPENAI_API_KEY`, `OPENAI_BASE_URL` |
| crush | 프로세스 범위 `CRUSH_GLOBAL_CONFIG`; key 는 env 에서 참조, 모델 카탈로그는 실시간 생성 |
| cline | 수명주기에 묶인 `CLINE_PROVIDER_SETTINGS_PATH`, 종료 후 삭제 |
| openclaw | 로컬 내장 TUI, 프로세스 범위 설정과 env 기반 SecretRef |
| continue | 수명주기에 묶인 `CONTINUE_GLOBAL_DIR/config.yaml`; Continue secret 참조는 env 기반 |
| kilo | 프로세스 범위 `KILO_CONFIG_CONTENT`; OpenCode 호환 provider, key 는 env 기반 |
| pi | `models.json` 과 선택 모델 설정을 담은 격리 `PI_CODING_AGENT_DIR`. 실행 전 `PI_CODING_AGENT_DIR`(기본 `~/.pi/agent`)에 있던 `{extensions,skills,prompts,themes}` 는 절대 경로로 로드 |
| pi-web | *영속* `PI_CODING_AGENT_DIR/models.json`(기본 `~/.pi/agent`)에 `providers.everyapi` 를 병합하므로 세션, 프로젝트 신뢰, 선택한 모델, Models 패널에서 한 편집이 모두 유지됩니다. relay key 는 env 참조로 남고 디스크에 기록되지 않습니다 |
| vibe | 격리된 `VIBE_HOME/config.toml`; `api_key_env_var` 를 가진 범용 provider |
| copilot | 공식 `COPILOT_PROVIDER_*` BYOK 환경; wire API 는 선택한 모델 능력을 따름 |
| droid | 공식 `--settings` 런타임 전용 파일, `custom:EveryAPI-0` 모델 하나와 env 기반 key |
| openhands | `--override-with-envs` 와 프로세스 전용 `LLM_API_KEY`, `LLM_BASE_URL`, `LLM_MODEL` |
| forge | 격리된 `FORGE_CONFIG`; OpenAI 호환 provider/model 을 설정과 프로세스 env 에 고정 |
| llxprt | 격리된 애플리케이션 home 과 예약된 `--provider openai`, `--baseurl`, `--model` 런타임 플래그 |
| grok | env: `XAI_API_KEY`, `GROK_MODELS_BASE_URL`; 격리된 `GROK_HOME`; 필터링된 실시간 모델 탐색 |
| qwen-code | env: `OPENAI_API_KEY`, `OPENAI_BASE_URL`, `OPENAI_MODEL`; 프로세스 범위 `QWEN_HOME` 사용자 설정과 고정된 `--auth-type=openai` |
| kimi-code | env: `KIMI_MODEL_API_KEY`, `KIMI_MODEL_BASE_URL`, `KIMI_MODEL_PROVIDER_TYPE`, `KIMI_MODEL_NAME`; 생성된 모델 별칭이 있는 격리 `KIMI_CODE_HOME` |
| hermes | 생성된 `HERMES_HOME/config.yaml`(이름 있는 custom provider, `base_url`, 인라인 `api_key`); 필터링된 실시간 모델 탐색 |
| librefang | 네이티브 `librefang start`. 데몬을 detach 하고 터미널을 돌려줍니다(`librefang stop` 으로 종료). LibreFang 은 요청마다 현재 EveryAPI 자격 증명을 해석합니다 |
| open-webui | `OPENAI_API_BASE_URLS`, `OPENAI_API_KEYS`, `ENABLE_PERSISTENT_CONFIG=false` 와 함께 `open-webui serve` 로 실행하므로 저장된 설정보다 프로세스 env 가 우선합니다. `DATA_DIR` 은 `~/.open-webui` 로 고정 |
| deepseek-harness | 공식 `dsh web` UI. `$DSH_HOME/settings.yaml`(기본 `~/.dsh`, 모드 `0700`)에 `llm-pi-ai.providers.everyapi` 항목을 생성하고, key 는 `0600` `.credentials.yaml` 에 보관 |

어떤 도구가 어떤 변수명을 읽는지, `/v1` 을 붙여야 하는지, 어떤 auth 헤더 방식인지 —— 더 이상 찾아볼 필요가 없습니다.

**relay key 선택**: `--group` 없이 실행하면 계정의 auto-group key —— 접근 가능한 모든 그룹으로 라우팅되는 그 하나의 key —— 를 해석해 `credentials.json` 에 캐시합니다. auto key 가 없는 계정(또는 해당 그룹을 더 이상 쓸 수 없게 된 tier)은 가장 최근에 활성화된 key 로 폴백합니다. 다른 key 를 기본으로 고정하려면 `everyapi token switch`, 다른 풀로 한 번만 실행하려면 `--group <id>` 를 넘기세요. 그룹 오버라이드는 절대 그 캐시에 기록되지 않습니다. 어떤 key 를 쓰는지가 아래 카탈로그를 결정합니다: 한 그룹에 고정된 key 는 그 그룹의 모델만 보게 됩니다.

이전 실행에서 캐시된 key 는 계속 사용됩니다 —— 이 조회는 의도적으로 오프라인이라 스스로 다시 고르지 않습니다. `/model` 에 한 그룹의 모델만 보인다면 `everyapi token switch` 를 실행해 한 번 `Auto` 를 선택하세요.

**모델 선택**: 실행 시 EveryAPI 는 선택된 relay key/group 에서 사용 가능한 실시간 카탈로그를 가져와 호환되지 않는 미디어/embedding 프로토콜을 제거하고, 그 스냅샷을 라우팅되는 각 클라이언트의 네이티브 선택기에 주입합니다. Claude Code, Codex, Qwen Code, Kimi Code 에서는 `/model` 을, Grok 에서는 `/model`/`models` 를, Hermes 에서는 `hermes model` 을 사용하세요. Claude 가 아닌 모델 ID 는 내부적으로 Claude 호환 별칭으로 표현되지만, 표시와 상위 전송은 실제 ID 로 이뤄집니다.

`ModelEnv` 계약을 가진 도구(Gemini, Aider, Goose, Crush, Cline, OpenClaw, Continue, Kilo, Pi, Vibe, GitHub Copilot CLI, Factory Droid, OpenHands, ForgeCode, LLxprt, Hermes, Qwen Code, Kimi Code)는 EveryAPI 선택기를 엽니다. `--model <id>` 를 넘기면 건너뜁니다. 비대화형 실행에서는 EveryAPI 가 결정적으로 첫 번째 호환 모델을 사용합니다. 순수 claude/codex/grok 은 자체 부팅 모델 동작을 유지합니다. `antigravity` 는 Google 인증으로 네이티브 `agy` 를 실행하고, `librefang` 은 자체 EveryAPI 자격 증명 프로세스를 사용합니다. `pi-web`, `open-webui`, `deepseek-harness` 는 브라우저 UI 를 제공합니다. EveryAPI 가 provider 와 호환 카탈로그 전체를 미리 등록하고, 모델은 터미널 선택기가 아니라 그 UI 안에서 고릅니다.

**reasoning level**: 모델 다음으로 `everyapi use codex` 와 `everyapi use pi` 는 어느 reasoning level 로 실행할지 묻고 그 답을 다음 실행을 위해 기억합니다 —— 한 번 묻고 이후에는 확인 없이 재사용하며, 아래 안전 설정과 동일한 방식입니다. 두 클라이언트의 조건이 다른 이유는 아는 정보가 다르기 때문입니다. Codex 는 자체 번들 카탈로그가 해당 모델에 대해 공개하는 단계(`low` … `ultra`, 모델마다 다름 —— `gpt-5.6-sol` 은 `ultra` 까지, `gpt-5.5` 는 `xhigh` 까지)를 읽고 선택을 `model_reasoning_effort` 로 받습니다. 이를 위해 게이트웨이에 묻지 않으므로 Codex 가 모르는 모델에는 이 단계가 나타나지 않습니다. Pi 는 custom provider 에 대한 모델별 테이블이 없어, 게이트웨이가 해당 모델이 effort 를 받는다고 확인한 경우(`/v1/models` 의 `supports_thinking`)에만 이 단계가 나타납니다. 선택지는 `off` … `high` 이며 `defaultThinkingLevel` 로 전달됩니다. 현재 모델이 제공하지 않는 기억된 레벨은 고정하지 않고 버립니다. 이 기능 출시 후 첫 실행에서는 커서가 Codex 의 영구 home 에 이미 있던 effort 에서 시작하므로, 기본값을 그대로 받아들이면 아무것도 바뀌지 않습니다. 두 클라이언트의 세션 내 컨트롤 —— Codex 의 `/model`, pi 의 shift+tab —— 은 그대로 유지되고, 실행 간 선택은 런처가 보관합니다. Codex 가 생성한 profile 과 Pi 의 격리 home 은 종료 시 삭제되기 때문입니다.

프로바이더 이름은 CLI 이름이 아닙니다: 해당 벤더의 공식 클라이언트에는 `qwen-code` 또는 `kimi-code` 를 사용하고, 프로바이더 모델은 지원 클라이언트의 실시간 모델 카탈로그에서 선택하세요.

**hermes 설정 격리**: `everyapi use hermes` 는 `HERMES_HOME` 을 `~/.config/everyapi/sessions` 아래의 프로세스 범위 디렉터리로 리다이렉트합니다. 자격 증명이 담긴 설정과 실시간 proxy URL 은 종료 시 삭제되며 다른 key/group 과 충돌할 수 없습니다. 안전한 설정으로 유지되는 것은 마지막으로 선택한 모델 ID 뿐입니다. 개인 `~/.hermes` 는 건드리지 않습니다. 생성된 설정은 EveryAPI 를 이름 있는 custom provider 로 등록하므로 `hermes model` 이 OpenRouter 로 떨어지지 않고 모델을 탐색·전환할 수 있습니다. 순수 `hermes` 는 대화형 채팅을 열며, 터미널 UI 가 필요하면 `everyapi use hermes -- --tui` 를 사용하세요.

**grok 설정 격리**: `everyapi use grok` 는 `GROK_HOME` 을 `~/.config/everyapi/grok-home` 으로 리다이렉트합니다. 이는 캐시된 xAI 브라우저 세션이 EveryAPI relay key 를 덮어쓰는 것을 막고, EveryAPI 로 라우팅된 세션을 순수 `grok` 과 분리합니다. Grok 전용 플래그는 `--` 뒤에 넘기세요. 예: `everyapi use grok -- --model grok-4.5`.

**Qwen/Kimi 설정 격리**: 라우팅된 실행마다 `~/.config/everyapi/sessions` 아래 프로세스 범위 home 을 받고 자식 프로세스 종료 시 삭제되므로, 동시에 쓰는 key/group 이 서로의 카탈로그나 loopback URL 을 덮어쓸 수 없습니다. Qwen 의 실제 시스템 설정은 그대로 유지되며 관리자 우선순위도 보존됩니다. 관리자 또는 워크스페이스 설정이 `modelProviders.openai` 를 정의해 실시간 EveryAPI 카탈로그를 가릴 경우, 오래되거나 호환되지 않는 모델을 조용히 보여주는 대신 조치 가능한 충돌로 실행을 중단합니다.

> ⚠️ **서브프로세스 env 안전 참고**: 위 환경 변수에는 당신의 relay API key 가 들어 있습니다. 서드파티 CLI 는 debug / verbose 모드에서 env 를 로깅할 수 있습니다 —— `everyapi use` 실행 전에, 켜려는 debug 플래그가 `*_TOKEN` / `*_API_KEY` 를 흘리지 않는지 확인하세요. debug 로그를 공유하기 전에 `sed -i 's/sk-everyapi-[A-Za-z0-9]*/REDACTED/g'` 를 실행하세요.

#### 투명 Connector(기본)

투명 모드는 서드파티 Base URL 을 설정하는 대신, 지원 클라이언트가 벤더 공식 API Origin 에 그대로 머물게 합니다. 이를 지원하는 모든 도구에서 기본값이며, 해제하려면 `--transparent=false` 를 넘기세요. CLI 는 임의의 loopback 포트에 임시 HTTP CONNECT proxy 를 띄우고, 실행마다 CA 를 생성하며 그 개인 키는 메모리에만 존재합니다. 자식 프로세스에는 proxy URL, 공개 CA 번들, 비밀이 아닌 자리표시자 자격 증명만 전달됩니다. 등록된 모델 경로는 로컬에서 복호화되어 실제 relay key 와 함께 EveryAPI 로 중계되고, 다른 HTTPS 호스트는 원시 CONNECT 패스스루를 사용합니다. 보호된 모델 접두 아래의 알 수 없는 경로는 차단되며, 중계 실패가 벤더로 폴백하는 일은 없습니다.

Claude Code 와 Codex CLI 로 검증했으며, 기본값이 적용되는 것도 이 두 도구입니다. 네이티브 Antigravity 와 LibreFang 은 connector 를 우회합니다. 나머지 등록된 도구는 문서화된 주입/설정 경로를 사용하므로, 미지원 도구에 명시적으로 `--transparent` 를 넘기면 분명하게 실패합니다.

`--sanitize` 는 투명 모드와 충돌하지 않고 조합됩니다: connector 가 sanitizer 를 거쳐 중계하므로(자식 → connector → sanitizer → 게이트웨이) 마스킹과 Claude 복구 응답 가드가 두 실행 경로 모두에 적용됩니다.

프록시 변수가 `ALL_PROXY` 뿐이라면 투명 모드는 거절되고 주입 경로로 폴백합니다 —— Go 의 프록시 해석은 `ALL_PROXY` 를 읽지 않아 connector 가 이를 존중할 수 없습니다. 투명 모드를 유지하려면 `HTTPS_PROXY`(socks5 포함, net/http 가 네이티브로 연결)를 설정하세요.

이 모드는 실험적이며 의도적으로 프로세스 범위입니다:

- 가로채는 클라이언트 쪽은 현재 HTTP/1.1 을 사용하며 일반 JSON/SSE 요청을 지원합니다(게이트웨이의 HTTP/2 응답은 HTTP/1.1 로 변환). 클라이언트 측 HTTP/2, HTTP/3/QUIC, WebSocket, 인증서 피닝 클라이언트, `HTTPS_PROXY` 를 무시하는 클라이언트는 대상이 아닙니다;
- Codex 내장 OpenAI provider 는 Responses WebSocket 을 한 번 프로브합니다. Connector 가 HTTP 426 을 반환하므로 Codex 는 재시도 예산을 쓰지 않고 즉시 HTTPS/SSE 로 폴백합니다. Codex 가 그 실패 프로브 로그 한 줄을 출력할 수는 있습니다;
- Claude Code 는 비밀이 아닌 자리표시자를 여전히 API-key 인증으로 취급하므로, `ANTHROPIC_BASE_URL` 이 공식 `https://api.anthropic.com` Origin 이어도 claude.ai connectors 는 비활성화됩니다. 투명 모드가 피하는 것은 서드파티 Origin 탐지이며, API-key 인증을 claude.ai OAuth 로그인처럼 만들 수는 없습니다;
- 시스템 CA 를 설치하지 않고, 관리자 권한도 필요 없으며, `everyapi use` 의 기본 동작도 바꾸지 않습니다;
- 탐지 불가능하지 않습니다: 클라이언트는 프록시 변수, 로컬 인증서 체인, 소켓, 타이밍, 응답 차이를 조사할 수 있습니다;
- Connector 는 복호화된 모델 내용을 봅니다. CA 서명 키는 기록되거나 업로드되지 않으며, 공개 CA 파일은 종료 시 삭제됩니다;
- relay key 는 자식 프로세스 환경과 생성된 클라이언트 설정에 없지만, 기존 `~/.config/everyapi/credentials.json` 은 같은 OS 사용자로 실행되는 모든 프로세스가 읽을 수 있습니다. 투명 모드는 자격 증명 주입 격리이지 적대적 자식 프로세스에 대한 샌드박스가 아닙니다.

### `everyapi auth login` —— Device Authorization Grant + QR 로그인

Device Authorization Grant(RFC 8628 형태) + docs §7-5 Layer 1 "기기 간 QR 로그인" 을 사용합니다:

1. CLI 가 세션을 만들고 **터미널에 QR 을 렌더링 + 짧은 코드와 URL 출력**
2. 휴대폰으로 QR 스캔(또는 EveryAPI 에 이미 로그인된 브라우저에서 URL 열기) —— QR 안의 URL 에 이미 `?code=USR-789` 가 실려 있어 대시보드가 코드를 자동 입력하므로, 사용자는 Approve 만 누르면 됩니다
3. CLI 가 access token 을 받아 `~/.config/everyapi/credentials.json`(mode 0600)에 저장합니다

```bash
everyapi auth login                                    # 프로덕션. 기본으로 QR 렌더링 + 브라우저 열기
everyapi settings set gateway_region cn               # 이후 명령에서 중국 가속 게이트웨이 사용
everyapi auth login --api-base http://localhost:8787   # 로컬 개발 / 셀프호스트
everyapi auth login --no-browser                       # 브라우저 자동 실행 안 함(QR 스캔)
everyapi auth login --no-qr                            # QR 렌더링 안 함(비 UTF-8 터미널 / 파이핑)
```

터미널 QR 렌더링 예(Unicode 반블록 문자, 약 18-20 행 높이):

```
█▀▀▀▀▀█  ▀▀ ▄  █▀▀▀▀▀█
█ ███ █  ▀▄█▀  █ ███ █
█ ▀▀▀ █ ▄ ▀ █▀ █ ▀▀▀ █
▀▀▀▀▀▀▀ █▄█▄█▄ ▀▀▀▀▀▀▀
... (실제 QR 은 verification_uri?code=USR-789 를 인코딩)
```

이것이 더 강한 안티피싱 경로인 이유:

- 사용자가 **새 기기에서 비밀번호를 입력하지 않습니다** → 피싱 사이트가 자격 증명을 가로챌 기회가 없습니다
- 사용자가 **낯선 브라우저 페이지로 리다이렉트되지 않습니다** → 웹 리다이렉트 피싱 면이 사라집니다
- CLI 가 가짜 QR 을 만드는 악성 fork 라 해도, 스캔 후의 승인 페이지는 진짜 everyapi.ai 대시보드(사용자가 이미 로그인한 기기에서 트리거됨)이며, 낯선 코드를 사용자가 Approve 하지는 않습니다

docs §7-5 의 나머지 계층(cert pinning / phrase 문자열 / PKCE OAuth)은 각각 독립 PR 로 반영됐습니다(cert pinning 은 report-only, enforce 는 출시하지 않는다는 제품 결정).

### `everyapi seller <sub>` —— marketplace 판매자 서브커맨드

대시보드의 채널 등록·출금 플로우를 터미널로 가져와 스크립트화된 onboarding 을 가능하게 합니다. 채널을 등록하기 전에 `seller setup` 이 자격(계정 활성 / 이메일 인증 / 계정 연령 / 소비 이력 / 채널 상한)을 확인하고, 실패한 게이트는 **사용자가 key 를 입력하기 전에** 나열됩니다. 제출 후 422 로 알게 되는 일을 피하기 위해서입니다.

```bash
everyapi seller list                          # 등록된 채널 목록
everyapi seller withdraw                      # 대기 중인 판매자 수익 전체를 메인 잔액으로 이동
everyapi seller withdraw --quota 1000         # 부분 이체(DB 단위)
everyapi seller add-key   --type claude --name 'my-pro' \
                        --key 'sk-ant-...' --models 'claude-3-opus,claude-3-sonnet'
everyapi seller add-oauth codex --name 'my-chatgpt' --models 'gpt-4'
                                            # 원클릭 OAuth: CLI 가 device flow 를 시작하고, 사용자가
                                            # 브라우저에서 user_code 를 입력하면 token 이 채널에 안착
everyapi seller add-oauth claude --name 'my-claude' --models 'claude-3-opus,claude-3-sonnet'
                                            # paste flow: CLI 가 Anthropic 인가 페이지를 열고, 사용자가
                                            # callback 에 표시된 code#state 를 터미널에 붙여넣음
everyapi seller add-oauth gemini --name 'my-gemini' --models 'gemini-1.5-pro'
                                            # 진짜 원클릭 loopback: CLI 가 임의 포트 listener 를 띄우고,
                                            # Google 이 code 를 CLI 로 직접 보내므로 붙여넣기 불필요
everyapi seller setup                         # 대화형 마법사: 자격을 먼저 확인한 뒤 add-key 안내
```

#### `add-key` —— 다중 key 백업 풀

`--key` 는 반복 지정할 수 있어, 같은 채널에 동등한 자격 증명 N 개를 백업 풀로 등록할 수 있습니다(B2, PRODUCT §4.5). 주 key 가 401/403 을 반환하면 백엔드가 자동으로 다음으로 페일오버합니다. `--key-remark` 도 반복 가능하며 `--key` 와 위치로 대응합니다(i 번째 `--key-remark` 가 i 번째 `--key` 의 라벨이 되어 나중에 대시보드에서 식별하는 데 쓰입니다). OAuth blob 은 백업 풀에 넣을 수 없습니다 —— 단일 key 채널로만 유지됩니다.

```
everyapi seller add-key   --type claude --name 'claude-pool' \
                        --models 'claude-3-opus' \
                        --key 'sk-ant-primary' --key-remark 'primary' \
                        --key 'sk-ant-backup'  --key-remark 'team backup'
```

`add-key` 의 `--type` 은 별칭(`openai` / `claude` / `gemini` / `codex` / `vertex` / `aws` / `xai` / `deepseek`) 또는 숫자 id 를 받습니다. 등록은 marketplace 자격 조건(계정 활성, 이메일 인증, 소비 이력, 채널 상한)의 적용을 받으며, CLI 는 세 진입점(`add-key` / `add-oauth` / `setup`) 모두에서 다른 작업보다 먼저 실패한 체크리스트를 나열합니다.

#### `add-oauth codex` —— 원클릭 OAuth(device flow)

`everyapi seller add-oauth codex --name 'my-chatgpt' --models 'gpt-4'` 는 Codex / ChatGPT 의 RFC 8628 유사 device authorization flow 를 진행합니다 —— 판매자는 **토큰 문자열을 전혀 만지지 않습니다**:

1. CLI 가 `/api/seller/codex/device/start` 를 호출해 짧은 `user_code` 와 `verification_uri` 를 받습니다
2. CLI 가 기본으로 `https://auth.openai.com/codex/device` 를 브라우저로 엽니다(`--no-browser` 로 생략). 사용자가 브라우저에서 `user_code` 를 입력해 인가를 완료합니다
3. CLI 가 `/api/seller/codex/device/poll` 을 폴링합니다. 인가되면 백엔드가 채널을 만들고 OAuth token 을 채널의 `key` 필드에 씁니다
4. 출력: 채널 id + 연결된 ChatGPT 이메일

인가 cookie 는 프로세스 내 `http.CookieJar` 가 관리하며 영속화되지 않습니다 —— device flow 상태는 수명이 짧고 프로세스에 묶여 위협 모델과 일치합니다.

#### `add-oauth claude` —— paste-and-submit OAuth

`everyapi seller add-oauth claude --name … --models …`. Anthropic 의 OAuth provider 는 자기 쪽에서 `redirect_uri` 를 `https://console.anthropic.com/oauth/code/callback` 로 하드코딩하기 때문에, CLI 는 localhost listener 로 callback 을 받을 수 없습니다. 흐름:

1. CLI 가 `/api/seller/claude/oauth/start` 를 호출. 백엔드가 PKCE 쌍 + state 를 만들고 Anthropic 의 authorize URL 을 반환
2. CLI 가 기본으로 브라우저를 엶(`--no-browser` 로 생략). 사용자가 Anthropic 에 로그인하고 승인
3. Anthropic 이 `<code>#<state>` 문자열을 보여주는 callback 페이지로 리다이렉트
4. **사용자가 그 문자열을 CLI 에 붙여넣습니다**
5. CLI 가 `/api/seller/claude/oauth/complete` 를 호출. 백엔드가 code+verifier 를 token 으로 교환하고 채널을 생성

device flow 보다 붙여넣기 한 단계가 더 있지만, 손으로 `~/.claude/auth.json` 을 찾는 것보다 훨씬 쉽습니다. 세션 cookie 는 start 시 백엔드가 발급하며 complete 는 같은 세션에 도달해야 합니다 —— CLI 의 `http.CookieJar` 는 프로세스 내부이며 호출마다 격리됩니다.

#### `add-oauth gemini` —— 진짜 원클릭 loopback OAuth

`everyapi seller add-oauth gemini --name … --models … [--no-browser] [--timeout 5m]`. Google 의 gemini-cli installed-app OAuth 클라이언트는 `http://127.0.0.1:<port>/callback` 을 `redirect_uri` 로 받아들이므로 **CLI 가 직접 listener 를 띄워 callback 을 받습니다** —— 사용자는 브라우저로 로그인만 하면 되고 붙여넣을 것이 없습니다. 흐름:

1. CLI 가 임의 ephemeral 포트(`127.0.0.1:0`)에 일회성 HTTP listener 를 띄웁니다. 경로는 고정 `/callback`
2. CLI 가 `redirect_uri = http://127.0.0.1:<port>/callback` 과 함께 `/api/seller/gemini/oauth/start` 를 호출. 백엔드가 리다이렉트를 엄격 검증: loopback / port ≥ 1024 / scheme=http / path=/callback / query·fragment·userinfo 없음(SSRF + 리다이렉트 탈취 방지)
3. CLI 가 기본으로 브라우저를 엶. 사용자가 Google 에 로그인하고 동의
4. Google 이 `?code=…&state=…` 를 CLI 의 listener 로 리다이렉트
5. CLI 가 state 일치를 검증하고(오래된 플로우 / 위조 방지) `/api/seller/gemini/oauth/complete` 를 호출
6. 백엔드가 code + 동일한 redirect_uri 를 token 으로 교환하고 채널을 생성

다른 두 프로바이더와의 비교:

| Provider | UX | 이유 |
|---|---|---|
| `codex` | 사용자가 브라우저에서 6자리 user_code 입력, CLI 가 자동 폴링 | OpenAI device flow, redirect_uri 없음 |
| `claude` | 사용자가 브라우저에서 로그인하고 `code#state` 를 CLI 에 붙여넣음 | Anthropic 이 redirect_uri 를 자사 callback URL 로 하드코딩 |
| `gemini` | 사용자가 브라우저에서 로그인하고 탭을 닫으면 완료 | Google 이 loopback 리다이렉트를 허용 |

`--timeout` 이 대기 시간을 제한합니다(기본 5분). 타임아웃 시 CLI 가 종료되며 listener 를 깔끔하게 닫습니다.

### `everyapi edge <sub>` —— BYO-GPU supplier agent 원커맨드 배포

유휴 GPU 를 EveryAPI 를 통해 판매할 수 있게 합니다. CLI 는 배포를 하나의 명령 집합 —— `register` / `list` / `start` / `status` / `logs` / `models` / `rename` / `pause` / `resume` / `stop` / `update` / `remove` —— 으로 압축해, 공급자가 docker-compose 를 손으로 옮기거나 `.env` 를 채우거나 등록 토큰을 들고 다니지 않아도 되게 합니다. 보통의 경로는 8개 명령입니다:

```bash
everyapi auth login                              # 기존 로그인 재사용
everyapi edge register --name "rtx-4090"    # /api/seller/edge/nodes 호출로 node_id + token 획득, ~/.local/share/everyapi/edge/<id>/ 에 기록
everyapi edge start                         # NVIDIA / ROCm / Apple Silicon / CPU 자동 감지, docker compose up -d
everyapi edge models pull llama3.1:8b       # docker compose exec ollama ollama pull ...
everyapi edge status                        # 로컬 docker compose ps + 대시보드 online/offline
everyapi edge logs -f                       # 로그 tail
everyapi edge update                        # docker compose pull && up -d
everyapi edge remove                        # down -v + 로컬 디렉터리 삭제 + backend DELETE
```

`start` 는 `text/template` 로 런타임에 `docker-compose.yml` 을 렌더링합니다(**임베드된 정적 YAML 이 아닙니다**) —— 덕분에 컨테이너 이름을 node_id 로 네임스페이스화할 수 있어 한 호스트의 여러 노드가 충돌하지 않고, GPU 패스스루가 모드별로 조건부 렌더링됩니다(NVIDIA = `deploy.resources.devices` + nvidia driver, ROCm = `/dev/kfd` + `/dev/dri` + `group_add: video`, macOS = ollama 컨테이너 없이 agent 가 `host.docker.internal` 로 호스트의 네이티브 ollama 에 접속).

자격 증명 흐름: CLI 가 기존 `sk-everyapi-` Bearer 로 `POST /api/seller/edge/nodes` 호출 → 백엔드가 `registration_token` 을 한 번만 반환(이후에는 sha256 만 저장하고 다시 표시하지 않음) → CLI 가 0600 으로 `~/.local/share/everyapi/edge/<id>/node.json` 에 기록 → compose 의 `EVERYAPI_REGISTRATION_TOKEN` env 로 렌더링. **등록 토큰은 어떤 .env 파일에도 기록되지 않습니다**(공급자가 실수로 커밋하지 않도록).

요구사항: `docker` + `docker compose v2`(v1 은 EOL 이며 미지원). macOS 에서는 `brew install ollama && brew services start ollama`(Metal 가속은 docker 컨테이너 안에서 동작하지 않습니다).

### `everyapi wallet topup` —— 안티피싱 phrase 가 붙은 충전 리다이렉트

`everyapi wallet topup` 은 대시보드 충전 페이지를 엽니다. 리다이렉트 전에 docs §7-5 Layer 3 검증을 거칩니다:

1. CLI 가 백엔드 `POST /api/cli/jump-session` 을 호출해 세션 id + 이모지 4개 phrase 문자열(예: `🌊 🦊 🍕 🚀`)을 받습니다
2. CLI 가 URL 과 phrase 를 모두 터미널에 출력하며 "잠시 후 페이지 상단에 같은 phrase 가 보여야 한다" 고 알립니다
3. 사용자가 Enter 를 누르면 CLI 가 시스템 브라우저로 URL 을 엽니다(`?jump_session=<id>` 포함)
4. 대시보드가 로드되면서 백엔드 `GET /api/cli/jump-session/:id/phrase` 를 호출해 같은 phrase 를 받고 **페이지 헤더에 눈에 띄게 표시**합니다
5. 사용자가 눈으로 비교합니다: 일치 → 진짜 EveryAPI, 불일치하거나 표시되지 않음 → 탭을 닫으세요(피싱 가능성)

이것이 피싱을 막는 이유: phrase 는 임의의 32-hex 세션 id 를 키로 백엔드 메모리에 존재합니다. 피싱 사이트에는 그것을 가져올 인증 경로가 없고, 공격자가 위조한 `wallet/topup?jump_session=<id>` 로도 phrase 를 읽을 수 없습니다. 짧은 TTL(10분) + 1회성 사용(대시보드가 한 번 가져오면 세션 삭제)이 재사용 위험을 더 줄입니다.

```bash
everyapi wallet topup                    # 기본으로 브라우저 열기
everyapi wallet topup --no-browser       # URL 만 출력, 수동 복사
```

### `everyapi auth status` —— 현재 잔액 / 사용량 / 쿼터

```
$ everyapi auth status

  alice (alice@example.com)
  quota:     $12.34 remaining   $5.67 used
  requests:  1,234
  topup:     https://app.everyapi.ai/wallet
```

### `everyapi version update` —— 업그레이드를 자동 실행

톱레벨 `everyapi update` 는 없습니다. CLI 수명주기 동작은 `version` 아래에 있습니다(`everyapi version update`, `everyapi version uninstall`).

GitHub 미러의 최신 릴리스를 확인해 현재 버전과 비교한 다음, 이 바이너리를 설치한 경로에 업그레이드를 넘깁니다 —— Homebrew(`brew update && brew upgrade everyapi`), `go install …@latest`, 또는 공개된 설치 스크립트. 한 명령으로 끝, 복사·붙여넣기 없음.

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

왜 바이너리를 직접 바꾸지 않을까요? Homebrew 와 Go 의 검증 체인(SHA / bottle 서명 / 모듈 체크섬)이 CLI 안에서 다시 만들 어떤 것보다 견고하고, 실행 중인 실행 파일을 자기 자신이 교체하는 것은 Windows 에서 지뢰밭이기 때문입니다. 설치 스크립트로 설치한 경우에는 실제로 제자리 교체가 일어나지만, 그 작업을 안전하게 처리하는 공개 설치 스크립트를 다시 실행하는 방식입니다.

플래그:
- `--check` —— 조용히 비교. 최신이면 exit 0, 오래됐으면 exit 1, 최신 버전을 확인할 수 없으면 exit 2(이유는 stderr 로) —— 네트워크 문제가 "업그레이드 있음" 으로 읽혀서는 안 되기 때문입니다. CI / cron 용:
  ```bash
  everyapi version update --check || echo "needs upgrade"
  ```
- `--dry-run` —— 실행될 명령을 출력만 하고 실제로 실행하지 않음(확인용)

### `everyapi settings` —— CLI 환경설정(언어 등)

CLI 는 8개 언어 i18n 을 내장합니다: 영어, 중국어 간체, 중국어 번체, 일본어, 한국어, 스페인어, 독일어, 프랑스어 —— CLI 문자열은 사용자가 선택한 언어로 렌더링됩니다. 백엔드 API 오류는 `Accept-Language` 헤더로 자동 협상하며 같은 8개 언어를 지원합니다.

```bash
$ everyapi settings                          # 대화형 선택기: 언어 선택
$ everyapi settings list                     # 현재 설정 보기
$ everyapi settings set language zh          # 직접 설정
$ everyapi settings set language fr          # 프랑스어도 동일
$ everyapi settings set terminal_mode tmux   # 대화형 도구 실행을 tmux 안에 유지
$ everyapi use codex -- resume               # 유일하게 살아 있는 프로젝트 tmux 에 재접속, 또는 Codex 선택기 열기
$ everyapi settings reset                    # 기본값으로 초기화(en + LANG 자동 감지)
```

**Terminal mode**: 첫 대화형 `everyapi use` 는 실행을 네이티브 터미널에 둘지 tmux 에서 돌릴지 묻고, 그 선택을 `terminal_mode` 로 저장합니다. tmux 모드는 `everyapi use` 프로세스 전체를 `everyapi-v3-*` 세션 안에서 재시작합니다. 이 세션은 선택한 도구, 워크스페이스 파일시스템 동일성, 임의의 128비트 실행 동일성으로 식별되므로 connector, sanitizer, 임시 설정, 대상 도구가 모두 detach 후에도 살아남습니다. 실행 메시지는 정확한 `tmux attach -t <session>` 명령을 출력합니다. 순수 Codex `resume` 은 먼저 이 동일성을 찾습니다: 살아 있는 관리 agent 창이 하나면 정확한 세션 이름으로 재검증해 다시 붙이고, 0개 또는 여러 개면 추측하지 않고 일반 Codex resume 선택기로 폴백합니다. 매 tmux 실행 전에 CLI 는 엄격히 생성된 `everyapi-v3-*`, `everyapi-v2-*`, 또는 레거시 `everyapi-<pid>-<timestamp>` 세션만 후보로 고려하며, 단일 원자적 tmux 명령으로 "유일한 window 에 유일하고 이미 죽은 EveryAPI 래퍼 창만 있다" 는 것을 재검증한 경우에만 제거합니다. 살아 있는 detach 된 agent, 사용자가 만든 일반 tmux 세션, 사용자가 추가한 창이나 window 를 포함한 세션은 모두 보존됩니다. 관리 창은 죽었지만 사용자 추가 창이 살아 있는 세션은 보존되되 재사용되지 않습니다. 실행된 각 클라이언트는 `EVERYAPI_TERMINAL_MODE`, `EVERYAPI_TMUX_SESSION`, `EVERYAPI_TMUX_ATTACH_COMMAND` 를 확인할 수 있습니다. Codex, Claude Code, OpenCode, Kilo 는 추가로 문서화된 모델 지시 표면을 통해 같은 세션 컨텍스트를 받으며, 여기에는 중첩 tmux 세션을 만들지 말라는 규칙이 포함됩니다. 다른 클라이언트는 사용자 메시지 주입 없이 환경 계약만 유지합니다. 이미 tmux 안인 실행은 중첩되지 않고, 비대화형 실행은 항상 네이티브로 남습니다. tmux 를 쓸 수 없으면 첫 실행 선택기가 그 옵션을 비활성화합니다. 기존 tmux 설정이 있으면 조용히 동작을 바꾸는 대신 설치 / 되돌리기 안내와 함께 실패합니다.

**자동 감지**: 명시적으로 설정하지 않았다면, CLI 는 시작할 때 `EVERYAPI_LANG > LC_ALL > LC_MESSAGES > LANG` 순서로 환경 변수를 읽습니다. 시스템 로케일이 `zh_CN.UTF-8` / `ja_JP.UTF-8` / `fr_FR.UTF-8` 등이면 즉시 적용됩니다 —— 설정 불필요.

**일회성 오버라이드**:

```bash
EVERYAPI_LANG=zh everyapi auth status             # 이번 호출만 중국어로 표시, 저장되지 않음
```

**번역 예시**(미로그인 오류, 8개 언어 × 같은 문장):

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

설정은 `~/.config/everyapi/settings.json` 에 저장됩니다(`credentials.json` 과 같은 디렉터리지만 mode 는 `0644` —— 비밀이 없습니다).

**번역 개선 / 언어 추가**: [`internal/i18n/locales/README.md`](../internal/i18n/locales/README.md) 를 참고하세요.

## 설정 파일

자격 증명은 `~/.config/everyapi/credentials.json`(`$XDG_CONFIG_HOME` 이 설정돼 있으면 `$XDG_CONFIG_HOME/everyapi/`)에 파일 모드 `0600` 으로 저장됩니다. `everyapi auth login` 이 쓰고, 다른 모든 명령이 읽습니다.

> ⚠️ **토큰은 평문으로 저장됩니다**. 파일 모드 `0600` + 비공개 `$HOME` 경로는 `gh auth` / `aws configure` 같은 업계 CLI 의 관행과 같지만, **가정용 기기 도난 / 멀웨어 위협 모델에서는** 이 파일을 읽을 수 있는 모든 프로세스가 당신으로서 EveryAPI API 를 호출할 수 있습니다(MCP 도구 포함 —— 아래 §money-path friction step 참고). 권장 사항:
> - 공유 / 공용 기기에서 `everyapi auth login` 하지 마세요
> - macOS 사용자: FileVault 를 켜기 전에 `everyapi auth logout` 을 고려하세요
> - Linux 사용자: 홈 디렉터리 암호화(`ecryptfs` / LUKS)를 켜세요
> - 유출이 의심되면 → `everyapi auth logout` 으로 즉시 로컬 자격 증명을 지우고, EveryAPI 대시보드에서 API key 를 교체하세요
>
> 플랫폼 keychain 백엔드(macOS Keychain / Windows DPAPI / Linux Secret Service)는 계획 중이며 아직 출시되지 않았습니다.

필드:

- `api_base` —— EveryAPI 게이트웨이 URL. 기본값 `https://api.everyapi.ai`. 셀프호스트 사용자 / 로컬 개발은 `auth login` 시 `--api-base` 로 덮어쓸 수 있습니다.
- `access_token` —— 인증이 필요한 모든 API 호출에 쓰는 bearer.
- `relay_key` —— relay API key(`sk-everyapi-…`). `everyapi use` 의 서브프로세스 env 에 사용합니다. `/api/token/*` 에서 가져와 여기에 캐시됩니다.
- `user_id` / `username` —— `auth status` 가 첫 API 왕복 전에 신원 줄을 렌더링할 수 있도록 캐시됩니다.

게이트웨이 리전은 `settings.json` 의 CLI 환경설정입니다: 설정돼 있지 않으면 대화형 로그인이 한 번 묻고 선택을 저장합니다. `everyapi settings set gateway_region cn` 은 공식 게이트웨이 트래픽을 `https://api-cn.everyapi.ai` 로 전환하고, `global` 은 `https://api.everyapi.ai` 를 씁니다. 셀프호스트용 커스텀 `--api-base` 는 여전히 우선합니다.

## 개발

CLI 소스 디렉터리(이 README, `go.mod`, `Makefile` 이 있는 곳)에서:

```bash
go test ./...
go run . auth status       # 프로덕션 대상
go run . auth login --api-base http://localhost:8787   # 로컬 백엔드 대상
```

모든 플랫폼 로컬 크로스 컴파일(CI 와 동일한 레시피):

```bash
make cli-release           # 산출물은 dist/(6 플랫폼 × 1 바이너리 = 6 파일)
```

## MCP server (`everyapi mcp` 서브커맨드)

`everyapi` 바이너리는 [Model Context Protocol](https://modelcontextprotocol.io) server 를 **내장**하고 있습니다 —— 서브커맨드로 노출됩니다(`everyapi mcp` 가 stdin 을 읽고 stdout 에 씁니다). AI 에이전트(Claude Code / Cursor / Codex CLI / 임의의 MCP 클라이언트)가 직접 호출할 수 있으며, **사용자가 터미널을 열 필요가 없습니다**.

> ⚠️ **MCP server 인증 모델 + 노출면**
>
> - **포트를 열지 않음**: `everyapi mcp` 는 순수 stdio JSON-RPC 이며 호스트 CLI 가 fork 합니다. **어떤 소켓 / TCP 포트도 listen 하지 않습니다** —— 네트워크 노출면이 없습니다.
> - **`~/.config/everyapi/credentials.json` 을 직접 읽음**: MCP server 는 자체 인증 흐름이 없어, 자격 증명 파일을 읽을 수 있다는 것 = 노출된 모든 도구를 당신으로서 호출할 수 있다는 뜻입니다. 당신의 사용자 권한으로 프로세스를 실행할 수 있는 MCP 호스트는 완전한 접근 권한을 갖습니다.
> - **money path `everyapi_seller_withdraw` 에는 friction step 이 있음**: 호출자는 `confirm: "yes"` 를 넘겨야 하며, 이는 AI 에이전트가 이체 동작을 UI 에서 사람에게 드러내도록 보장해 조용한 자금 유출을 막습니다. 다른 읽기 전용 도구(status / topup / seller_list)에는 이런 요구가 없습니다.
>
> 신뢰하지 않는 MCP 호스트는 설치하지 마세요.

### 설치

CLI 와 같은 바이너리입니다. CLI 를 설치하면 MCP server 도 함께 옵니다:

```bash
make cli                                              # 로컬 빌드, ./bin/everyapi 생성
# 또는 go install 로:
go install github.com/everyapi-ai/everyapi-ai/v3@latest
```

### Claude Code 연결

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

Cursor, Codex CLI, 다른 MCP 클라이언트 연결도 비슷합니다 —— `command` 를 `everyapi` 바이너리로 향하게 하고 `args: ["mcp"]` 를 지정하세요.

### 인증 선행 조건

터미널에서 최소 한 번은 `everyapi auth login` 을 실행해야 합니다 —— MCP server 는 터미널 상호작용 능력이 없는 백그라운드 프로세스라 device-code 흐름을 스스로 수행할 수 없습니다. `~/.config/everyapi/credentials.json` 을 직접 읽으며, 없으면 모든 도구가 `isError: true` 인 "not logged in" 메시지를 반환해 사용자를 로그인으로 안내합니다.

### 노출하는 도구(15개)

| Tool | 입력 | 용도 |
|---|---|---|
| `everyapi_status` | 없음 | 현재 잔액 / 사용량 / 요청 수 |
| `everyapi_topup` | 없음 | 웹 충전 URL 반환 |
| `everyapi_seller_list` | 없음 | marketplace 판매자 채널 목록 |
| `everyapi_seller_withdraw` | `{confirm: "yes", quota?: int}` | seller_quota 를 메인 잔액으로 이체; **`confirm: "yes"` 필수**(money-path friction) |
| `everyapi_seller_eligibility` | 없음 | 읽기 전용 마운트 게이트 체크리스트(marketplace 오픈 여부, 계정 활성, 이메일 인증, 계정 연령, 이전 사용 이력, 채널 상한). 사용자에게 key 를 요청하기 *전에* 호출하세요 |
| `everyapi_seller_add_key` | `{name, type, keys[], models, key_remarks?[], remark?}` | 평문 API key 로 판매자 채널을 마운트 —— `everyapi seller add-key` 의 쌍둥이. 대화에서 사용자가 직접 제공한 key 만 넘기세요 |
| `everyapi_seller_add_oauth_codex_start` | `{name, models}` | Codex / ChatGPT 기기 인가 흐름 시작, `user_code` + `verification_uri` + `flow_id` 반환 |
| `everyapi_seller_add_oauth_codex_poll` | `{flow_id}` | Codex 인가 상태 확인. `pending`/`slow_down` 은 계속 폴링, `authorized` 는 채널 id 반환, `expired`/`denied` 는 종료 |
| `everyapi_seller_add_oauth_claude_start` | `{name, models}` | Anthropic OAuth 흐름 시작, `authorize_url` 반환. 사용자가 브라우저 로그인 후 `<code>#<state>` 문자열을 받음 |
| `everyapi_seller_add_oauth_claude_complete` | `{input}` | 이전 단계에서 사용자가 붙여넣은 `<code>#<state>` 문자열을 제출해 채널 생성 |
| `everyapi_edge_list` | 없음 | BYO-GPU edge 노드 목록: id, 이름, 온라인 상태, 연결된 채널, 마지막 확인 시각, 설치된 모델 |
| `everyapi_edge_status` | `{node_id: int}` | 노드 하나의 상세 —— pause 여부, agent 버전, GPU 모델 / 개수 / VRAM, 설치된 모델 |
| `everyapi_edge_remove` | `{node_id: int, confirm: "yes"}` | 노드 삭제(마지막 노드였다면 연결된 채널도 함께). **`confirm: "yes"` 필수**(destructive-path friction) |
| `everyapi_admin_marketplace_status` | 없음 | 배포 전역 `marketplace.enabled` 플래그 조회. admin 역할 필요 |
| `everyapi_admin_marketplace_set` | `{enabled: bool, confirm: "yes"}` | 배포 전체의 marketplace 를 열거나 닫음. **`confirm: "yes"` 필수**. 닫아도 기존 노드와 채널은 계속 서비스합니다 |

**OAuth 도구 사용 패턴**(AI 에이전트가 대화에서 이렇게 진행합니다):

```
User: ChatGPT Plus 판매자 채널을 추가해줘. 이름은 my-chatgpt, models 는 gpt-4
AI    → everyapi_seller_add_oauth_codex_start({name: "my-chatgpt", models: "gpt-4"})
       ← "chatgpt.com/codex 에서 USR-789 를 입력하고 끝나면 알려주세요"
User: 브라우저에서 완료했어
AI    → everyapi_seller_add_oauth_codex_poll({flow_id: "..."})
       ← "status=pending, 몇 초만 더 기다리세요"
[authorized 될 때까지 폴링 계속]
       ← "status=authorized — channel #314 mounted"

User: Claude Pro 것도 추가해줘, my-claude / claude-3-opus
AI    → everyapi_seller_add_oauth_claude_start({...})
       ← "[URL] 에서 인가를 완료한 뒤 code#state 문자열을 알려주세요"
User: code-abc123#state-xyz
AI    → everyapi_seller_add_oauth_claude_complete({input: "code-abc123#state-xyz"})
       ← "Channel #315 mounted"
```

Gemini OAuth(loopback flow)는 **MCP 로 노출되지 않습니다** —— loopback listener 의 수명이 도구 호출을 넘나드는 수명주기와 맞지 않기 때문입니다. Gemini 는 여전히 CLI 의 `everyapi seller add-oauth gemini` 를 사용합니다.

### 수동 스모크 테스트

```bash
make cli
./bin/everyapi mcp <<'EOF'
{"jsonrpc":"2.0","id":1,"method":"initialize"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"everyapi_status","arguments":{}}}
EOF
```

JSON 응답 3줄이 보여야 합니다: initialize 결과, 15개 도구 목록, status 텍스트(또는 미로그인 isError).

## 이 바이너리에 아직 **포함되지 않은** 것

현재 **미구현**(중요도 순. 이후 릴리스에서 v1 표면을 깨지 않고 점진적으로 추가):

- ⚠️ OS 수준 코드 서명(macOS notarization / Windows Authenticode) —— 현재는 sigstore cosign keyless + SHA256SUMS 두 겹 검증에 의존합니다. 둘 다 모든 GitHub Release 에 동봉되며 Homebrew 가 자동으로 검증합니다
- ❌ 플랫폼 keychain 백엔드 —— 토큰은 여전히 디스크에 평문 저장(mode 0600)

여기 적혀 있었지만 **이미 출시된** 것(미구현으로 취급하지 마세요):

- ✅ 로컬 sanitizer proxy —— 명령은 `everyapi proxy {start,stop,status,configure}`(`everyapi start`/`everyapi configure` 가 아닙니다). 엔진 + 내장 detector 6개 + 커스텀 regex, `everyapi use` 에 통합됨
- ✅ 3개 프로바이더 전부의 판매자 OAuth onboarding(codex device / claude paste / gemini loopback)
- ✅ QR 로그인 메인 경로 —— `auth login` 은 device-code **+ QR 을 메인 경로로** 사용하고 `--no-qr` 이 폴백
- ✅ 안티피싱 계층 —— phrase 문자열(`everyapi wallet topup`), PKCE/state 엄격 검사, cert pinning 모두 반영됨. cert pinning 은 **report-only**(일치 시 무음 / 불일치 시 경고 / 연결 거부는 하지 않음)이며, 제품 결정은 "경고만, 강제하지 않음" 입니다

## 취약점 보고

[`SECURITY.md`](../SECURITY.md) 를 참고하세요.
