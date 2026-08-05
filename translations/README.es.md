> 🌐 [English](../README.md) · [简体中文](README.zh-CN.md) · [日本語](README.ja.md) · [한국어](README.ko.md) · **Español** · [Deutsch](README.de.md) · [Français](README.fr.md)

# CLI `everyapi`

CLI de buyer onboarding para la pasarela de APIs de IA [EveryAPI](https://everyapi.ai). Inicia Claude Code, Codex, Antigravity, Grok Build, Qwen Code o Kimi Code **en menos de un minuto**.

Estado: **flujos centrales listos** — buyer onboarding, comandos de seller (plain-key + OAuth para tres proveedores), sanitizer proxy, ruta principal de QR sign-in y capas anti-phishing están todas implementadas. Lo único que falta es el code signing a nivel de SO y un backend de keychain de plataforma (ver «Lo que este binario AÚN no incluye» al final).

## Instalación

```bash
brew tap everyapi-ai/tap && brew install everyapi
```

Para actualizar después — primero `brew update`:

```bash
brew update && brew upgrade everyapi
```

Sin `brew update`, `brew upgrade everyapi` usa el formula en caché y reporta "already installed" incluso cuando hay una nueva release.

## Comandos

| Comando | Función |
|---|---|
| `everyapi login` | Iniciar sesión en EveryAPI desde este dispositivo |
| `everyapi logout` | Borrar las credenciales locales |
| `everyapi status` | Ver saldo, uso y cuota |
| `everyapi topup` | Abrir la página de recarga (con verificación de phrase anti-phishing) |
| `everyapi use <tool>` | Configurar env y hacer exec a un CLI de terceros (apuntando a EveryAPI) |
| `everyapi seller <sub>` | Comandos del lado vendedor del marketplace (list / withdraw / add-key / setup) |
| `everyapi edge <sub>` | Despliegue de un comando para el supplier agent BYO-GPU (register / start / status / logs / models / stop / update / remove) |
| `everyapi mcp` | Ejecutar como servidor MCP (JSON-RPC sobre stdin/stdout) |
| `everyapi update` | Verificar nueva versión e imprimir el comando de upgrade para tu método de instalación |
| `everyapi version` | Mostrar la versión de build |
| `everyapi help` | Ayuda |

### `everyapi use <tool>` — exec a un CLI de terceros (apuntando a la pasarela EveryAPI)

La razón principal para instalar este CLI. Configura e inicia los clientes de código compatibles mediante EveryAPI; la entrada `gemini` inicia el CLI Antigravity ya autenticado.

```bash
everyapi use claude         # Claude Code → EveryAPI
everyapi use codex          # OpenAI Codex CLI → EveryAPI
everyapi use gemini         # Iniciar Antigravity
everyapi use grok           # xAI Grok Build → EveryAPI
everyapi use qwen-code      # Alibaba Qwen Code → EveryAPI
everyapi use kimi-code      # Moonshot Kimi Code → EveryAPI
everyapi use                # sin argumentos → selector interactivo sobre los tools instalados
```

Cada tool usa convenciones de env distintas; el CLI las recuerda por ti:

| Tool | Variables de entorno que se configuran |
|---|---|
| claude | `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN` |
| codex | `OPENAI_BASE_URL`, `OPENAI_API_KEY` |
| gemini | lanzador nativo de Antigravity (`agy`) |
| grok | `XAI_API_KEY`, `GROK_MODELS_BASE_URL` |
| qwen-code | `OPENAI_API_KEY`, `OPENAI_BASE_URL`, `OPENAI_MODEL`; `QWEN_HOME` aislado |
| kimi-code | `KIMI_MODEL_API_KEY`, `KIMI_MODEL_BASE_URL`, `KIMI_MODEL_NAME`; `KIMI_CODE_HOME` aislado |

Ya no hace falta consultar qué variable lee cada tool, si necesita el sufijo `/v1` o qué estilo de auth header usar.

> ⚠️ **Aviso de seguridad sobre env de subproceso**: las variables de entorno anteriores contienen tu relay API key. El modo debug / verbose de CLIs de terceros puede loguear el env — antes de hacer `everyapi use`, verifica que el flag debug que enciendas no filtre `*_TOKEN` / `*_API_KEY`. Antes de compartir logs de debug, ejecuta `sed -i 's/sk-everyapi-[A-Za-z0-9]*/REDACTED/g'`.

### `everyapi login` — Device Authorization Grant + QR sign-in

Usa Device Authorization Grant (estilo RFC 8628) + docs §7-5 Layer 1 «inicio de sesión por QR dispositivo-a-dispositivo»:

1. El CLI crea una sesión y **renderiza un QR en el terminal + imprime un código corto + URL**
2. Escanea el QR con tu móvil (o abre la URL en un navegador donde ya tengas sesión iniciada en EveryAPI) — la URL en el QR ya lleva `?code=USR-789`, el dashboard rellena el código automáticamente y el usuario solo necesita hacer clic en Approve
3. El CLI recibe el access token y lo guarda en `~/.config/everyapi/credentials.json` (mode 0600)

```bash
everyapi login                                    # producción; renderiza QR + abre el navegador por defecto
everyapi login --api-base http://localhost:8787   # dev local / self-hosted
everyapi login --no-browser                       # no abrir el navegador automáticamente (escanea el QR)
everyapi login --no-qr                            # no renderizar el QR (terminales que no soportan UTF-8 / piping)
```

Ejemplo de renderizado de QR en el terminal (caracteres Unicode de medio bloque; aprox. 18-20 filas de altura):

```
█▀▀▀▀▀█  ▀▀ ▄  █▀▀▀▀▀█
█ ███ █  ▀▄█▀  █ ███ █
█ ▀▀▀ █ ▄ ▀ █▀ █ ▀▀▀ █
▀▀▀▀▀▀▀ █▄█▄█▄ ▀▀▀▀▀▀▀
... (el QR real codifica verification_uri?code=USR-789)
```

Por qué esto es una ruta anti-phishing más fuerte:

- El usuario **no introduce una contraseña en el nuevo dispositivo** → ningún sitio de phishing puede capturar las credenciales
- El usuario **no es redirigido a una página de navegador desconocida** → desaparece la superficie de phishing por redirección web
- Incluso si el CLI es un fork malicioso que genera un QR falso, la página de confirmación tras escanear es el dashboard real de everyapi.ai (disparado desde un dispositivo donde el usuario ya tiene sesión iniciada), y un código desconocido no es algo que un usuario apruebe

Las demás capas de docs §7-5 (cert pinning / phrase string / PKCE OAuth) están implementadas en PRs independientes (cert pinning es report-only; enforce fue una decisión de producto de no enviarlo).

### `everyapi seller <sub>` — subcomandos del lado vendedor del marketplace

Traslada al terminal los flujos de mount de channels y retiros del dashboard para hacer scripted onboarding. Antes de montar un channel, `seller setup` chequea elegibilidad (cuenta activa / email verificado / antigüedad de cuenta / historial de gasto / tope de channels), y los gates fallidos se listan **antes de que el usuario escriba una key** para no enterarse vía un 422 después del submit.

```bash
everyapi seller list                          # listar channels montados
everyapi seller withdraw                      # mover todas las ganancias seller pending al saldo principal
everyapi seller withdraw --quota 1000         # transferencia parcial (unidades de DB)
everyapi seller add-key   --type claude --name 'my-pro' \
                        --key 'sk-ant-...' --models 'claude-3-opus,claude-3-sonnet'
everyapi seller add-oauth codex --name 'my-chatgpt' --models 'gpt-4'
                                            # OAuth de un clic: el CLI arranca un device flow, el usuario
                                            # introduce el user_code en el navegador, el token aterriza en el channel
everyapi seller add-oauth claude --name 'my-claude' --models 'claude-3-opus,claude-3-sonnet'
                                            # paste flow: el CLI abre la página de autorización de Anthropic; el usuario
                                            # pega el code#state mostrado por el callback de vuelta al terminal
everyapi seller add-oauth gemini --name 'my-gemini' --models 'gemini-1.5-pro'
                                            # loopback de un clic real: el CLI inicia un listener en puerto aleatorio,
                                            # Google envía el code directamente al CLI — sin pegar nada
everyapi seller setup                         # wizard interactivo: chequea elegibilidad primero, luego guía a add-key
```

#### `add-key` — pool de keys de respaldo

`--key` se puede repetir para montar N credenciales equivalentes en el mismo channel como pool de respaldo (B2, PRODUCT §4.5); cuando la key primaria devuelve 401/403, el backend hace failover automáticamente a la siguiente. `--key-remark` también puede repetirse, alineado posicionalmente con `--key` (la i-ésima `--key-remark` es la etiqueta de la i-ésima `--key`, para identificar después en el dashboard). Los blobs OAuth no pueden ir al pool de respaldo — solo siguen siendo channels de key única.

```
everyapi seller add-key   --type claude --name 'claude-pool' \
                        --models 'claude-3-opus' \
                        --key 'sk-ant-primary' --key-remark 'primary' \
                        --key 'sk-ant-backup'  --key-remark 'team backup'
```

`--type` de `add-key` acepta alias (`openai` / `claude` / `gemini` / `codex` / `vertex` / `aws` / `xai` / `deepseek`) o un id numérico. El mount está sujeto a elegibilidad del marketplace (cuenta activa, email verificado, historial de gasto, tope de channels), y el CLI muestra el checklist de fallos en los tres puntos de entrada (`add-key` / `add-oauth` / `setup`) antes de hacer nada más.

#### `add-oauth codex` — OAuth de un clic (device flow)

`everyapi seller add-oauth codex --name 'my-chatgpt' --models 'gpt-4'` ejecuta el device authorization flow estilo RFC 8628 de Codex / ChatGPT — el seller **nunca toca el string del token**:

1. El CLI llama a `/api/seller/codex/device/start` y recibe un `user_code` corto y un `verification_uri`
2. El CLI abre el navegador por defecto a `https://auth.openai.com/codex/device` (saltar con `--no-browser`); el usuario introduce el `user_code` en el navegador para completar la autorización
3. El CLI hace polling a `/api/seller/codex/device/poll`; una vez autorizado, el backend crea el channel y escribe el token OAuth en el campo `key` del channel
4. Salida: id de channel + el email de ChatGPT enlazado

Las cookies de autorización son gestionadas por un `http.CookieJar` en-proceso (no persistidas) — el state del device flow es corto y atado al proceso, alineado con el modelo de amenazas.

#### `add-oauth claude` — OAuth paste-and-submit

`everyapi seller add-oauth claude --name … --models …`. El provider OAuth de Anthropic tiene hardcodeado `redirect_uri` a `https://console.anthropic.com/oauth/code/callback` en su lado, así que el CLI no puede usar un listener localhost para recibir el callback. Flujo:

1. El CLI llama a `/api/seller/claude/oauth/start`; el backend crea el par PKCE + state y devuelve la URL authorize de Anthropic
2. El CLI abre el navegador por defecto (saltar con `--no-browser`); el usuario se autentica en Anthropic y aprueba
3. Anthropic redirige a su página de callback que muestra un string `<code>#<state>`
4. **El usuario copia ese string de vuelta al CLI**
5. El CLI llama a `/api/seller/claude/oauth/complete`; el backend intercambia code+verifier por el token y monta el channel

Un paso extra de pegado vs el device flow, pero aún mucho más fácil que buscar manualmente `~/.claude/auth.json`. La cookie de sesión la emite el backend en start; complete debe llegar a la misma sesión — el `http.CookieJar` del CLI es en-proceso y aislado por invocación.

#### `add-oauth gemini` — OAuth loopback de un clic real

`everyapi seller add-oauth gemini --name … --models … [--no-browser] [--timeout 5m]`. El cliente OAuth installed-app de gemini-cli de Google acepta `http://127.0.0.1:<port>/callback` como `redirect_uri`, así que **el CLI ejecuta su propio listener para el callback** — el usuario se autentica en el navegador y no necesita pegar nada. Flujo:

1. El CLI inicia un listener HTTP de un solo uso en un puerto efímero aleatorio (`127.0.0.1:0`), path fijo `/callback`
2. El CLI llama a `/api/seller/gemini/oauth/start` con `redirect_uri = http://127.0.0.1:<port>/callback`; el backend valida estrictamente el redirect: loopback / port ≥ 1024 / scheme=http / path=/callback / sin query/fragment/userinfo (previene SSRF + redirect hijacking)
3. El CLI abre el navegador por defecto; el usuario se autentica en Google y consiente
4. Google redirige con `?code=…&state=…` al listener del CLI
5. El CLI verifica que el state coincida (previene flujos stale / forgery) y llama a `/api/seller/gemini/oauth/complete`
6. El backend intercambia code + el mismo redirect_uri por el token y monta el channel

Comparación con los otros dos providers:

| Provider | UX | Razón |
|---|---|---|
| `codex` | El usuario teclea un user_code de 6 dígitos en el navegador; el CLI auto-pollea | Device flow de OpenAI, sin redirect_uri |
| `claude` | El usuario se autentica en el navegador, copia `code#state` de vuelta al CLI | Anthropic hardcodea redirect_uri a su propia URL de callback |
| `gemini` | El usuario se autentica en el navegador, cierra la pestaña, listo | Google acepta loopback redirects |

`--timeout` acota la espera (5 minutos por defecto). Al timeout, el CLI sale y cierra el listener limpiamente.

### `everyapi edge <sub>` — despliegue de un comando del supplier agent BYO-GPU

Onboarding de GPUs ociosas para vender compute a través de EveryAPI. El CLI condensa el despliegue a 8 subcomandos, ahorrando a los suppliers copiar a mano docker-compose, rellenar `.env`, o mover el registration token:

```bash
everyapi login                              # reutiliza el login existente
everyapi edge register --name "rtx-4090"    # llama a /api/seller/edge/nodes para node_id + token, escribe en ~/.local/share/everyapi/edge/<id>/
everyapi edge start                         # auto-detecta NVIDIA / ROCm / Apple Silicon / CPU, docker compose up -d
everyapi edge models pull llama3.1:8b       # docker compose exec ollama ollama pull ...
everyapi edge status                        # docker compose ps local + dashboard online/offline
everyapi edge logs -f                       # seguir logs
everyapi edge update                        # docker compose pull && up -d
everyapi edge remove                        # down -v + borrar dir local + DELETE en backend
```

`start` renderiza `docker-compose.yml` en runtime vía `text/template` (**no desde YAML estático embebido**) — esto permite que los nombres de containers se namespacen por node_id para que múltiples nodes en un mismo host no colisionen, y el GPU passthrough se renderiza condicionalmente por mode (NVIDIA = `deploy.resources.devices` + driver nvidia; ROCm = `/dev/kfd` + `/dev/dri` + `group_add: video`; macOS = sin container ollama, el agent se conecta al ollama nativo del host vía `host.docker.internal`).

Flujo de credenciales: el CLI usa un Bearer `sk-everyapi-` existente para llamar a `POST /api/seller/edge/nodes` → el backend devuelve el `registration_token` una vez (después el backend solo almacena el sha256, nunca lo vuelve a mostrar) → el CLI lo escribe 0600 a `~/.local/share/everyapi/edge/<id>/node.json` → se renderiza en la env `EVERYAPI_REGISTRATION_TOKEN` del compose. **El registration token nunca se escribe a ningún archivo .env** (para que los suppliers no lo committeen por error).

Requisitos: `docker` + `docker compose v2` (v1 está EOL y no se soporta). En macOS, `brew install ollama && brew services start ollama` (la aceleración Metal no funciona dentro de un container docker).

### `everyapi topup` — redirect de recarga con phrase anti-phishing

`everyapi topup` abre la página de recarga del dashboard. Antes de redirigir, pasa por una verificación docs §7-5 Layer 3:

1. El CLI llama al backend `POST /api/cli/jump-session` y recibe un session id + una phrase de 4 emojis (p. ej. `🌊 🦊 🍕 🚀`)
2. El CLI imprime tanto la URL como la phrase al terminal, diciendo al usuario «la misma phrase debería aparecer en la parte superior de la página en un momento»
3. El usuario pulsa Enter; el CLI abre la URL en el navegador del sistema (con `?jump_session=<id>`)
4. Al cargar el dashboard, llama al backend `GET /api/cli/jump-session/:id/phrase`, recibe la misma phrase, y **la muestra prominentemente en el header de la página**
5. El usuario hace comparación visual: si la phrase coincide → EveryAPI genuino; si no coincide o no se muestra → cierra la pestaña, posible phishing

Por qué esto bloquea el phishing: la phrase vive en memoria del backend con clave en un session id aleatorio de 32 hex; un sitio de phishing no tiene path de auth para fetcharla, y un `wallet/topup?jump_session=<id>` forjado tampoco puede leerla. TTL corto (10 min) + single-use (la sesión se borra después de que el dashboard la fetchee una vez) limitan aún más el riesgo de reutilización.

```bash
everyapi topup                    # abre el navegador por defecto
everyapi topup --no-browser       # solo imprime la URL, copia manual
```

### `everyapi status` — saldo / uso / cuota actuales

```
$ everyapi status

  alice (alice@example.com)
  quota:     $12.34 remaining   $5.67 used
  requests:  1,234
  topup:     https://app.everyapi.ai/wallet
```

### `everyapi update` — ejecuta automáticamente el comando de actualización de brew

Comprueba la última release en el mirror de GitHub, la compara con la versión actual y **ejecuta automáticamente `brew update && brew upgrade everyapi`** — un comando, sin copy-paste.

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

¿Por qué no reemplazar el binario directamente? La cadena de verificación propia de Homebrew (SHA / bottle signing) es más sólida que cualquier cosa que reconstruyamos dentro del CLI, y auto-reemplazar un executable en ejecución es un campo minado en la plataforma Windows.

Flags:
- `--check` — comparación silenciosa; exit 0 si está al día, exit 1 si está desactualizado. Para CI / cron:
  ```bash
  everyapi update --check || echo "needs upgrade"
  ```
- `--dry-run` — imprime el comando que se ejecutaría pero no lo ejecuta (para inspección)

### `everyapi settings` — preferencias del CLI (idioma, etc.)

El CLI viene con i18n en 7 idiomas: inglés, chino simplificado, japonés, coreano, español, alemán, francés — los strings del CLI se renderizan en el idioma elegido. Los errores de la API backend se auto-negocian vía el header `Accept-Language` y cubren 8 idiomas — los 7 anteriores más chino tradicional.

```bash
$ everyapi settings                          # picker interactivo: elige un idioma
$ everyapi settings list                     # ver la configuración actual
$ everyapi settings set language zh          # setear directamente
$ everyapi settings set language fr          # francés igual
$ everyapi settings reset                    # resetear a default (en + auto-detección de LANG)
```

**Auto-detección**: si no has seteado explícitamente nada, el CLI lee las env vars en el orden `EVERYAPI_LANG > LC_ALL > LC_MESSAGES > LANG` al arrancar. Un locale del sistema `zh_CN.UTF-8` / `ja_JP.UTF-8` / `fr_FR.UTF-8` etc. surte efecto inmediatamente — cero configuración.

**Override puntual**:

```bash
EVERYAPI_LANG=zh everyapi status             # esta invocación se muestra en chino; no se persiste
```

**Ejemplo de traducción** (error not-logged-in, 7 idiomas × misma línea):

```
en : Error: not logged in — run 'everyapi login' first
zh : 错误: 未登录 — 先运行 'everyapi login'
ja : エラー: ログインしていません — まず 'everyapi login' を実行してください
ko : 오류: 로그인되어 있지 않습니다 — 먼저 'everyapi login' 을 실행하세요
es : Error: no has iniciado sesión — ejecuta primero 'everyapi login'
de : Fehler: nicht angemeldet — führe zuerst 'everyapi login' aus
fr : Erreur: non connecté — exécutez d'abord 'everyapi login'
```

Las settings viven en `~/.config/everyapi/settings.json` (mismo directorio que `credentials.json`, pero modo `0644` — sin secretos).

**Para mejorar traducciones o añadir un idioma**: ver [`internal/i18n/locales/README.md`](internal/i18n/locales/README.md).

## Archivos de configuración

Las credenciales viven en `~/.config/everyapi/credentials.json` (o `$XDG_CONFIG_HOME/everyapi/` si `$XDG_CONFIG_HOME` está seteado), modo `0600`. Las escribe `everyapi login` y las lee cualquier otro comando.

> ⚠️ **Los tokens se almacenan en texto plano**. El modo `0600` + la ruta privada de `$HOME` coincide con la convención de CLIs industriales como `gh auth` / `aws configure`, pero **para modelos de amenaza de robo de máquina doméstica / malware**, cualquier proceso que pueda leer este archivo puede llamar a la API de EveryAPI como tú (incluyendo los tools MCP — ver §money-path friction step más abajo). Recomendado:
> - No hacer `everyapi login` en máquinas compartidas / públicas
> - Usuarios macOS: considerar `everyapi logout` antes de activar FileVault
> - Usuarios Linux: activar cifrado de home-dir (`ecryptfs` / LUKS)
> - Si sospechas filtración → `everyapi logout` borra las credenciales locales inmediatamente, y rota el API key desde el dashboard de EveryAPI
>
> Un backend de keychain de plataforma (macOS Keychain / Windows DPAPI / Linux Secret Service) está planeado pero no enviado.

Campos:

- `api_base` — la URL de la pasarela EveryAPI. Por defecto `https://api.everyapi.ai`. Usuarios self-hosted / dev local pueden sobreescribir con `--api-base` en `login`.
- `access_token` — bearer usado por cada llamada autenticada a la API.
- `relay_key` — relay API key (`sk-everyapi-…`) usada para el env del subproceso de `everyapi use`. Se fetchea desde `/api/token/*` y se cachea aquí.
- `user_id` / `username` — cacheados para que `status` pueda renderizar la línea de identidad antes del primer roundtrip de API.

## Desarrollo

En el directorio source del CLI (el que contiene este README, `go.mod` y `Makefile`):

```bash
go test ./...
go run . status            # contra producción
go run . login --api-base http://localhost:8787   # contra backend local
```

Compilación cruzada local para todas las plataformas (misma receta que CI):

```bash
make cli-release           # artefactos en dist/ (5 plataformas × 1 binario = 5 archivos)
```

## Servidor MCP (subcomando `everyapi mcp`)

El binario `everyapi` **incluye un** servidor [Model Context Protocol](https://modelcontextprotocol.io) integrado — expuesto como subcomando (`everyapi mcp` lee stdin y escribe stdout). Los agentes IA (Claude Code / Cursor / Codex CLI / cualquier cliente MCP) pueden invocarlo directamente, **sin que el usuario abra un terminal**.

> ⚠️ **Modelo de auth y superficie de exposición del servidor MCP**
>
> - **Sin puertos abiertos**: `everyapi mcp` es JSON-RPC pura sobre stdio, forkado por el host CLI. **No escucha en ningún socket / TCP port** — sin superficie de red.
> - **Lee `~/.config/everyapi/credentials.json` directamente**: el servidor MCP no tiene flujo de auth propio; poder leer el archivo de credenciales = poder llamar a cada tool expuesto como tú. Cualquier host MCP que pueda ejecutar un proceso como tu user tiene acceso completo.
> - **La ruta de dinero `everyapi_seller_withdraw` tiene un paso de fricción**: los llamadores deben pasar `confirm: "yes"`, asegurando que el agente IA superficie la acción de transferencia en la UI a un humano, evitando un silent drain. Otros tools de solo lectura (status / topup / seller_list) no tienen este requisito.
>
> No instales hosts MCP en los que no confíes.

### Instalación

Mismo binario que el CLI — instalar el CLI te da el servidor MCP:

```bash
make cli                                              # build local, produce ./bin/everyapi
# o vía go install:
go install github.com/everyapi-ai/everyapi-ai@latest
```

### Conectar a Claude Code

Añadir a `~/.claude/settings.json`:

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

Conectar a Cursor, Codex CLI u otros clientes MCP es similar — apunta `command` al binario `everyapi` con `args: ["mcp"]`.

### Prerrequisito de auth

Debes haber ejecutado `everyapi login` en un terminal al menos una vez — el servidor MCP es un proceso en background sin capacidad de interacción con terminal, así que no puede ejecutar el flujo device-code por sí mismo. Lee `~/.config/everyapi/credentials.json` directamente; si falta, cada tool devuelve un mensaje `isError: true` «not logged in» guiando al usuario para que inicie sesión.

### Tools expuestos en v1 (8 en total)

| Tool | Entrada | Función |
|---|---|---|
| `everyapi_status` | ninguna | Saldo actual / usado / número de requests |
| `everyapi_topup` | ninguna | Devuelve la URL web de recarga |
| `everyapi_seller_list` | ninguna | Lista los seller channels del marketplace |
| `everyapi_seller_withdraw` | `{confirm: "yes", quota?: int}` | Transfiere seller_quota al saldo principal; **`confirm: "yes"` requerido** (fricción de ruta de dinero) |
| `everyapi_seller_add_oauth_codex_start` | `{name, models}` | Inicia el flujo de autorización de dispositivo Codex / ChatGPT; devuelve `user_code` + `verification_uri` + `flow_id` |
| `everyapi_seller_add_oauth_codex_poll` | `{flow_id}` | Consulta el estado de autorización de Codex. `pending`/`slow_down` seguir polleando; `authorized` devuelve el id de channel; `expired`/`denied` terminan |
| `everyapi_seller_add_oauth_claude_start` | `{name, models}` | Inicia el flujo OAuth de Anthropic; devuelve `authorize_url`. Después de que el usuario se autentique en el navegador, recibe un string `<code>#<state>` |
| `everyapi_seller_add_oauth_claude_complete` | `{input}` | Envía el string `<code>#<state>` que el usuario pegó en el paso anterior; monta el channel |

**Patrón de uso de tool OAuth** (cómo un agente IA conduce esto en una conversación):

```
User: Añade un seller channel de ChatGPT Plus para mí, ponle de nombre my-chatgpt, models gpt-4
AI    → everyapi_seller_add_oauth_codex_start({name: "my-chatgpt", models: "gpt-4"})
       ← "Ve a chatgpt.com/codex, mete USR-789, después avísame cuando hayas terminado"
User: Hecho en el navegador
AI    → everyapi_seller_add_oauth_codex_poll({flow_id: "..."})
       ← "status=pending, espera unos segundos más"
[seguir polleando hasta authorized]
       ← "status=authorized — channel #314 mounted"

User: Añade también el de Claude Pro, my-claude / claude-3-opus
AI    → everyapi_seller_add_oauth_claude_start({...})
       ← "Ve a [URL] a completar la autorización, después dame el string code#state"
User: code-abc123#state-xyz
AI    → everyapi_seller_add_oauth_claude_complete({input: "code-abc123#state-xyz"})
       ← "Channel #315 mounted"
```

Gemini OAuth (flujo loopback) **no está expuesto vía MCP** — el ciclo de vida del listener loopback no coincide con el ciclo de vida cross-tool-call. Gemini sigue yendo a través del CLI `everyapi seller add-oauth gemini`.

### Smoke test manual

```bash
make cli
./bin/everyapi mcp <<'EOF'
{"jsonrpc":"2.0","id":1,"method":"initialize"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"everyapi_status","arguments":{}}}
EOF
```

Deberías ver tres líneas de respuesta JSON: resultado de initialize, lista de 4 tools, texto de status (o un isError de not-logged-in).

## Lo que este binario AÚN no incluye

Todavía **sin implementar** (ordenados por importancia; releases posteriores los añadirán incrementalmente sin romper la superficie v1):

- ⚠️ Code signing a nivel de SO (notarización macOS / Authenticode Windows) — por ahora confiamos en la verificación de doble capa sigstore cosign keyless + SHA256SUMS; ambos se incluyen en cada GitHub Release y Homebrew los verifica automáticamente al instalar
- ❌ Backend de keychain de plataforma — los tokens siguen en texto plano en disco (mode 0600)

Previamente listados aquí pero **ya enviados** (no los trates como sin implementar):

- ✅ Sanitizer proxy local — el comando es `everyapi proxy {start,stop,status,configure}` (no `everyapi start`/`everyapi configure`); motor + 6 detectores integrados + regex personalizados + integrado en `everyapi use`
- ✅ Seller OAuth onboarding para los tres providers (codex device / claude paste / gemini loopback)
- ✅ Ruta principal de QR sign-in — `login` usa device-code **+ QR como ruta principal**, con `--no-qr` como fallback
- ✅ Capas anti-phishing — phrase string (`everyapi topup`), strict-check de PKCE/state, y cert pinning están todas en su sitio; cert pinning es **report-only** (silencio en match / alerta en mismatch / nunca rechaza conexión), con la decisión de producto de «solo alerta, no enforce»

## Reportar vulnerabilidades

Ver [`SECURITY.md`](../SECURITY.md).
