> 🌐 [English](../README.md) · [简体中文](README.zh-CN.md) · [日本語](README.ja.md) · [한국어](README.ko.md) · [Español](README.es.md) · **Deutsch** · [Français](README.fr.md)

# `everyapi` CLI

Buyer-Onboarding-CLI für das AI-API-Gateway [EveryAPI](https://everyapi.ai). Starte Claude Code, Codex, Antigravity, Grok Build, Qwen Code oder Kimi Code **in unter einer Minute**.

Status: **Kernabläufe stehen** — Buyer-Onboarding, Seller-Befehle (Plain-Key + OAuth über drei Provider), Sanitizer-Proxy, QR-Sign-In-Hauptpfad und Anti-Phishing-Schichten sind alle vorhanden. Die einzigen noch nicht implementierten Punkte sind OS-level Code-Signing und ein Plattform-Keychain-Backend (siehe „Was dieses Binary noch NICHT enthält" am Ende).

## Installation

```bash
brew tap everyapi-ai/tap && brew install everyapi
```

Spätere Upgrades — zuerst `brew update`:

```bash
brew update && brew upgrade everyapi
```

Ohne `brew update` verwendet `brew upgrade everyapi` das gecachte Formula und meldet "already installed", auch wenn eine neue Release existiert.

## Befehle

| Befehl | Funktion |
|---|---|
| `everyapi login` | Auf diesem Gerät bei EveryAPI anmelden |
| `everyapi logout` | Lokale Anmeldedaten löschen |
| `everyapi status` | Saldo, Nutzung, Quota anzeigen |
| `everyapi topup` | Topup-Seite öffnen (mit Anti-Phishing-Phrase-Prüfung) |
| `everyapi use <tool>` | Env setzen und in ein Drittanbieter-CLI execen (auf EveryAPI gerichtet) |
| `everyapi seller <sub>` | Marketplace-Seller-Befehle (list / withdraw / add-key / setup) |
| `everyapi edge <sub>` | Ein-Befehl-Deployment für den BYO-GPU-Supplier-Agent (register / start / status / logs / models / stop / update / remove) |
| `everyapi mcp` | Als MCP-Server laufen (stdin/stdout JSON-RPC) |
| `everyapi update` | Neue Versionen prüfen und den Upgrade-Befehl für deine Installationsmethode ausgeben |
| `everyapi version` | Build-Version anzeigen |
| `everyapi help` | Hilfe |

### `everyapi use <tool>` — exec in ein Drittanbieter-CLI (auf das EveryAPI-Gateway gerichtet)

Der Hauptgrund, dieses CLI zu installieren. Es konfiguriert und startet die unterstützten Coding-Clients über EveryAPI; der Eintrag `gemini` startet das bereits authentifizierte Antigravity-CLI.

```bash
everyapi use claude         # Claude Code → EveryAPI
everyapi use codex          # OpenAI Codex CLI → EveryAPI
everyapi use gemini         # Antigravity starten
everyapi use grok           # xAI Grok Build → EveryAPI
everyapi use qwen-code      # Alibaba Qwen Code → EveryAPI
everyapi use kimi-code      # Moonshot Kimi Code → EveryAPI
everyapi use                # ohne Argument → interaktive Auswahl über installierte Tools
```

Jedes Tool verwendet andere Env-Konventionen; das CLI merkt sie sich für dich:

| Tool | Gesetzte Umgebungsvariablen |
|---|---|
| claude | `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN` |
| codex | `OPENAI_BASE_URL`, `OPENAI_API_KEY` |
| gemini | nativer Antigravity-Launcher (`agy`) |
| grok | `XAI_API_KEY`, `GROK_MODELS_BASE_URL` |
| qwen-code | `OPENAI_API_KEY`, `OPENAI_BASE_URL`, `OPENAI_MODEL`; isoliertes `QWEN_HOME` |
| kimi-code | `KIMI_MODEL_API_KEY`, `KIMI_MODEL_BASE_URL`, `KIMI_MODEL_NAME`; isoliertes `KIMI_CODE_HOME` |

Kein Nachschlagen mehr, welchen Variablennamen jedes Tool liest, ob du `/v1` anhängen musst oder welche Auth-Header-Form gilt.

> ⚠️ **Sicherheitshinweis Subprozess-Env**: die Env-Variablen oben enthalten deinen Relay-API-Key. Drittanbieter-CLIs im Debug- / Verbose-Modus können Env loggen — bevor du `everyapi use` startest, stelle sicher, dass das aktivierte Debug-Flag keine `*_TOKEN` / `*_API_KEY` leakt. Bevor du Debug-Logs teilst, führe `sed -i 's/sk-everyapi-[A-Za-z0-9]*/REDACTED/g'` aus.

### `everyapi login` — Device Authorization Grant + QR-Sign-In

Verwendet Device Authorization Grant (RFC 8628-Style) + docs §7-5 Layer 1 „Gerät-zu-Gerät-QR-Sign-In":

1. Das CLI erstellt eine Session, **rendert einen Terminal-QR + druckt einen kurzen Code + URL**
2. Mit dem Handy scannen (oder die URL in einem Browser öffnen, in dem du bereits bei EveryAPI angemeldet bist) — die URL im QR enthält bereits `?code=USR-789`, das Dashboard füllt den Code automatisch aus, der User muss nur auf Approve klicken
3. Das CLI erhält das Access-Token und speichert es in `~/.config/everyapi/credentials.json` (mode 0600)

```bash
everyapi login                                    # Produktion; rendert standardmäßig QR + öffnet Browser
everyapi login --api-base http://localhost:8787   # lokales Dev / Self-Hosted
everyapi login --no-browser                       # Browser nicht automatisch öffnen (QR scannen)
everyapi login --no-qr                            # QR nicht rendern (Nicht-UTF-8-Terminals / Piping)
```

Beispiel-Terminal-QR-Rendering (Unicode-Halbblockzeichen; ca. 18-20 Zeilen hoch):

```
█▀▀▀▀▀█  ▀▀ ▄  █▀▀▀▀▀█
█ ███ █  ▀▄█▀  █ ███ █
█ ▀▀▀ █ ▄ ▀ █▀ █ ▀▀▀ █
▀▀▀▀▀▀▀ █▄█▄█▄ ▀▀▀▀▀▀▀
... (der tatsächliche QR encodiert verification_uri?code=USR-789)
```

Warum dies ein stärkerer Anti-Phishing-Pfad ist:

- Der User **gibt auf dem neuen Gerät kein Passwort ein** → keine Möglichkeit für eine Phishing-Site, Credentials zu erfassen
- Der User **wird nicht zu einer unbekannten Browser-Seite umgeleitet** → die Web-Redirect-Phishing-Fläche verschwindet
- Selbst wenn das CLI ein böswilliger Fork ist, der einen gefälschten QR erzeugt, ist die Bestätigungsseite nach dem Scannen das echte everyapi.ai-Dashboard (ausgelöst von einem Gerät, an dem der User bereits angemeldet ist), und einen unbekannten Code wird ein User nicht approven

Die übrigen Layer von docs §7-5 (Cert-Pinning / Phrase-String / PKCE-OAuth) wurden in unabhängigen PRs implementiert (Cert-Pinning ist report-only; Enforce war eine Produktentscheidung, es nicht auszuliefern).

### `everyapi seller <sub>` — Marketplace-Seller-Subbefehle

Bringt die Channel-Mount- und Withdrawal-Flows des Dashboards ins Terminal für scripted Onboarding. Vor dem Mounten eines Channels prüft `seller setup` die Eligibility (Account aktiv / E-Mail verifiziert / Account-Alter / Spend-Historie / Channel-Cap), und alle fehlschlagenden Gates werden **vor der Key-Eingabe** aufgelistet, damit du das nicht erst über ein 422 nach dem Submit erfährst.

```bash
everyapi seller list                          # gemountete Channels listen
everyapi seller withdraw                      # alle pending Seller-Einnahmen ins Hauptguthaben überweisen
everyapi seller withdraw --quota 1000         # Teilüberweisung (DB-Einheiten)
everyapi seller add-key   --type claude --name 'my-pro' \
                        --key 'sk-ant-...' --models 'claude-3-opus,claude-3-sonnet'
everyapi seller add-oauth codex --name 'my-chatgpt' --models 'gpt-4'
                                            # Ein-Klick-OAuth: CLI startet einen Device-Flow, User gibt im Browser
                                            # den user_code ein, Token landet automatisch im Channel
everyapi seller add-oauth claude --name 'my-claude' --models 'claude-3-opus,claude-3-sonnet'
                                            # Paste-Flow: CLI öffnet die Anthropic-Authorize-Seite; User klebt
                                            # den vom Callback angezeigten code#state zurück ins Terminal
everyapi seller add-oauth gemini --name 'my-gemini' --models 'gemini-1.5-pro'
                                            # echtes Ein-Klick-Loopback: CLI startet Random-Port-Listener,
                                            # Google sendet den Code direkt an das CLI — kein Einfügen
everyapi seller setup                         # interaktiver Wizard: prüft zuerst Eligibility, führt dann durch add-key
```

#### `add-key` — Multi-Key-Backup-Pool

`--key` darf wiederholt werden, um N äquivalente Credentials als Backup-Pool auf denselben Channel zu mounten (B2, PRODUCT §4.5); wenn der Primary-Key 401/403 zurückgibt, failt das Backend automatisch auf den nächsten over. `--key-remark` kann ebenfalls wiederholt werden, positionell auf `--key` ausgerichtet (das i-te `--key-remark` ist das Label des i-ten `--key`, zur späteren Identifikation im Dashboard). OAuth-Blobs können nicht in den Backup-Pool — sie bleiben Single-Key-Channels.

```
everyapi seller add-key   --type claude --name 'claude-pool' \
                        --models 'claude-3-opus' \
                        --key 'sk-ant-primary' --key-remark 'primary' \
                        --key 'sk-ant-backup'  --key-remark 'team backup'
```

`--type` von `add-key` akzeptiert Aliase (`openai` / `claude` / `gemini` / `codex` / `vertex` / `aws` / `xai` / `deepseek`) oder eine numerische ID. Mounting unterliegt der Marketplace-Eligibility (Account aktiv, E-Mail verifiziert, Spend-Historie, Channel-Cap), und das CLI listet die fehlschlagende Checkliste an allen drei Einstiegspunkten (`add-key` / `add-oauth` / `setup`), bevor es etwas anderes tut.

#### `add-oauth codex` — Ein-Klick-OAuth (Device-Flow)

`everyapi seller add-oauth codex --name 'my-chatgpt' --models 'gpt-4'` läuft den RFC 8628-ähnlichen Device-Authorization-Flow von Codex / ChatGPT — der Seller **berührt den Token-String nie**:

1. CLI ruft `/api/seller/codex/device/start` und erhält einen kurzen `user_code` und eine `verification_uri`
2. CLI öffnet den Browser standardmäßig auf `https://auth.openai.com/codex/device` (mit `--no-browser` überspringen); der User gibt den `user_code` im Browser ein, um die Autorisierung abzuschließen
3. CLI pollt `/api/seller/codex/device/poll`; nach Autorisierung erstellt das Backend automatisch den Channel und schreibt das OAuth-Token in das Feld `key` des Channels
4. Ausgabe: Channel-ID + die gebundene ChatGPT-Email

Autorisierungs-Cookies werden von einem prozessinternen `http.CookieJar` verwaltet (nicht persistiert) — Device-Flow-State ist kurzlebig und prozessgebunden, passend zum Threat-Model.

#### `add-oauth claude` — Paste-and-Submit-OAuth

`everyapi seller add-oauth claude --name … --models …`. Der Anthropic-OAuth-Provider hat `redirect_uri` auf seiner Seite hart auf `https://console.anthropic.com/oauth/code/callback` codiert, daher kann das CLI keinen Localhost-Listener für den Callback nutzen. Flow:

1. CLI ruft `/api/seller/claude/oauth/start`; Backend erstellt das PKCE-Paar + State und gibt die Authorize-URL von Anthropic zurück
2. CLI öffnet standardmäßig den Browser (mit `--no-browser` überspringen); der User meldet sich bei Anthropic an und approved
3. Anthropic leitet auf seine Callback-Seite weiter, die einen `<code>#<state>`-String anzeigt
4. **Der User kopiert diesen String zurück ins CLI**
5. CLI ruft `/api/seller/claude/oauth/complete`; Backend tauscht Code+Verifier gegen das Token und mintet den Channel

Ein zusätzlicher Paste-Schritt gegenüber dem Device-Flow, aber immer noch viel einfacher, als `~/.claude/auth.json` von Hand zu finden. Der Session-Cookie wird beim Start vom Backend ausgegeben; complete muss dieselbe Session treffen — der `http.CookieJar` des CLI ist prozessintern und pro Invocation isoliert.

#### `add-oauth gemini` — echtes Ein-Klick-Loopback-OAuth

`everyapi seller add-oauth gemini --name … --models … [--no-browser] [--timeout 5m]`. Googles gemini-cli Installed-App-OAuth-Client akzeptiert `http://127.0.0.1:<port>/callback` als `redirect_uri`, sodass **das CLI seinen eigenen Listener für den Callback betreibt** — der User meldet sich per Browser an und muss nichts einfügen. Flow:

1. CLI startet einen One-Shot-HTTP-Listener auf einem zufälligen ephemeren Port (`127.0.0.1:0`), fester Pfad `/callback`
2. CLI ruft `/api/seller/gemini/oauth/start` mit `redirect_uri = http://127.0.0.1:<port>/callback`; Backend validiert den Redirect strikt: Loopback / Port ≥ 1024 / scheme=http / path=/callback / kein query/fragment/userinfo (verhindert SSRF + Redirect-Hijacking)
3. CLI öffnet standardmäßig den Browser; der User meldet sich bei Google an und stimmt zu
4. Google leitet mit `?code=…&state=…` auf den Listener des CLI weiter
5. CLI verifiziert, dass der State übereinstimmt (verhindert Stale-Flows / Forgery) und ruft `/api/seller/gemini/oauth/complete`
6. Backend tauscht Code + denselben redirect_uri gegen das Token und mintet den Channel

Vergleich mit den anderen beiden Providern:

| Provider | UX | Grund |
|---|---|---|
| `codex` | User tippt im Browser einen 6-stelligen user_code; CLI pollt automatisch | OpenAI-Device-Flow, kein redirect_uri |
| `claude` | User meldet sich per Browser an, kopiert `code#state` zurück ins CLI | Anthropic codiert redirect_uri hart auf seine eigene Callback-URL |
| `gemini` | User meldet sich per Browser an, schließt den Tab, fertig | Google akzeptiert Loopback-Redirects |

`--timeout` begrenzt das Warten (Standard 5 Minuten). Bei Timeout beendet das CLI und schließt den Listener sauber.

### `everyapi edge <sub>` — Ein-Befehl-BYO-GPU-Supplier-Agent-Deploy

Idle-GPUs an EveryAPI anschließen, um Compute zu verkaufen. Das CLI verdichtet den Deploy auf 8 Subbefehle und erspart Suppliern das Hand-Kopieren von docker-compose, das Ausfüllen von `.env` oder das Herumreichen des Registration-Tokens:

```bash
everyapi login                              # verwendet vorhandenes Login wieder
everyapi edge register --name "rtx-4090"    # ruft /api/seller/edge/nodes für node_id + token, schreibt nach ~/.local/share/everyapi/edge/<id>/
everyapi edge start                         # auto-erkennt NVIDIA / ROCm / Apple Silicon / CPU, docker compose up -d
everyapi edge models pull llama3.1:8b       # docker compose exec ollama ollama pull ...
everyapi edge status                        # lokales docker compose ps + Dashboard online/offline
everyapi edge logs -f                       # Logs verfolgen
everyapi edge update                        # docker compose pull && up -d
everyapi edge remove                        # down -v + lokales dir löschen + Backend-DELETE
```

`start` rendert `docker-compose.yml` zur Laufzeit per `text/template` (**nicht aus eingebettetem statischem YAML**) — so können Container-Namen per node_id genamespacet werden, sodass mehrere Nodes auf einem Host nicht kollidieren, und GPU-Passthrough wird per Mode bedingt gerendert (NVIDIA = `deploy.resources.devices` + nvidia-Driver; ROCm = `/dev/kfd` + `/dev/dri` + `group_add: video`; macOS = kein ollama-Container, der Agent verbindet sich per `host.docker.internal` mit dem nativen ollama des Hosts).

Credential-Flow: CLI verwendet einen vorhandenen `sk-everyapi-`-Bearer, um `POST /api/seller/edge/nodes` zu rufen → Backend gibt das `registration_token` einmalig zurück (danach speichert das Backend nur den sha256, zeigt es nie wieder an) → CLI schreibt es 0600 nach `~/.local/share/everyapi/edge/<id>/node.json` → rendert es in die Compose-Env `EVERYAPI_REGISTRATION_TOKEN`. **Das Registration-Token wird in keine .env-Datei geschrieben** (damit Supplier es nicht versehentlich committen).

Voraussetzungen: `docker` + `docker compose v2` (v1 ist EOL und wird nicht unterstützt). Auf macOS `brew install ollama && brew services start ollama` (Metal-Beschleunigung läuft nicht in einem Docker-Container).

### `everyapi topup` — Topup-Redirect mit Anti-Phishing-Phrase

`everyapi topup` öffnet die Topup-Seite des Dashboards. Vor dem Redirect durchläuft es eine docs §7-5 Layer-3-Verifizierung:

1. CLI ruft Backend `POST /api/cli/jump-session` und erhält eine Session-ID + einen 4-Emoji-Phrase-String (z. B. `🌊 🦊 🍕 🚀`)
2. CLI druckt URL und Phrase beide ins Terminal und sagt dem User „dieselbe Phrase sollte gleich oben auf der Seite erscheinen"
3. User drückt Enter; das CLI öffnet die URL im System-Browser (mit `?jump_session=<id>`)
4. Beim Laden des Dashboards ruft es Backend `GET /api/cli/jump-session/:id/phrase`, erhält dieselbe Phrase und **zeigt sie prominent im Page-Header**
5. Der User vergleicht visuell: Phrase stimmt → echtes EveryAPI; stimmt nicht oder wird nicht angezeigt → Tab schließen, möglich Phishing

Warum das Phishing blockt: die Phrase lebt im Speicher des Backends, keyed by einer zufälligen 32-Hex-Session-ID; eine Phishing-Site hat keinen Auth-Pfad, um sie zu fetchen, und eine gefälschte `wallet/topup?jump_session=<id>` kann die Phrase auch nicht lesen. Kurze TTL (10 min) + Single-Use (die Session wird gelöscht, nachdem das Dashboard sie einmal fetcht) begrenzen das Reuse-Risiko weiter.

```bash
everyapi topup                    # öffnet standardmäßig den Browser
everyapi topup --no-browser       # nur URL ausgeben, manuell kopieren
```

### `everyapi status` — aktuelles Saldo / Nutzung / Quota

```
$ everyapi status

  alice (alice@example.com)
  quota:     $12.34 remaining   $5.67 used
  requests:  1,234
  topup:     https://app.everyapi.ai/wallet
```

### `everyapi update` — führt automatisch den brew-Upgrade-Befehl aus

Prüft die neueste Release auf dem GitHub-Mirror, vergleicht mit der aktuellen Version und **führt automatisch `brew update && brew upgrade everyapi` aus** — ein Befehl, kein Copy-Paste.

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

Warum nicht das Binary direkt austauschen? Homebrews eigene Verifizierungskette (SHA / Bottle Signing) ist stärker als alles, was wir innerhalb der CLI nachbauen würden, und das Selbst-Ersetzen eines laufenden Executable ist auf der Windows-Plattform ein Minenfeld.

Flags:
- `--check` — stiller Vergleich; exit 0 wenn aktuell, exit 1 wenn veraltet. Für CI / cron:
  ```bash
  everyapi update --check || echo "needs upgrade"
  ```
- `--dry-run` — gibt den auszuführenden Befehl aus, ohne ihn tatsächlich auszuführen (zur Inspektion)

### `everyapi settings` — CLI-Einstellungen (Sprache usw.)

Das CLI kommt mit i18n in 7 Sprachen: Englisch, Vereinfachtes Chinesisch, Japanisch, Koreanisch, Spanisch, Deutsch, Französisch — CLI-Strings werden in der gewählten Sprache gerendert. Backend-API-Fehler werden über den `Accept-Language`-Header auto-verhandelt und decken 8 Sprachen ab — die genannten 7 plus Traditionelles Chinesisch.

```bash
$ everyapi settings                          # interaktiver Picker: Sprache wählen
$ everyapi settings list                     # aktuelle Einstellungen anzeigen
$ everyapi settings set language zh          # direkt setzen
$ everyapi settings set language fr          # Französisch genauso
$ everyapi settings reset                    # auf Default zurücksetzen (en + LANG auto-erkennen)
```

**Auto-Erkennung**: wenn du nichts explizit gesetzt hast, liest das CLI beim Start die Env-Vars in der Reihenfolge `EVERYAPI_LANG > LC_ALL > LC_MESSAGES > LANG`. Ein System-Locale `zh_CN.UTF-8` / `ja_JP.UTF-8` / `fr_FR.UTF-8` etc. greift sofort — Null Konfiguration.

**Einmaliger Override**:

```bash
EVERYAPI_LANG=zh everyapi status             # diese eine Invocation zeigt auf Chinesisch; nicht persistiert
```

**Übersetzungsbeispiel** (Not-Logged-In-Fehler, 7 Sprachen × dieselbe Zeile):

```
en : Error: not logged in — run 'everyapi login' first
zh : 错误: 未登录 — 先运行 'everyapi login'
ja : エラー: ログインしていません — まず 'everyapi login' を実行してください
ko : 오류: 로그인되어 있지 않습니다 — 먼저 'everyapi login' 을 실행하세요
es : Error: no has iniciado sesión — ejecuta primero 'everyapi login'
de : Fehler: nicht angemeldet — führe zuerst 'everyapi login' aus
fr : Erreur: non connecté — exécutez d'abord 'everyapi login'
```

Die Settings leben in `~/.config/everyapi/settings.json` (selbes Verzeichnis wie `credentials.json`, aber Mode `0644` — keine Secrets).

**Um Übersetzungen zu verbessern oder eine Sprache hinzuzufügen**: siehe [`internal/i18n/locales/README.md`](internal/i18n/locales/README.md).

## Konfigurationsdateien

Credentials leben in `~/.config/everyapi/credentials.json` (oder `$XDG_CONFIG_HOME/everyapi/` falls `$XDG_CONFIG_HOME` gesetzt ist), File-Mode `0600`. Geschrieben von `everyapi login`, gelesen von jedem anderen Befehl.

> ⚠️ **Tokens werden im Klartext gespeichert**. File-Mode `0600` + privater `$HOME`-Pfad entspricht der Konvention von Industrie-CLIs wie `gh auth` / `aws configure`, aber **für Heim-Maschinen-Diebstahl- / Malware-Threat-Modelle** kann jeder Prozess, der diese Datei lesen kann, die EveryAPI-API als du aufrufen (inklusive der MCP-Tools — siehe §money-path Friction-Step unten). Empfohlen:
> - Nicht auf gemeinsam genutzten / öffentlichen Maschinen `everyapi login` machen
> - macOS-User: vor Aktivierung von FileVault `everyapi logout` in Betracht ziehen
> - Linux-User: Home-Dir-Verschlüsselung aktivieren (`ecryptfs` / LUKS)
> - Wenn Leak vermutet → `everyapi logout` löscht lokale Credentials sofort, und API-Key über das EveryAPI-Dashboard rotieren
>
> Ein Plattform-Keychain-Backend (macOS Keychain / Windows DPAPI / Linux Secret Service) ist geplant, aber nicht ausgeliefert.

Felder:

- `api_base` — die EveryAPI-Gateway-URL. Standard `https://api.everyapi.ai`. Self-Hosted-User / lokale Entwicklung können beim `login` mit `--api-base` überschreiben.
- `access_token` — Bearer, der von jedem authentifizierten API-Call verwendet wird.
- `relay_key` — Relay-API-Key (`sk-everyapi-…`), verwendet für die Subprozess-Env von `everyapi use`. Von `/api/token/*` gefetcht und hier gecacht.
- `user_id` / `username` — gecacht, damit `status` die Identity-Zeile vor seinem ersten API-Roundtrip rendern kann.

## Entwicklung

Im CLI-Source-Verzeichnis (dem mit dieser README, `go.mod` und `Makefile`):

```bash
go test ./...
go run . status            # gegen Produktion
go run . login --api-base http://localhost:8787   # gegen lokales Backend
```

Lokales Cross-Compile für alle Plattformen (gleiche Rezeptur wie CI):

```bash
make cli-release           # Artefakte in dist/ (5 Plattformen × 1 Binary = 5 Dateien)
```

## MCP-Server (`everyapi mcp`-Subbefehl)

Das `everyapi`-Binary **enthält einen eingebauten** [Model Context Protocol](https://modelcontextprotocol.io) Server — als Subbefehl ausgesetzt (`everyapi mcp` liest stdin und schreibt stdout). AI-Agents (Claude Code / Cursor / Codex CLI / beliebiger MCP-Client) können ihn direkt invoken, **ohne dass der User ein Terminal öffnen muss**.

> ⚠️ **MCP-Server-Auth-Model + Expositionsfläche**
>
> - **Keine offenen Ports**: `everyapi mcp` ist reines stdio-JSON-RPC, vom Host-CLI geforkt. **Lauscht auf keinem Socket / TCP-Port** — keine Netzwerk-Oberfläche.
> - **Liest `~/.config/everyapi/credentials.json` direkt**: der MCP-Server hat keinen eigenen Auth-Flow; Lesefähigkeit der Credentials-Datei = Fähigkeit, jedes ausgesetzte Tool als du aufzurufen. Jeder MCP-Host, der einen Prozess als dein User laufen lassen kann, hat vollen Zugriff.
> - **Money-Path `everyapi_seller_withdraw` hat einen Friction-Step**: Aufrufer müssen `confirm: "yes"` übergeben, was sicherstellt, dass der AI-Agent die Transfer-Aktion in der UI einem Menschen vorlegt und Silent-Drain vermeidet. Andere Read-Only-Tools (status / topup / seller_list) haben diese Anforderung nicht.
>
> Installiere keine MCP-Hosts, denen du nicht vertraust.

### Installation

Gleiches Binary wie das CLI — die Installation des CLI liefert dir den MCP-Server:

```bash
make cli                                              # lokaler Build, produziert ./bin/everyapi
# oder via go install:
go install github.com/everyapi-ai/everyapi-ai@latest
```

### Verdrahtung in Claude Code

Zu `~/.claude/settings.json` hinzufügen:

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

Verdrahtung in Cursor, Codex CLI oder andere MCP-Clients ist ähnlich — `command` auf das `everyapi`-Binary mit `args: ["mcp"]` zeigen lassen.

### Auth-Voraussetzung

Du musst `everyapi login` mindestens einmal in einem Terminal ausgeführt haben — der MCP-Server ist ein Hintergrundprozess ohne Terminal-Interaktionsfähigkeit, kann den Device-Code-Flow also nicht selbst ausführen. Er liest `~/.config/everyapi/credentials.json` direkt; wenn fehlt, gibt jedes Tool eine `isError: true` „not logged in"-Nachricht zurück, die den User zum Login führt.

### In v1 ausgesetzte Tools (8 insgesamt)

| Tool | Eingabe | Funktion |
|---|---|---|
| `everyapi_status` | keine | Aktuelles Saldo / Verbraucht / Request-Anzahl |
| `everyapi_topup` | keine | Gibt die Web-Topup-URL zurück |
| `everyapi_seller_list` | keine | Listet Marketplace-Seller-Channels |
| `everyapi_seller_withdraw` | `{confirm: "yes", quota?: int}` | Überträgt seller_quota ins Hauptguthaben; **`confirm: "yes"` erforderlich** (Money-Path-Friction) |
| `everyapi_seller_add_oauth_codex_start` | `{name, models}` | Startet den Codex / ChatGPT Device-Authorization-Flow; gibt `user_code` + `verification_uri` + `flow_id` zurück |
| `everyapi_seller_add_oauth_codex_poll` | `{flow_id}` | Prüft Codex-Autorisierungsstatus. `pending`/`slow_down` weiter pollen; `authorized` gibt die Channel-ID zurück; `expired`/`denied` terminieren |
| `everyapi_seller_add_oauth_claude_start` | `{name, models}` | Startet den Anthropic-OAuth-Flow; gibt `authorize_url` zurück. Nachdem der User sich per Browser angemeldet hat, erhält er einen `<code>#<state>`-String |
| `everyapi_seller_add_oauth_claude_complete` | `{input}` | Übergibt den `<code>#<state>`-String, den der User im vorherigen Schritt eingefügt hat; mintet den Channel |

**OAuth-Tool-Nutzungsmuster** (wie ein AI-Agent das in einer Conversation durchläuft):

```
User: Füge mir einen ChatGPT Plus Seller-Channel hinzu, nenn ihn my-chatgpt, Models gpt-4
AI    → everyapi_seller_add_oauth_codex_start({name: "my-chatgpt", models: "gpt-4"})
       ← „Geh zu chatgpt.com/codex, gib USR-789 ein, dann sag mir Bescheid"
User: Im Browser fertig
AI    → everyapi_seller_add_oauth_codex_poll({flow_id: "..."})
       ← „status=pending, warte noch ein paar Sekunden"
[weiter pollen bis authorized]
       ← „status=authorized — channel #314 mounted"

User: Füge den Claude Pro auch hinzu, my-claude / claude-3-opus
AI    → everyapi_seller_add_oauth_claude_start({...})
       ← „Geh zu [URL], um die Autorisierung abzuschließen, dann gib mir den code#state-String"
User: code-abc123#state-xyz
AI    → everyapi_seller_add_oauth_claude_complete({input: "code-abc123#state-xyz"})
       ← „Channel #315 mounted"
```

Gemini-OAuth (Loopback-Flow) ist **nicht über MCP ausgesetzt** — die Lebensdauer des Loopback-Listeners passt nicht zur Cross-Tool-Call-Lebensdauer. Gemini geht weiterhin über das CLI `everyapi seller add-oauth gemini`.

### Manueller Smoke-Test

```bash
make cli
./bin/everyapi mcp <<'EOF'
{"jsonrpc":"2.0","id":1,"method":"initialize"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"everyapi_status","arguments":{}}}
EOF
```

Du solltest drei JSON-Antwortzeilen sehen: initialize-Ergebnis, Liste von 4 Tools, Status-Text (oder ein Not-Logged-In-isError).

## Was dieses Binary noch NICHT enthält

Immer noch **nicht implementiert** (nach Wichtigkeit sortiert; spätere Releases ergänzen schrittweise, ohne v1-Surface zu brechen):

- ⚠️ OS-level Code-Signing (macOS-Notarization / Windows-Authenticode) — vorerst verlassen wir uns auf die sigstore-cosign-keyless + SHA256SUMS-Doppelschichtverifikation; beide werden mit jeder GitHub-Release ausgeliefert und Homebrew prüft sie beim Installieren automatisch
- ❌ Plattform-Keychain-Backend — Tokens immer noch im Klartext auf der Disk (Mode 0600)

Früher hier gelistet, aber **jetzt ausgeliefert** (nicht mehr als unimplementiert behandeln):

- ✅ Lokaler Sanitizer-Proxy — der Befehl ist `everyapi proxy {start,stop,status,configure}` (nicht `everyapi start`/`everyapi configure`); Engine + 6 eingebaute Detektoren + Custom-Regex + integriert in `everyapi use`
- ✅ Seller-OAuth-Onboarding bei allen drei Providern (codex device / claude paste / gemini loopback)
- ✅ QR-Sign-In-Hauptpfad — `login` verwendet Device-Code **+ QR als Hauptpfad**, mit `--no-qr` als Fallback
- ✅ Anti-Phishing-Schichten — Phrase-String (`everyapi topup`), PKCE/State-Strict-Check und Cert-Pinning sind alle vorhanden; Cert-Pinning ist **report-only** (still bei Match / Alarm bei Mismatch / lehnt Verbindung nie ab), mit der Produktentscheidung „nur Alarm, nicht enforcen"

## Vulnerabilities melden

Siehe [`SECURITY.md`](../SECURITY.md).
