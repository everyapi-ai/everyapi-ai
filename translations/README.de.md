> 🌐 [English](../README.md) · [简体中文](README.zh-CN.md) · [日本語](README.ja.md) · [한국어](README.ko.md) · [Español](README.es.md) · **Deutsch** · [Français](README.fr.md)

# `everyapi` CLI

Buyer-Onboarding-CLI für das KI-API-Gateway [EveryAPI](https://everyapi.ai). Bringt jeden unterstützten Coding-Agenten **in unter einer Minute** über eine einzige geprüfte Registry zum Laufen.

Status: **Kernabläufe ausgeliefert** —— Buyer-Onboarding, Seller-Befehle (Plain Key + OAuth für drei Anbieter), Sanitizer-Proxy, QR-Anmeldung als Hauptpfad und Anti-Phishing-Schichten sind vorhanden. Es fehlen nur noch Code-Signierung auf Betriebssystemebene und Plattform-Keychain-Backends (siehe „Was diese Binary noch NICHT enthält" am Ende).

## Installation

**macOS (Homebrew):**

```bash
brew tap everyapi-ai/tap && brew install everyapi
```

Führe für spätere Upgrades zuerst `brew update` aus (sonst verwendet `brew upgrade everyapi` die zwischengespeicherte Formula und meldet „already installed", obwohl ein neues Release existiert):

```bash
brew update && brew upgrade everyapi
```

**Linux / macOS (Installationsskript):**

```bash
curl -fsSL https://dl.everyapi.ai/install.sh | bash
```

Das Skript erkennt Betriebssystem und Architektur, lädt das passende `everyapi_{os}_{arch}.tar.gz` herunter, prüft dessen SHA256 und installiert es nach `~/.local/bin` (bzw. `/usr/local/bin`, wenn du es als root ausführst). Ist [cosign](https://github.com/sigstore/cosign) installiert, wird zusätzlich die Keyless-Signatur geprüft —— mit `--require-signature` wird dieser Schritt verpflichtend (empfohlen für CI oder lieferkettensensible Umgebungen).

Ein Einzeiler weltweit: Das Skript wählt die Downloadquelle zur Laufzeit —— GitHub Releases, wenn erreichbar, ein Spiegel in Festlandchina, wenn GitHub langsam oder blockiert ist —— derselbe Befehl funktioniert also innerhalb und außerhalb Chinas. Setze `EVERYAPI_DOWNLOAD_BASE`, um einen bestimmten Spiegel zu erzwingen.

Gängige Flags:

```bash
curl -fsSL https://dl.everyapi.ai/install.sh | bash -s -- --version v0.2.2     # Version festnageln
curl -fsSL https://dl.everyapi.ai/install.sh | bash -s -- --prefix /usr/local  # Präfix wählen
curl -fsSL https://dl.everyapi.ai/install.sh | bash -s -- --require-signature  # abbrechen, wenn cosign fehlschlägt
curl -fsSL https://dl.everyapi.ai/install.sh | bash -s -- --force              # dieselbe Version neu installieren
```

Für ein späteres Upgrade führst du denselben Befehl erneut aus: Das Skript löst den neuesten Release-Tag auf und ersetzt die Binary an Ort und Stelle, wenn eine neuere existiert. Bist du bereits auf der Zielversion, endet es mit `already at vX.Y.Z — nothing to do`, es ist also unbedenklich in Provisioning-Skripten oder Dotfiles. Mit `--force` wird darüber neu installiert (nützlich zur Integritätsprüfung oder um eine beschädigte Datei zu reparieren). Das Skript selbst liegt in diesem Repository unter [`install.sh`](../install.sh), falls du es lieber erst herunterlädst und liest.

**Go-Nutzer (`go install`):**

```bash
go install github.com/everyapi-ai/everyapi-ai/v3@latest
```

**Windows (PowerShell):**

```powershell
irm https://dl.everyapi.ai/install.ps1 | iex
```

Gleicher Ablauf wie beim Shell-Skript: neuesten Tag auflösen, `everyapi_windows_amd64.zip` + `SHA256SUMS` laden, Hash prüfen (und die Signatur, wenn cosign im `PATH` liegt), `everyapi.exe` nach `%LOCALAPPDATA%\everyapi\bin` installieren und zum Benutzer-`PATH` hinzufügen. Um eine Version festzunageln oder andere Optionen zu übergeben, materialisiere das Skript zuerst: `& ([scriptblock]::Create((irm https://dl.everyapi.ai/install.ps1))) -Version v0.2.2`. Auch dieses Skript liegt im Repository unter [`install.ps1`](../install.ps1).

**Windows (manuell):** Lade `everyapi_windows_amd64.zip` (oder ein anderes Artefakt) von der [Releases-Seite](https://github.com/everyapi-ai/everyapi-ai/releases/latest), prüfe es gegen `SHA256SUMS` und lege die Binary in deinen `%PATH%`.

## Befehle

`everyapi` ohne Argumente in einem TTY öffnet einen interaktiven Launcher über genau diese Menge; `everyapi help` gibt sie als Text aus.

| Befehl | Wofür |
|---|---|
| `everyapi auth <sub>` | An- und abmelden sowie den Sitzungsstatus anzeigen (`login` / `logout` / `status`) |
| `everyapi wallet <sub>` | Aufladen (mit Anti-Phishing-Phrasenprüfung), Zahlungsverlauf, Zahlungsmethoden |
| `everyapi checkin <sub>` | Das heutige Tageskontingent abholen; den Kalender dieses Monats anzeigen |
| `everyapi account <sub>` | Profil, 2FA, Affiliate-Code, Abo-Tarife |
| `everyapi use <tool>` | Umgebung setzen und ein Drittanbieter-CLI gegen EveryAPI starten |
| `everyapi token <sub>` | Relay-API-Keys verwalten (list / create / key / revoke / switch / …) |
| `everyapi models <sub>` | Modellkatalog: list / pricing / groups |
| `everyapi stats <sub>` | Verbrauch, Request-Log, Performance pro Modell, Upstream-Gesundheit |
| `everyapi market <sub>` | Nachfrage-Posts, Streitfälle, Missbrauchsmeldungen |
| `everyapi inbox <sub>` | In-App-Benachrichtigungen und Direktnachrichten |
| `everyapi seller <sub>` | Marketplace-Verkäuferbefehle (list / setup / withdraw / add-key / add-oauth) |
| `everyapi edge <sub>` | Ein-Befehl-Deployment des BYO-GPU-Supplier-Agents (register / start / status / logs / models / rename / pause / resume / stop / update / remove) |
| `everyapi artifacts <sub>` | Eigenständige HTML-Reports veröffentlichen und verwalten (`share` / `update` / `delete`) |
| `everyapi events` | Den Live-Event-Stream abonnieren (SSE) |
| `everyapi feedback` | Einen Fehlerbericht oder Feature-Wunsch ans Team schicken |
| `everyapi proxy <sub>` | Lokaler Sanitizer-Proxy (`start` / `stop` / `status` / `configure`) |
| `everyapi computer <sub>` | Lokale macOS-App-Fenster über die Bedienungshilfen lesen und steuern |
| `everyapi mcp` | Als MCP-Server laufen (JSON-RPC über stdin/stdout) |
| `everyapi doctor` | Selbstprüfung: Zugangsdaten, Gateway, Sanitizer, installierte Tools |
| `everyapi settings <sub>` | CLI-Einstellungen ansehen und ändern (Sprache, Terminal-Modus) |
| `everyapi admin` | Operator-Konsole —— nur für Admin-Konten sichtbar |
| `everyapi version [update\|uninstall]` | Build-Version; auf ein Upgrade prüfen und es ausführen; das CLI entfernen |
| `everyapi help` | Die vollständige Befehlsliste ausgeben |

### `everyapi computer <sub>` —— lokale macOS-Computer-Use

Unter macOS kann das CLI laufende Apps und Fenster ermitteln, einen begrenzten Schnappschuss der Bedienungshilfen zurückgeben und semantische oder koordinatenbasierte Aktionen ausführen. Diese Oberfläche ist rein lokal und wird nicht in `everyapi mcp` registriert. Linux- und Windows-Builds geben explizit `unsupported_platform` zurück.

Unter macOS steuert `everyapi computer` über einen lokalen Unix-Socket eine kleine, eigenständig codesignierte Helfer-App (`EveryAPI Computer Use.app`, gebaut aus `clients/desktop/native/computer-use-macos`) und lädt sie beim ersten Einsatz automatisch herunter und startet sie, falls sie noch nicht installiert ist — auch dann, wenn EveryAPI Connect bereits seine eigene mitgelieferte Kopie installiert hat, die dieses CLI wiederverwendet, statt eine zweite herunterzuladen. Der Helfer meldet Screenshot-Unterstützung als false, weil macOS über diesen Provider keine verlässliche öffentliche Kennung für fensterbezogene Aufnahmen bereitstellt; er ersetzt sie nie durch eine Bildschirmbereichsaufnahme, die eine überlappende App enthalten könnte.

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

Führe `everyapi computer permissions --json` aus und erteile **EveryAPI Computer Use** die Bedienungshilfen-Berechtigung — nicht `everyapi`, `osascript` oder deinem Terminal — unter Systemeinstellungen > Datenschutz & Sicherheit > Bedienungshilfen. Da der Helfer eine eigene signierte App mit eigener Bundle-Identität ist, bleibt diese Freigabe auf diese eine Fähigkeit beschränkt: Sie autorisiert nicht zusätzlich jedes AppleScript oder JXA-Skript auf dem Rechner und übersteht Updates von CLI und Helfer. `permissions` meldet die Bedienungshilfen direkt und die Automation als `unknown`, da dieser Provider nicht von System Events abhängt und keine separate Automation-Vorprüfung durchführt.

Element-Indizes stammen aus dem letzten `get-app-state`-Schnappschuss und verfallen nach zwei Minuten. Fenster werden über den Index (`--window-index`) ausgewählt, intern aber über die echte fensterbezogene ID identifiziert, die CoreGraphics auf dem Bildschirm vergibt, sofern vorhanden — für minimierte Fenster wird auf eine schnappschussbezogene synthetische ID zurückgegriffen. In beiden Fällen erkennt der Provider beobachtbare Änderungen über einen internen Fingerabdruck, doch öffentliche Attribute der Bedienungshilfen können nicht beweisen, dass ein ersetztes Fenster oder Steuerelement mit identischen Attributen dieselbe native Instanz ist. Der Cache speichert unter `~/.config/everyapi/computer-use/state/` mit privaten Rechten ausschließlich undurchsichtige Daten zu Anwendung, Prozess, Fenster, Pfad, Role, Frame, Aktionsname und Fingerabdruck. Hole nach `app_stale`, `element_stale` oder `window_stale` einen neuen Schnappschuss. Eine erfolgreiche GUI-Aktion bleibt erfolgreich, wenn ihre Best-Effort-Statusaktualisierung fehlschlägt; das JSON enthält dann `refreshError`, statt einen wiederholbaren Aktionsfehler zurückzugeben. Wird der Helfer-Aufruf unterbrochen oder liefert er nach der Übergabe einer Aktion eine ungültige Quittung, bedeutet `action_outcome_unknown`, dass die Aktion bereits stattgefunden haben kann; aktualisiere den Status, bevor du über einen erneuten Versuch entscheidest.

Eine gepflegte Liste bekannter Terminal-Apps, Passwortmanager, der Schlüsselbundverwaltung, der Passwörter-App, der Systemeinstellungen und von EveryAPI Connect wird als Defense-in-Depth-Hürde blockiert. Das Blockieren nach Bundle-ID ist kein vollständiger Anwendungsklassifikator: nicht gelistete Apps, Editoren mit integriertem Terminal, Browser sowie umbenannte oder neu veröffentlichte Apps können gleichwertige Fähigkeiten bieten. Die eigentliche Vertrauensgrenze bleiben das explizite `--app`-Ziel, macOS TCC und die Rechte des aufrufenden Nutzers. Beobachteter Text wird vor der Ausgabe von Terminal-Steuersequenzen befreit und auf Zugangsdaten geprüft; eingegebener oder gesetzter Text, der auf die eingebauten Secret-Detektoren passt, wird abgelehnt. Bevorzuge `--text-stdin` und `--value-stdin`, damit gewöhnlicher Text nicht in der Shell-History landet.

### `everyapi use <tool>` —— ein Drittanbieter-CLI gegen das EveryAPI-Gateway starten

Das ist der Hauptgrund, dieses CLI zu installieren: einen unterstützten Coding-Client über EveryAPI konfigurieren und starten. Native Integrationen (`antigravity`, `librefang`) behalten ihren eigenen Auth-Pfad und erhalten keinen kopierten Relay-Key.

```bash
everyapi use claude            # Claude Code → EveryAPI
everyapi use codex             # OpenAI Codex CLI → EveryAPI
everyapi use opencode          # OpenCode → prozessbezogener EveryAPI-Provider
everyapi use gemini            # Google Gemini CLI → EveryAPI
everyapi use antigravity       # Antigravity (native Google-Auth und -Routing)
everyapi use aider             # Aider → EveryAPI (mit Modellauswahl)
everyapi use goose             # Goose CLI → EveryAPI (mit Modellauswahl)
everyapi use crush             # Crush CLI → isolierter EveryAPI-Modellkatalog
everyapi use cline             # Cline CLI → lebenszyklusgebundene Provider-Konfiguration
everyapi use openclaw          # lokale OpenClaw-TUI → isolierter EveryAPI-Katalog
everyapi use continue          # Continue CLI → isolierte Assistant-Konfiguration
everyapi use kilo              # Kilo Code CLI → prozessbezogene Provider-Konfiguration
everyapi use pi                # Pi Coding Agent → isolierter Modellkatalog
everyapi use pi-web            # Pi Web Browser-UI → dauerhafter models.json-Provider-Eintrag
everyapi use vibe              # Mistral Vibe → isolierter generischer Provider
everyapi use copilot           # GitHub Copilot CLI → offizielles prozessbezogenes BYOK
everyapi use droid             # Factory Droid → isolierte Laufzeiteinstellungen
everyapi use openhands         # OpenHands CLI → explizite, nur prozessbezogene Env-Overrides
everyapi use forge             # ForgeCode → isolierte OpenAI-kompatible Sitzung
everyapi use llxprt            # LLxprt Code → isoliertes Home + feste Laufzeit-Flags
everyapi use grok              # xAI Grok Build → EveryAPI
everyapi use qwen-code         # Alibaba Qwen Code → EveryAPI (mit Modellauswahl)
everyapi use kimi-code         # Moonshot Kimi Code → EveryAPI (mit Modellauswahl)
everyapi use hermes            # Nous Research Hermes Agent → EveryAPI (mit Modellauswahl)
everyapi use librefang         # LibreFang starten (nativer EveryAPI-Credential-Prozess)
everyapi use open-webui        # Open-WebUI-Server → EveryAPI als sein OpenAI-Backend
everyapi use deepseek-harness  # DeepSeek Harness Web-UI (dsh) → generierter Provider + Credential
everyapi use hermes --model gpt-5.1      # Modell festlegen, Auswahl überspringen
everyapi use claude                      # transparenter Modus als Standard: bleibt auf api.anthropic.com
everyapi use codex                       # bleibt auf api.openai.com
everyapi use antigravity                 # behält Googles offiziellen Origin
everyapi use claude --transparent=false  # transparenten Modus abschalten: Gateway-Base-URL + Relay-Key injizieren
everyapi use                             # ohne Argument → interaktive Auswahl der installierten Tools
```

Jedes Tool hat eigene Konventionen, aber das CLI merkt sie sich für dich:

| Tool | Wie es sich mit EveryAPI verbindet |
|---|---|
| claude | env: `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`; kompatible Live-Modelle über Gateway-Discovery |
| codex | env: `OPENAI_API_KEY` plus ein persistentes EveryAPI-`CODEX_HOME` zum Erhalt der Sitzung, mit lebenszyklusgebundenem `--profile` und keybezogenem Modellkatalog (codex routet über Konfiguration, nicht über `OPENAI_BASE_URL`) |
| gemini | env: `GEMINI_API_KEY`, `GOOGLE_GEMINI_BASE_URL`, `GEMINI_MODEL`; isoliertes Konfigurations-Overlay mit Auth-Modus |
| antigravity | nativer Antigravity-Launcher (`agy`) |
| aider | OpenAI-kompatible Env + LiteLLM-Modellnamensraum `openai/<model>` |
| goose | `GOOSE_PROVIDER=openai`, `GOOSE_MODEL`, `OPENAI_API_KEY`, `OPENAI_BASE_URL` |
| crush | prozessbezogene `CRUSH_GLOBAL_CONFIG`; Key wird per Env referenziert, Modellkatalog live generiert |
| cline | lebenszyklusgebundener `CLINE_PROVIDER_SETTINGS_PATH`, wird beim Beenden gelöscht |
| openclaw | lokal eingebettete TUI mit prozessbezogener Konfiguration und env-basierter SecretRef |
| continue | lebenszyklusgebundene `CONTINUE_GLOBAL_DIR/config.yaml`; Continue-Secret-Referenzen sind env-basiert |
| kilo | prozessbezogene `KILO_CONFIG_CONTENT`; OpenCode-kompatibler Provider mit env-basiertem Key |
| pi | isoliertes `PI_CODING_AGENT_DIR` mit `models.json` und der Konfiguration des gewählten Modells. `{extensions,skills,prompts,themes}`, die vor dem Start bereits in deinem `PI_CODING_AGENT_DIR` (Standard `~/.pi/agent`) lagen, werden über absolute Pfade geladen |
| pi-web | `providers.everyapi` wird in die *dauerhafte* `PI_CODING_AGENT_DIR/models.json` (Standard `~/.pi/agent`) gemergt, damit Sitzungen, Projekt-Trust, das gewählte Modell und die eigenen Änderungen des Models-Panels erhalten bleiben; der Relay-Key bleibt eine Env-Referenz und wird nie auf die Platte geschrieben |
| vibe | isolierte `VIBE_HOME/config.toml`; generischer Provider mit `api_key_env_var` |
| copilot | offizielle `COPILOT_PROVIDER_*`-BYOK-Umgebung; die Wire-API folgt den Fähigkeiten des gewählten Modells |
| droid | offizielle, nur für den Lauf gültige `--settings`-Datei mit genau einem `custom:EveryAPI-0`-Modell und env-basiertem Key |
| openhands | `--override-with-envs` mit rein prozessbezogenen `LLM_API_KEY`, `LLM_BASE_URL`, `LLM_MODEL` |
| forge | isolierte `FORGE_CONFIG`; nagelt OpenAI-kompatiblen Provider/Modell in Konfiguration und Prozess-Env fest |
| llxprt | isoliertes Anwendungs-Home und reservierte Laufzeit-Flags `--provider openai`, `--baseurl`, `--model` |
| grok | env: `XAI_API_KEY`, `GROK_MODELS_BASE_URL`; isoliertes `GROK_HOME`; gefilterte Live-Modell-Discovery |
| qwen-code | env: `OPENAI_API_KEY`, `OPENAI_BASE_URL`, `OPENAI_MODEL`; Benutzereinstellungen in einem prozessbezogenen `QWEN_HOME` und festes `--auth-type=openai` |
| kimi-code | env: `KIMI_MODEL_API_KEY`, `KIMI_MODEL_BASE_URL`, `KIMI_MODEL_PROVIDER_TYPE`, `KIMI_MODEL_NAME`; isoliertes `KIMI_CODE_HOME` mit generierten Modell-Aliassen |
| hermes | generierte `HERMES_HOME/config.yaml` mit benanntem Custom-Provider, `base_url` und inline `api_key`; gefilterte Live-Modell-Discovery |
| librefang | natives `librefang start`. Löst den Daemon ab und gibt dir dein Terminal zurück (`librefang stop` zum Beenden). LibreFang löst deine aktuellen EveryAPI-Zugangsdaten pro Anfrage auf |
| open-webui | wird als `open-webui serve` mit `OPENAI_API_BASE_URLS`, `OPENAI_API_KEYS` und `ENABLE_PERSISTENT_CONFIG=false` gestartet, damit die Prozessumgebung gegenüber jeder gespeicherten Konfiguration gewinnt; `DATA_DIR` ist auf `~/.open-webui` festgelegt |
| deepseek-harness | die offizielle `dsh web`-UI; ein generierter `llm-pi-ai.providers.everyapi`-Eintrag in `$DSH_HOME/settings.yaml` (Standard `~/.dsh`, Modus `0700`) plus eine `.credentials.yaml` mit Modus `0600`, die den Key hält |

Welches Tool welche Variable liest, ob `/v1` angehängt gehört, welchen Auth-Header-Stil es erwartet —— das musst du nicht mehr nachschlagen.

**Relay-Key-Auswahl**: Ohne `--group` löst das CLI den Auto-Group-Key deines Kontos auf —— den einen Key, der zu allen zugänglichen Gruppen routet —— und cacht ihn in `credentials.json`. Konten ohne Auto-Key (oder Tiers, die den Zugriff auf diese Gruppe verlieren) fallen auf den zuletzt aktivierten Key zurück. Mit `everyapi token switch` legst du einen anderen Key als Standard fest, mit `--group <id>` läufst du einmalig gegen einen anderen Pool. Gruppen-Overrides werden nie in diesen Cache geschrieben. Welchen Key du verwendest, bestimmt den Katalog unten: Ein auf eine Gruppe festgelegter Key sieht nur die Modelle dieser Gruppe.

Ein in einem früheren Lauf gecachter Key wird weiterverwendet —— diese Abfrage ist bewusst offline und wählt sich nicht selbst neu. Zeigt `/model` nur die Modelle einer Gruppe, führe `everyapi token switch` aus und wähle einmal `Auto`.

**Modellauswahl**: Beim Start holt EveryAPI den Live-Katalog, der für den gewählten Relay-Key bzw. die Gruppe verfügbar ist, entfernt inkompatible Medien-/Embedding-Protokolle und injiziert diesen Snapshot in die native Auswahl jedes gerouteten Clients. Nutze `/model` in Claude Code, Codex, Qwen Code und Kimi Code; `/model` oder `models` in Grok; `hermes model` in Hermes. Nicht-Claude-Modell-IDs werden intern über Claude-kompatible Aliasse dargestellt, aber mit ihrer echten ID angezeigt und stromaufwärts gesendet.

Tools mit `ModelEnv`-Vertrag (Gemini, Aider, Goose, Crush, Cline, OpenClaw, Continue, Kilo, Pi, Vibe, GitHub Copilot CLI, Factory Droid, OpenHands, ForgeCode, LLxprt, Hermes, Qwen Code, Kimi Code) öffnen die EveryAPI-Auswahl. Mit `--model <id>` überspringst du sie. In nicht-interaktiven Läufen verwendet EveryAPI deterministisch das erste kompatible Modell. Reines claude, codex und grok behalten ihr eigenes Startmodellverhalten. `antigravity` startet das native `agy` mit Google-Auth, und `librefang` nutzt seinen eigenen EveryAPI-Credential-Prozess. `pi-web`, `open-webui` und `deepseek-harness` liefern Browser-UIs: EveryAPI registriert den Provider und den gesamten kompatiblen Katalog im Voraus, und das Modell wird in dieser UI statt über eine Terminal-Auswahl gewählt.

**Reasoning-Level**: Nach dem Modell fragen `everyapi use codex` und `everyapi use pi`, mit welchem Reasoning-Level gestartet werden soll, und merken sich die Antwort für die nächsten Läufe —— einmal fragen, danach ohne Rückfrage wiederverwenden, genau wie bei den bisherigen Sicherheitseinstellungen. Die Bedingungen unterscheiden sich zwischen den beiden Clients, weil sich unterscheidet, was wir wissen. Codex liest die Stufen, die sein gebündelter Katalog für dieses Modell ausweist (`low` … `ultra`, je nach Modell —— `gpt-5.6-sol` reicht bis `ultra`, `gpt-5.5` bis `xhigh`), und nimmt die Wahl als `model_reasoning_effort` entgegen. Dafür wird das Gateway nicht gefragt, deshalb erscheint der Schritt bei Modellen, die Codex nicht kennt, gar nicht. Pi hat für Custom-Provider keine modellspezifische Tabelle, deshalb erscheint der Schritt nur, wenn das Gateway bestätigt, dass das Modell Effort akzeptiert (`supports_thinking` in `/v1/models`); die Auswahl reicht von `off` bis `high` und wird als `defaultThinkingLevel` übergeben. Ein gemerktes Level, das das aktuelle Modell nicht anbietet, wird verworfen statt festgenagelt. Beim ersten Lauf nach diesem Feature startet der Cursor auf dem Effort, der bereits in Codex' persistentem Home stand —— den Standard zu akzeptieren ändert also nichts. Die Steuerung innerhalb der Sitzung bleibt in beiden Clients erhalten —— `/model` in Codex, shift+tab in pi ——; der Launcher bewahrt deine Wahl nur über Läufe hinweg, weil Codex' generiertes Profil und Pis isoliertes Home beim Beenden gelöscht werden.

Anbieternamen sind keine CLI-Namen: Nutze `qwen-code` oder `kimi-code` für die offiziellen Clients dieser Anbieter und wähle Anbietermodelle aus dem Live-Modellkatalog eines beliebigen unterstützten Clients.

**hermes-Konfigurationsisolierung**: `everyapi use hermes` leitet `HERMES_HOME` auf ein prozessbezogenes Verzeichnis unter `~/.config/everyapi/sessions` um. Die Konfiguration mit Zugangsdaten und die Live-Proxy-URL werden beim Beenden gelöscht und können nicht mit einem anderen Key oder einer anderen Gruppe kollidieren. Persistent bleibt nur die zuletzt gewählte Modell-ID, eine unbedenkliche Einstellung. Dein persönliches `~/.hermes` bleibt unangetastet. Die generierte Konfiguration registriert EveryAPI als benannten Custom-Provider, damit `hermes model` Modelle entdecken und wechseln kann, ohne auf OpenRouter zurückzufallen. Reines `hermes` öffnet den interaktiven Chat; nutze `everyapi use hermes -- --tui`, wenn du die Terminal-Oberfläche willst.

**grok-Konfigurationsisolierung**: `everyapi use grok` leitet `GROK_HOME` auf `~/.config/everyapi/grok-home` um. Das verhindert, dass eine gecachte xAI-Browsersitzung deinen EveryAPI-Relay-Key überschreibt, und trennt über EveryAPI geroutete Sitzungen von reinem `grok`. Grok-eigene Flags gibst du nach `--` an, z. B. `everyapi use grok -- --model grok-4.5`.

**Qwen/Kimi-Konfigurationsisolierung**: Jeder geroutete Lauf erhält ein prozessbezogenes Home unter `~/.config/everyapi/sessions`, das beim Ende des Kindprozesses gelöscht wird —— parallel genutzte Keys oder Gruppen können sich also nicht gegenseitig Katalog oder Loopback-URL überschreiben. Qwens echte Systemkonfiguration bleibt intakt, Administrator-Präzedenzen bleiben erhalten. Definiert eine Administrator- oder Workspace-Konfiguration `modelProviders.openai` und würde damit den EveryAPI-Live-Katalog verdecken, schlägt der Lauf mit einem umsetzbaren Konflikt fehl, statt stillschweigend veraltete oder inkompatible Modelle anzuzeigen.

> ⚠️ **Sicherheitshinweis zur Subprozess-Umgebung**: Die obigen Umgebungsvariablen enthalten deinen Relay-API-Key. Drittanbieter-CLIs können die Umgebung im Debug-/Verbose-Modus protokollieren —— prüfe vor `everyapi use`, ob das Debug-Flag, das du einschalten willst, `*_TOKEN` / `*_API_KEY` durchsickern lässt. Lass `sed -i 's/sk-everyapi-[A-Za-z0-9]*/REDACTED/g'` über Debug-Logs laufen, bevor du sie teilst.

#### Transparenter Connector (Standard)

Der transparente Modus hält unterstützte Clients auf dem offiziellen API-Origin des Anbieters, statt eine Drittanbieter-Base-URL zu konfigurieren. Er ist der Standard bei allen Tools, die ihn unterstützen; mit `--transparent=false` schaltest du ihn ab. Das CLI startet einen kurzlebigen HTTP-CONNECT-Proxy auf einem zufälligen Loopback-Port und erzeugt pro Lauf eine CA, deren privater Schlüssel nur im Speicher existiert. Dem Kindprozess werden nur die Proxy-URL, das öffentliche CA-Bundle und nicht geheime Platzhalter-Zugangsdaten übergeben. Registrierte Modellpfade werden lokal entschlüsselt und mit deinem echten Relay-Key an EveryAPI weitergeleitet; alle anderen HTTPS-Hosts nutzen reines CONNECT-Passthrough. Unbekannte Pfade unter einem geschützten Modellpräfix werden blockiert, und ein Weiterleitungsfehler fällt nie auf den Anbieter zurück.

Verifiziert mit Claude Code und dem Codex CLI, den beiden Tools, bei denen der Standard auch greift. Natives Antigravity und LibreFang umgehen den Connector. Alle anderen registrierten Tools nutzen ihre dokumentierten Injektions- oder Konfigurationspfade, deshalb schlägt ein explizites `--transparent` bei einem nicht unterstützten Tool sichtbar fehl.

`--sanitize` steht nicht im Konflikt mit dem transparenten Modus, sondern kombiniert sich damit: Der Connector leitet über den Sanitizer weiter (Kind → Connector → Sanitizer → Gateway), sodass Maskierung und Claudes Recovery-Response-Schutz auf beiden Ausführungspfaden greifen.

Ist `ALL_PROXY` deine einzige Proxy-Variable, wird der transparente Modus abgelehnt und auf den Injektionspfad zurückgefallen —— Gos Proxy-Auflösung liest `ALL_PROXY` nicht, der Connector kann sie also nicht berücksichtigen. Setze `HTTPS_PROXY` (auch socks5, mit dem net/http nativ verbindet), wenn du den transparenten Modus behalten willst.

Dieser Modus ist experimentell und bewusst prozessbezogen:

- Die von uns abgefangene Clientseite spricht derzeit HTTP/1.1 und unterstützt normale JSON/SSE-Anfragen (HTTP/2-Antworten des Gateways werden nach HTTP/1.1 übersetzt). Clientseitiges HTTP/2, HTTP/3/QUIC, WebSockets, Clients mit Zertifikats-Pinning und Clients, die `HTTPS_PROXY` ignorieren, sind nicht im Umfang;
- Codex' eingebauter OpenAI-Provider testet den Responses-WebSocket einmal. Der Connector antwortet mit HTTP 426, sodass Codex sofort auf HTTPS/SSE zurückfällt, ohne Retry-Budget zu verbrauchen; Codex gibt für diesen fehlgeschlagenen Test möglicherweise eine Logzeile aus;
- Claude Code behandelt den nicht geheimen Platzhalter weiterhin als API-Key-Authentifizierung, deshalb sind claude.ai-Connectors deaktiviert, auch wenn `ANTHROPIC_BASE_URL` der offizielle Origin `https://api.anthropic.com` ist. Der transparente Modus vermeidet die Erkennung eines Drittanbieter-Origins; er kann API-Key-Auth nicht wie eine claude.ai-OAuth-Anmeldung aussehen lassen;
- er installiert keine System-CA, braucht keine Administratorrechte und ändert nichts am Standardverhalten von `everyapi use`;
- er ist nicht unerkennbar: Ein Client kann Proxy-Variablen, lokale Zertifikatsketten, Sockets, Timing und Antwortunterschiede untersuchen;
- der Connector sieht entschlüsselte Modellinhalte. Der CA-Signierschlüssel wird nie persistiert oder hochgeladen, und die öffentliche CA-Datei wird beim Beenden gelöscht;
- dein Relay-Key liegt weder in der Umgebung des Kindprozesses noch in der generierten Client-Konfiguration, aber eine bereits vorhandene `~/.config/everyapi/credentials.json` bleibt für jeden Prozess lesbar, der unter demselben Systembenutzer läuft. Der transparente Modus ist Isolierung der Credential-Injektion, keine Sandbox gegen feindselige Kindprozesse.

### `everyapi auth login` —— Device Authorization Grant + QR-Anmeldung

Nutzt den Device Authorization Grant (RFC-8628-Stil) + docs §7-5 Layer 1 „geräteübergreifende QR-Anmeldung":

1. Das CLI erstellt eine Sitzung und **rendert einen QR-Code im Terminal**, zusätzlich zum kurzen Code und der URL
2. Scanne den QR-Code mit dem Handy (oder öffne die URL in einem Browser, in dem du bereits bei EveryAPI angemeldet bist) —— die URL im QR-Code trägt bereits `?code=USR-789`, das Dashboard füllt den Code also automatisch aus und der Nutzer klickt nur noch auf Approve
3. Das CLI erhält das Access Token und speichert es in `~/.config/everyapi/credentials.json` (Modus 0600)

```bash
everyapi auth login                                    # Produktion; rendert standardmäßig QR und öffnet den Browser
everyapi settings set gateway_region cn               # das China-beschleunigte Gateway für folgende Befehle nutzen
everyapi auth login --api-base http://localhost:8787   # lokale Entwicklung / Self-Hosting
everyapi auth login --no-browser                       # Browser nicht öffnen (QR scannen)
everyapi auth login --no-qr                            # keinen QR rendern (Nicht-UTF-8-Terminals / Pipe-Ausgabe)
```

Beispiel für den im Terminal gerenderten QR-Code (Unicode-Halbblockzeichen, etwa 18-20 Zeilen hoch):

```
█▀▀▀▀▀█  ▀▀ ▄  █▀▀▀▀▀█
█ ███ █  ▀▄█▀  █ ███ █
█ ▀▀▀ █ ▄ ▀ █▀ █ ▀▀▀ █
▀▀▀▀▀▀▀ █▄█▄█▄ ▀▀▀▀▀▀▀
... (der echte QR kodiert verification_uri?code=USR-789)
```

Warum dieser Pfad phishing-resistenter ist:

- der Nutzer **tippt auf dem neuen Gerät kein Passwort** → eine Phishing-Seite hat keinen Ort, Zugangsdaten abzugreifen
- der Nutzer **wird nicht auf eine unbekannte Browserseite umgeleitet** → die Redirect-Phishing-Fläche entfällt
- selbst wenn das CLI ein bösartiger Fork wäre, der einen gefälschten QR erzeugt, ist die Genehmigungsseite nach dem Scan das echte everyapi.ai-Dashboard (ausgelöst von einem Gerät, auf dem du bereits angemeldet bist) —— und ein Nutzer genehmigt keinen Code, den er nicht erkennt

Die übrigen Schichten aus docs §7-5 (Zertifikats-Pinning / Phrase / PKCE-OAuth) wurden in eigenen PRs ausgeliefert (Pinning ist reiner Report-Modus: die Produktentscheidung lautet, nicht zu blockieren).

### `everyapi seller <sub>` —— Marketplace-Verkäufer-Unterbefehle

Bringen Kanalregistrierung und Auszahlungsablauf des Dashboards ins Terminal und ermöglichen skriptbares Onboarding. Vor der Registrierung eines Kanals prüft `seller setup` die Berechtigung (Konto aktiv / E-Mail verifiziert / Kontoalter / Ausgabenhistorie / Kanallimit) und listet fehlgeschlagene Bedingungen auf, **bevor der Nutzer einen Key eintippt** —— damit man das nicht erst nach dem Absenden per 422 erfährt.

```bash
everyapi seller list                          # registrierte Kanäle auflisten
everyapi seller withdraw                      # alle ausstehenden Verkäufererlöse auf das Hauptguthaben übertragen
everyapi seller withdraw --quota 1000         # Teilübertrag (Datenbankeinheiten)
everyapi seller add-key   --type claude --name 'my-pro' \
                        --key 'sk-ant-...' --models 'claude-3-opus,claude-3-sonnet'
everyapi seller add-oauth codex --name 'my-chatgpt' --models 'gpt-4'
                                            # Ein-Klick-OAuth: das CLI startet den Device Flow, der Nutzer tippt
                                            # den user_code im Browser ein, das Token landet im Kanal
everyapi seller add-oauth claude --name 'my-claude' --models 'claude-3-opus,claude-3-sonnet'
                                            # Paste-Flow: das CLI öffnet Anthropics Autorisierungsseite, der Nutzer
                                            # fügt das im Callback angezeigte code#state ins Terminal ein
everyapi seller add-oauth gemini --name 'my-gemini' --models 'gemini-1.5-pro'
                                            # echtes Ein-Klick-Loopback: das CLI öffnet einen Listener auf einem
                                            # zufälligen Port, Google liefert den Code direkt ans CLI, kein Einfügen
everyapi seller setup                         # interaktiver Assistent: prüft zuerst die Berechtigung, führt dann durch add-key
```

#### `add-key` —— Backup-Pool aus mehreren Keys

`--key` kann wiederholt werden, um N gleichwertige Zugangsdaten am selben Kanal als Backup-Pool zu registrieren (B2, PRODUCT §4.5). Gibt der Haupt-Key 401/403 zurück, macht das Backend automatisch Failover auf den nächsten. `--key-remark` ist ebenfalls wiederholbar und wird positionsweise mit `--key` gepaart (das i-te `--key-remark` beschriftet den i-ten `--key`, damit du sie später im Dashboard identifizieren kannst). OAuth-Blobs können keinen Backup-Pool bilden —— sie bleiben Kanäle mit einem einzelnen Key.

```
everyapi seller add-key   --type claude --name 'claude-pool' \
                        --models 'claude-3-opus' \
                        --key 'sk-ant-primary' --key-remark 'primary' \
                        --key 'sk-ant-backup'  --key-remark 'team backup'
```

Das `--type` von `add-key` akzeptiert Aliasse (`openai` / `claude` / `gemini` / `codex` / `vertex` / `aws` / `xai` / `deepseek`) oder die numerische ID. Die Registrierung unterliegt den Marketplace-Berechtigungsbedingungen (Konto aktiv, E-Mail verifiziert, Ausgabenhistorie, Kanallimit), und das CLI listet die fehlgeschlagene Prüfliste vor allem anderen auf —— an allen drei Einstiegspunkten (`add-key` / `add-oauth` / `setup`).

#### `add-oauth codex` —— Ein-Klick-OAuth (Device Flow)

`everyapi seller add-oauth codex --name 'my-chatgpt' --models 'gpt-4'` durchläuft den RFC-8628-artigen Device-Authorization-Flow von Codex / ChatGPT —— der Verkäufer **fasst keinen Token-String an**:

1. Das CLI ruft `/api/seller/codex/device/start` auf und erhält einen kurzen `user_code` und eine `verification_uri`
2. Das CLI öffnet standardmäßig `https://auth.openai.com/codex/device` im Browser (`--no-browser` unterdrückt das). Der Nutzer tippt den `user_code` im Browser ein und schließt die Autorisierung ab
3. Das CLI pollt `/api/seller/codex/device/poll`. Nach der Autorisierung erstellt das Backend den Kanal und schreibt das OAuth-Token in dessen `key`-Feld
4. Ausgabe: Kanal-ID + die verknüpfte ChatGPT-E-Mail

Autorisierungs-Cookies verwaltet ein prozessinterner `http.CookieJar` und werden nicht persistiert —— der Device-Flow-Zustand ist kurzlebig und prozessgebunden, was zum Bedrohungsmodell passt.

#### `add-oauth claude` —— Paste-and-Submit-OAuth

`everyapi seller add-oauth claude --name … --models …`. Anthropics OAuth-Provider nagelt die `redirect_uri` auf seiner Seite auf `https://console.anthropic.com/oauth/code/callback` fest, das CLI kann den Callback also nicht mit einem localhost-Listener empfangen. Der Ablauf:

1. Das CLI ruft `/api/seller/claude/oauth/start` auf. Das Backend erzeugt das PKCE-Paar + State und gibt Anthropics Authorize-URL zurück
2. Das CLI öffnet standardmäßig den Browser (`--no-browser` unterdrückt das). Der Nutzer meldet sich bei Anthropic an und stimmt zu
3. Anthropic leitet auf eine Callback-Seite um, die einen `<code>#<state>`-String anzeigt
4. **Der Nutzer fügt diesen String ins CLI ein**
5. Das CLI ruft `/api/seller/claude/oauth/complete` auf. Das Backend tauscht Code + Verifier gegen das Token und erstellt den Kanal

Ein Einfügeschritt mehr als beim Device Flow, aber weit einfacher, als `~/.claude/auth.json` von Hand zu suchen. Das Session-Cookie stellt das Backend beim Start aus, und Complete muss dieselbe Sitzung erreichen —— der `http.CookieJar` des CLIs lebt im Prozess und ist pro Aufruf isoliert.

#### `add-oauth gemini` —— echtes Ein-Klick-Loopback-OAuth

`everyapi seller add-oauth gemini --name … --models … [--no-browser] [--timeout 5m]`. Googles gemini-cli-OAuth-Client für installierte Anwendungen akzeptiert `http://127.0.0.1:<port>/callback` als `redirect_uri`, deshalb **öffnet das CLI seinen eigenen Listener für den Callback** —— der Nutzer meldet sich nur im Browser an, nichts zum Einfügen. Der Ablauf:

1. Das CLI öffnet einen Einweg-HTTP-Listener auf einem zufälligen ephemeren Port (`127.0.0.1:0`), Pfad fest auf `/callback`
2. Das CLI ruft `/api/seller/gemini/oauth/start` mit `redirect_uri = http://127.0.0.1:<port>/callback` auf. Das Backend validiert den Redirect streng: Loopback / Port ≥ 1024 / Schema http / Pfad /callback / kein Query, Fragment oder Userinfo (verhindert SSRF und Redirect-Hijacking)
3. Das CLI öffnet standardmäßig den Browser. Der Nutzer meldet sich bei Google an und stimmt zu
4. Google leitet mit `?code=…&state=…` an den Listener des CLIs um
5. Das CLI prüft die State-Übereinstimmung (schützt vor veralteten oder gefälschten Flows) und ruft `/api/seller/gemini/oauth/complete` auf
6. Das Backend tauscht Code + dieselbe redirect_uri gegen das Token und erstellt den Kanal

Vergleich mit den anderen beiden Anbietern:

| Anbieter | UX | Grund |
|---|---|---|
| `codex` | Nutzer tippt einen 6-stelligen user_code im Browser, das CLI pollt automatisch | OpenAI Device Flow, keine redirect_uri |
| `claude` | Nutzer meldet sich im Browser an und fügt `code#state` ins CLI ein | Anthropic nagelt die redirect_uri auf die eigene Callback-URL fest |
| `gemini` | Nutzer meldet sich im Browser an, schließt den Tab, fertig | Google erlaubt Loopback-Redirects |

`--timeout` begrenzt die Wartezeit (Standard 5 Minuten). Bei Zeitüberschreitung beendet sich das CLI und schließt den Listener sauber.

### `everyapi edge <sub>` —— Ein-Befehl-Deployment des BYO-GPU-Supplier-Agents

Ermöglicht es, ungenutzte GPUs über EveryAPI zu verkaufen. Das CLI komprimiert das Deployment auf einen einzigen Befehlssatz —— `register` / `list` / `start` / `status` / `logs` / `models` / `rename` / `pause` / `resume` / `stop` / `update` / `remove` ——, damit Anbieter kein docker-compose von Hand kopieren, keine `.env` ausfüllen und keine Registrierungstoken jonglieren müssen. Der übliche Weg sind acht Befehle:

```bash
everyapi auth login                              # bestehende Anmeldung wiederverwenden
everyapi edge register --name "rtx-4090"    # ruft /api/seller/edge/nodes für node_id + Token auf, schreibt ~/.local/share/everyapi/edge/<id>/
everyapi edge start                         # erkennt NVIDIA / ROCm / Apple Silicon / CPU, docker compose up -d
everyapi edge models pull llama3.1:8b       # docker compose exec ollama ollama pull ...
everyapi edge status                        # lokales docker compose ps + online/offline-Status aus dem Dashboard
everyapi edge logs -f                       # Logs verfolgen
everyapi edge update                        # docker compose pull && up -d
everyapi edge remove                        # down -v + lokales Verzeichnis löschen + DELETE im Backend
```

`start` rendert die `docker-compose.yml` zur Laufzeit mit `text/template` (**kein eingebettetes statisches YAML**) —— dadurch werden Containernamen mit der node_id namespaced, mehrere Nodes auf einem Host kollidieren nicht, und GPU-Passthrough wird pro Modus bedingt gerendert (NVIDIA = `deploy.resources.devices` + nvidia-Treiber, ROCm = `/dev/kfd` + `/dev/dri` + `group_add: video`, macOS = kein ollama-Container, der Agent verbindet sich über `host.docker.internal` mit dem nativen ollama des Hosts).

Credential-Fluss: Das CLI ruft `POST /api/seller/edge/nodes` mit deinem bestehenden `sk-everyapi-`-Bearer auf → das Backend gibt einmalig ein `registration_token` zurück (danach speichert es nur den sha256 und zeigt es nie wieder) → das CLI schreibt es mit Modus 0600 nach `~/.local/share/everyapi/edge/<id>/node.json` → es wird als `EVERYAPI_REGISTRATION_TOKEN`-Variable im Compose gerendert. **Das Registrierungstoken wird in keine .env-Datei geschrieben** (damit Anbieter es nicht versehentlich committen).

Voraussetzungen: `docker` + `docker compose v2` (v1 ist EOL und nicht unterstützt). Auf macOS: `brew install ollama && brew services start ollama` (Metal-Beschleunigung funktioniert nicht in einem Docker-Container).

### `everyapi wallet topup` —— Aufladeweiterleitung mit Anti-Phishing-Phrase

`everyapi wallet topup` öffnet die Aufladeseite des Dashboards. Vor der Weiterleitung greift die Prüfung aus docs §7-5 Layer 3:

1. Das CLI ruft das Backend `POST /api/cli/jump-session` auf und erhält eine Session-ID + eine Phrase aus 4 Emojis (z. B. `🌊 🦊 🍕 🚀`)
2. Das CLI gibt sowohl die URL als auch die Phrase im Terminal aus und weist darauf hin: „gleich solltest du dieselbe Phrase oben auf der Seite sehen"
3. Der Nutzer drückt Enter, das CLI öffnet die URL im Systembrowser (inklusive `?jump_session=<id>`)
4. Beim Laden ruft das Dashboard das Backend `GET /api/cli/jump-session/:id/phrase` auf, erhält dieselbe Phrase und **zeigt sie prominent im Seitenkopf an**
5. Der Nutzer vergleicht visuell: Übereinstimmung → echtes EveryAPI; keine Übereinstimmung oder nichts angezeigt → Tab schließen (möglicherweise Phishing)

Warum das Phishing bremst: Die Phrase lebt im Speicher des Backends, indiziert über eine zufällige 32-Hex-Session-ID. Eine Phishing-Seite hat keinen authentifizierten Weg, sie abzurufen, und ein vom Angreifer gefälschtes `wallet/topup?jump_session=<id>` kann sie ebenfalls nicht lesen. Kurze TTL (10 Minuten) + Einmalverwendung (die Sitzung wird gelöscht, sobald das Dashboard sie abruft) senken das Wiederverwendungsrisiko weiter.

```bash
everyapi wallet topup                    # öffnet standardmäßig den Browser
everyapi wallet topup --no-browser       # gibt nur die URL zum manuellen Kopieren aus
```

### `everyapi auth status` —— aktuelles Guthaben / Verbrauch / Kontingent

```
$ everyapi auth status

  alice (alice@example.com)
  quota:     $12.34 remaining   $5.67 used
  requests:  1,234
  topup:     https://app.everyapi.ai/wallet
```

### `everyapi version update` —— führt das Upgrade für dich aus

Ein `everyapi update` auf oberster Ebene gibt es nicht; die CLI-Lebenszyklus-Aktionen liegen unter `version` (`everyapi version update`, `everyapi version uninstall`).

Prüft das neueste Release des GitHub-Spiegels, vergleicht es mit deiner aktuellen Version und übergibt das Upgrade dann an das, was die Binary installiert hat —— Homebrew (`brew update && brew upgrade everyapi`), `go install …@latest` oder das veröffentlichte Installationsskript. Ein Befehl, kein Copy-Paste.

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

Warum die Binary nicht direkt ersetzen? Weil die Verifikationsketten von Homebrew und Go (SHA / Bottle-Signaturen / Modul-Checksumme) robuster sind als alles, was wir im CLI nachbauen würden, und weil sich eine laufende ausführbare Datei unter Windows selbst zu ersetzen ein Minenfeld ist. Eine per Installationsskript installierte Binary wird tatsächlich an Ort und Stelle ersetzt —— aber durch erneutes Ausführen des veröffentlichten Installers, der genau das bereits sicher erledigt.

Flags:
- `--check` —— vergleicht still. Exit 0, wenn aktuell, Exit 1, wenn veraltet, Exit 2, wenn sich die neueste Version nicht ermitteln ließ (Grund auf stderr) —— ein Netzwerkaussetzer darf nicht als „ein Upgrade ist verfügbar" gelesen werden. Für CI / cron:
  ```bash
  everyapi version update --check || echo "needs upgrade"
  ```
- `--dry-run` —— gibt die Befehle aus, die ausgeführt würden, ohne sie auszuführen (zur Bestätigung)

### `everyapi settings` —— CLI-Einstellungen (Sprache usw.)

Das CLI bringt i18n für 8 Sprachen mit: Englisch, vereinfachtes Chinesisch, traditionelles Chinesisch, Japanisch, Koreanisch, Spanisch, Deutsch und Französisch —— CLI-Texte werden in der gewählten Sprache gerendert. Backend-API-Fehler werden automatisch über den `Accept-Language`-Header ausgehandelt und decken dieselben 8 ab.

```bash
$ everyapi settings                          # interaktive Auswahl: Sprache wählen
$ everyapi settings list                     # aktuelle Einstellungen ansehen
$ everyapi settings set language zh          # direkt setzen
$ everyapi settings set language fr          # dasselbe für Französisch
$ everyapi settings set terminal_mode tmux   # interaktive Starts in tmux halten
$ everyapi use codex -- resume               # an das einzige Projekt-tmux andocken oder Codex' Auswahl öffnen
$ everyapi settings reset                    # auf Standard zurücksetzen (en + LANG-Autoerkennung)
```

**Terminal-Modus**: Der erste interaktive `everyapi use` fragt, ob Starts in deinem nativen Terminal oder in tmux laufen sollen, und speichert die Wahl als `terminal_mode`. Der tmux-Modus startet den gesamten `everyapi use`-Prozess innerhalb einer `everyapi-v3-*`-Sitzung neu, identifiziert über das gewählte Tool, die Dateisystem-Identität des Workspaces und eine zufällige 128-Bit-Startidentität, sodass Connector, Sanitizer, temporäre Konfiguration und Zieltool ein Detach überleben. Die Startmeldung gibt den exakten `tmux attach -t <session>`-Befehl aus. Ein reines Codex-`resume` sucht zuerst nach dieser Identität: Gibt es genau ein lebendes verwaltetes Agent-Pane, wird es über den exakten Sitzungsnamen revalidiert und wieder angedockt; bei null oder mehreren rät es nicht und fällt auf Codex' normale Resume-Auswahl zurück. Vor jedem tmux-Start betrachtet das CLI nur streng generierte `everyapi-v3-*`-, `everyapi-v2-*`- oder Legacy-`everyapi-<pid>-<timestamp>`-Sitzungen als Kandidaten und entfernt sie nur, wenn ein einzelner atomarer tmux-Befehl revalidiert, dass die Sitzung genau ein Fenster mit genau einem bereits toten EveryAPI-Wrapper-Pane enthält. Lebende abgedockte Agenten, vom Nutzer erstellte normale tmux-Sitzungen und jede Sitzung mit vom Nutzer hinzugefügten Panes oder Fenstern bleiben immer erhalten. Eine Sitzung, deren verwaltetes Pane tot ist, die aber noch lebende, vom Nutzer hinzugefügte Panes hat, bleibt erhalten, wird aber nicht wiederverwendet. Jeder gestartete Client kann `EVERYAPI_TERMINAL_MODE`, `EVERYAPI_TMUX_SESSION` und `EVERYAPI_TMUX_ATTACH_COMMAND` abfragen. Codex, Claude Code, OpenCode und Kilo erhalten denselben Sitzungskontext zusätzlich über ihre dokumentierte Modellanweisungs-Schnittstelle, inklusive der Regel, keine verschachtelten tmux-Sitzungen zu erzeugen. Andere Clients behalten nur den Umgebungsvertrag, ohne Injektion von Nutzernachrichten. Ein Start, der bereits in tmux läuft, verschachtelt nicht, und nicht-interaktive Starts bleiben immer nativ. Ist tmux nicht verfügbar, deaktiviert die Erstauswahl diese Option. Kollidiert deine bestehende tmux-Konfiguration, schlägt der Start mit Hinweisen zur Installation bzw. zum Zurücksetzen fehl, statt das Verhalten still zu ändern.

**Autoerkennung**: Wenn du nichts explizit gesetzt hast, liest das CLI beim Start die Umgebungsvariablen in dieser Reihenfolge: `EVERYAPI_LANG > LC_ALL > LC_MESSAGES > LANG`. Ist deine System-Locale `zh_CN.UTF-8` / `ja_JP.UTF-8` / `fr_FR.UTF-8` usw., greift das sofort —— ohne Konfiguration.

**Einmaliges Override**:

```bash
EVERYAPI_LANG=zh everyapi auth status             # nur dieser Aufruf auf Chinesisch, wird nicht gespeichert
```

**Übersetzungsbeispiel** (Fehler „nicht angemeldet", 8 Sprachen × derselbe Satz):

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

Die Einstellungen liegen in `~/.config/everyapi/settings.json` (dasselbe Verzeichnis wie `credentials.json`, aber mit Modus `0644` —— keine Geheimnisse enthalten).

**Übersetzungen verbessern / eine Sprache hinzufügen**: siehe [`internal/i18n/locales/README.md`](../internal/i18n/locales/README.md).

## Konfigurationsdateien

Zugangsdaten liegen in `~/.config/everyapi/credentials.json` (oder `$XDG_CONFIG_HOME/everyapi/`, wenn `$XDG_CONFIG_HOME` gesetzt ist) mit Dateimodus `0600`. Geschrieben von `everyapi auth login`, gelesen von allen anderen Befehlen.

> ⚠️ **Das Token wird im Klartext gespeichert**. Dateimodus `0600` + ein privater `$HOME`-Pfad entsprechen der Praxis von Branchen-CLIs wie `gh auth` oder `aws configure`, aber **unter einem Bedrohungsmodell mit Gerätediebstahl oder Malware** kann jeder Prozess, der diese Datei lesen kann, die EveryAPI-API als du aufrufen (auch MCP-Tools —— siehe den Money-Path-Frictionschritt weiter unten). Empfehlungen:
> - führe `everyapi auth login` nicht auf geteilten oder öffentlichen Geräten aus
> - macOS: erwäge `everyapi auth logout`, bevor du FileVault aktivierst
> - Linux: aktiviere die Verschlüsselung des Home-Verzeichnisses (`ecryptfs` / LUKS)
> - bei Verdacht auf Kompromittierung → `everyapi auth logout` löscht die lokalen Zugangsdaten sofort; rotiere anschließend deinen API-Key im EveryAPI-Dashboard
>
> Plattform-Keychain-Backends (macOS Keychain / Windows DPAPI / Linux Secret Service) sind geplant, aber noch nicht ausgeliefert.

Felder:

- `api_base` —— URL des EveryAPI-Gateways. Standard `https://api.everyapi.ai`. Self-Hosting-Nutzer und lokale Entwicklung können ihn beim `auth login` mit `--api-base` überschreiben.
- `access_token` —— der Bearer für alle authentifizierten API-Aufrufe.
- `relay_key` —— der Relay-API-Key (`sk-everyapi-…`), verwendet in der Subprozess-Umgebung von `everyapi use`. Wird von `/api/token/*` geholt und hier gecacht.
- `user_id` / `username` —— gecacht, damit `auth status` die Identitätszeile vor dem ersten API-Roundtrip rendern kann.

Die Gateway-Region ist eine CLI-Einstellung in `settings.json`: Ist sie nicht gesetzt, fragt der interaktive Login einmal und speichert die Wahl. `everyapi settings set gateway_region cn` leitet den offiziellen Gateway-Verkehr auf `https://api-cn.everyapi.ai`, `global` nutzt `https://api.everyapi.ai`. Eine eigene `--api-base` fürs Self-Hosting hat weiterhin Vorrang.

## Entwicklung

Aus dem Quellverzeichnis des CLIs (wo dieses README, `go.mod` und das `Makefile` liegen):

```bash
go test ./...
go run . auth status       # gegen Produktion
go run . auth login --api-base http://localhost:8787   # gegen ein lokales Backend
```

Lokale Cross-Kompilierung für alle Plattformen (dasselbe Rezept wie CI):

```bash
make cli-release           # Artefakte in dist/ (6 Plattformen × 1 Binary = 6 Dateien)
```

## MCP-Server (Unterbefehl `everyapi mcp`)

Die `everyapi`-Binary **enthält** einen [Model-Context-Protocol](https://modelcontextprotocol.io)-Server —— als Unterbefehl bereitgestellt (`everyapi mcp` liest von stdin und schreibt nach stdout). Ein KI-Agent (Claude Code / Cursor / Codex CLI / beliebiger MCP-Client) kann ihn direkt aufrufen, **ohne dass der Nutzer ein Terminal öffnet**.

> ⚠️ **Authentifizierungsmodell und Angriffsfläche des MCP-Servers**
>
> - **Öffnet keinen Port**: `everyapi mcp` ist reines stdio-JSON-RPC und wird vom Host-CLI geforkt. **Es lauscht auf keinem Socket und keinem TCP-Port** —— null Netzwerkangriffsfläche.
> - **Liest `~/.config/everyapi/credentials.json` direkt**: Der MCP-Server hat keinen eigenen Auth-Flow, also bedeutet Lesezugriff auf die Credential-Datei = Aufruf aller bereitgestellten Tools als du. Jeder MCP-Host, der einen Prozess mit deinen Benutzerrechten starten kann, hat vollen Zugriff.
> - **Der Money Path `everyapi_seller_withdraw` hat einen Frictionschritt**: Aufrufer müssen `confirm: "yes"` übergeben, was sicherstellt, dass ein KI-Agent die Transferaktion einem Menschen in der Oberfläche zeigt, und stille Geldabflüsse verhindert. Andere, nur lesende Tools (status / topup / seller_list) verlangen das nicht.
>
> Installiere keine MCP-Hosts, denen du nicht vertraust.

### Installation

Dieselbe Binary wie das CLI: Wer das CLI installiert, hat den MCP-Server bereits:

```bash
make cli                                              # lokaler Build, erzeugt ./bin/everyapi
# oder per go install:
go install github.com/everyapi-ai/everyapi-ai/v3@latest
```

### Anbindung an Claude Code

Ergänze `~/.claude/settings.json`:

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

Die Anbindung an Cursor, das Codex CLI und andere MCP-Clients funktioniert ähnlich: `command` auf die `everyapi`-Binary zeigen lassen und `args: ["mcp"]` setzen.

### Authentifizierungsvoraussetzung

Du musst `everyapi auth login` mindestens einmal in einem Terminal ausführen —— der MCP-Server ist ein Hintergrundprozess ohne Terminalinteraktion und kann den Device-Code-Flow nicht selbst durchführen. Er liest `~/.config/everyapi/credentials.json` direkt; existiert die Datei nicht, geben alle Tools eine „not logged in"-Meldung mit `isError: true` zurück, die den Nutzer zur Anmeldung führt.

### Bereitgestellte Tools (15 insgesamt)

| Tool | Eingabe | Wofür |
|---|---|---|
| `everyapi_status` | keine | aktuelles Guthaben / Verbrauch / Anfragezahl |
| `everyapi_topup` | keine | gibt die Web-Auflade-URL zurück |
| `everyapi_seller_list` | keine | listet die Marketplace-Verkäuferkanäle |
| `everyapi_seller_withdraw` | `{confirm: "yes", quota?: int}` | überträgt seller_quota auf das Hauptguthaben; **erfordert `confirm: "yes"`** (Money-Path-Friction) |
| `everyapi_seller_eligibility` | keine | schreibgeschützte Checkliste des Mount-Gates (Marketplace offen, Konto aktiv, E-Mail verifiziert, Kontoalter, frühere Nutzung, Kanal-Limit). Rufe sie auf, *bevor* du den Nutzer nach einem Key fragst |
| `everyapi_seller_add_key` | `{name, type, keys[], models, key_remarks?[], remark?}` | mountet einen Verkäuferkanal aus API-Keys im Klartext —— das Gegenstück zu `everyapi seller add-key`. Übergib ausschließlich Keys, die der Nutzer im Gespräch selbst genannt hat |
| `everyapi_seller_add_oauth_codex_start` | `{name, models}` | startet den Codex-/ChatGPT-Device-Authorization-Flow, gibt `user_code` + `verification_uri` + `flow_id` zurück |
| `everyapi_seller_add_oauth_codex_poll` | `{flow_id}` | fragt den Codex-Autorisierungsstatus ab. `pending`/`slow_down` = weiter pollen, `authorized` gibt die Kanal-ID zurück, `expired`/`denied` beendet |
| `everyapi_seller_add_oauth_claude_start` | `{name, models}` | startet den Anthropic-OAuth-Flow, gibt `authorize_url` zurück. Der Nutzer meldet sich im Browser an und erhält einen `<code>#<state>`-String |
| `everyapi_seller_add_oauth_claude_complete` | `{input}` | reicht den vom Nutzer im vorherigen Schritt eingefügten `<code>#<state>`-String ein und erstellt den Kanal |
| `everyapi_edge_list` | keine | listet die BYO-GPU-Edge-Nodes: ID, Name, Online-Status, gekoppelter Kanal, zuletzt gesehen, installierte Modelle |
| `everyapi_edge_status` | `{node_id: int}` | Detail zu einem Node —— Pause-Flag, Agent-Version, GPU-Modell / -Anzahl / VRAM, installierte Modelle |
| `everyapi_edge_remove` | `{node_id: int, confirm: "yes"}` | löscht einen Node (und seinen gekoppelten Kanal, wenn es der letzte war); **erfordert `confirm: "yes"`** (Destructive-Path-Friction) |
| `everyapi_admin_marketplace_status` | keine | liest das deploymentweite Flag `marketplace.enabled`. Erfordert die Admin-Rolle |
| `everyapi_admin_marketplace_set` | `{enabled: bool, confirm: "yes"}` | öffnet oder schließt den Marketplace für das gesamte Deployment; **erfordert `confirm: "yes"`**. Bestehende Nodes und Kanäle liefern auch im geschlossenen Zustand weiter aus |

**Nutzungsmuster der OAuth-Tools** (so führt ein KI-Agent das im Gespräch durch):

```
Nutzer: füge einen ChatGPT-Plus-Verkäuferkanal hinzu, Name my-chatgpt, models gpt-4
KI    → everyapi_seller_add_oauth_codex_start({name: "my-chatgpt", models: "gpt-4"})
       ← "gib USR-789 auf chatgpt.com/codex ein und sag Bescheid, wenn du fertig bist"
Nutzer: erledigt, im Browser gemacht
KI    → everyapi_seller_add_oauth_codex_poll({flow_id: "..."})
       ← "status=pending, noch ein paar Sekunden warten"
[pollt weiter bis authorized]
       ← "status=authorized — channel #314 mounted"

Nutzer: füge auch den Claude-Pro-Kanal hinzu, my-claude / claude-3-opus
KI    → everyapi_seller_add_oauth_claude_start({...})
       ← "schließe die Autorisierung unter [URL] ab und gib mir den code#state-String"
Nutzer: code-abc123#state-xyz
KI    → everyapi_seller_add_oauth_claude_complete({input: "code-abc123#state-xyz"})
       ← "Channel #315 mounted"
```

Gemini-OAuth (Loopback-Flow) wird **nicht über MCP bereitgestellt** —— der Lebenszyklus des Loopback-Listeners passt nicht zu einem Lebenszyklus, der über Tool-Aufrufe hinwegreicht. Für Gemini bleibt `everyapi seller add-oauth gemini` im CLI.

### Manueller Smoke-Test

```bash
make cli
./bin/everyapi mcp <<'EOF'
{"jsonrpc":"2.0","id":1,"method":"initialize"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"everyapi_status","arguments":{}}}
EOF
```

Du solltest 3 JSON-Antwortzeilen sehen: das initialize-Ergebnis, eine Liste mit 15 Tools und den status-Text (oder ein isError „nicht angemeldet").

## Was diese Binary noch **NICHT** enthält

Derzeit **nicht implementiert** (nach Wichtigkeit sortiert; wird in späteren Releases schrittweise ergänzt, ohne die v1-Oberfläche zu brechen):

- ⚠️ Code-Signierung auf Betriebssystemebene (macOS-Notarisierung / Windows Authenticode) —— heute verlassen wir uns auf die doppelte Prüfung mit sigstore cosign keyless + SHA256SUMS, beide an jedes GitHub Release angehängt und von Homebrew automatisch verifiziert
- ❌ Plattform-Keychain-Backends —— das Token liegt weiterhin im Klartext auf der Platte (Modus 0600)

Dinge, die hier gelistet waren, aber **bereits ausgeliefert sind** (nicht als offen behandeln):

- ✅ Lokaler Sanitizer-Proxy —— die Befehle sind `everyapi proxy {start,stop,status,configure}` (nicht `everyapi start` / `everyapi configure`). Engine + 6 eingebaute Detektoren + eigene Regexe, integriert mit `everyapi use`
- ✅ Verkäufer-OAuth-Onboarding für alle drei Anbieter (codex device / claude paste / gemini loopback)
- ✅ QR-Anmeldung als Hauptpfad —— `auth login` nutzt Device-Code **+ QR als Hauptpfad**, `--no-qr` als Fallback
- ✅ Anti-Phishing-Schichten —— Phrase (`everyapi wallet topup`), strenge PKCE-/State-Prüfungen und Zertifikats-Pinning sind alle ausgeliefert. Pinning ist **reiner Report-Modus** (still bei Übereinstimmung, Warnung bei Abweichung, verweigert nie die Verbindung), und die Produktentscheidung lautet „warnen, nicht erzwingen"

## Schwachstellen melden

Siehe [`SECURITY.md`](../SECURITY.md).
