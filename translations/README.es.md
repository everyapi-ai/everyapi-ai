> 🌐 [English](../README.md) · [简体中文](README.zh-CN.md) · [日本語](README.ja.md) · [한국어](README.ko.md) · **Español** · [Deutsch](README.de.md) · [Français](README.fr.md)

# CLI `everyapi`

CLI de onboarding para compradores de la pasarela de API de IA [EveryAPI](https://everyapi.ai). Pone en marcha cualquier agente de programación compatible **en menos de un minuto** a través de un único registro auditado.

Estado: **flujos principales publicados** —— onboarding de compradores, comandos de vendedor (clave plana + OAuth de tres proveedores), proxy de saneamiento, inicio de sesión por QR como ruta principal y capas antiphishing están todos presentes. Lo único que falta es la firma de código a nivel de sistema operativo y los backends de keychain de plataforma (ver "Lo que este binario todavía NO incluye" al final).

## Instalación

**macOS (Homebrew):**

```bash
brew tap everyapi-ai/tap && brew install everyapi
```

Para actualizar después, ejecuta primero `brew update` (si no lo haces, `brew upgrade everyapi` usa la fórmula en caché y dirá "already installed" aunque exista una versión nueva):

```bash
brew update && brew upgrade everyapi
```

**Linux / macOS (script de instalación):**

```bash
curl -fsSL https://dl.everyapi.ai/install.sh | bash
```

El script detecta el sistema operativo y la arquitectura, descarga el `everyapi_{os}_{arch}.tar.gz` correspondiente, verifica su SHA256 y lo instala en `~/.local/bin` (o `/usr/local/bin` si lo ejecutas como root). Si tienes [cosign](https://github.com/sigstore/cosign) instalado, también verifica la firma keyless —— pasa `--require-signature` para hacer obligatorio ese paso (recomendado en CI o entornos sensibles a la cadena de suministro).

Una sola línea en todo el mundo: el script elige el origen de descarga en tiempo de ejecución —— GitHub Releases si es accesible, un espejo en China continental cuando GitHub va lento o está bloqueado —— así que el mismo comando funciona dentro y fuera de China. Define `EVERYAPI_DOWNLOAD_BASE` para forzar un espejo concreto.

Opciones habituales:

```bash
curl -fsSL https://dl.everyapi.ai/install.sh | bash -s -- --version v0.2.2     # fijar una versión
curl -fsSL https://dl.everyapi.ai/install.sh | bash -s -- --prefix /usr/local  # elegir el prefijo
curl -fsSL https://dl.everyapi.ai/install.sh | bash -s -- --require-signature  # abortar si falla cosign
curl -fsSL https://dl.everyapi.ai/install.sh | bash -s -- --force              # reinstalar la misma versión
```

Para actualizar más adelante, vuelve a ejecutar el mismo comando: el script resuelve la última etiqueta de release y reemplaza el binario in situ si hay una versión más nueva. Si ya estás en la versión objetivo, termina con `already at vX.Y.Z — nothing to do`, así que es seguro incluirlo en scripts de aprovisionamiento o dotfiles. Pasa `--force` para reinstalar por encima (útil para comprobar la integridad o recuperar un archivo corrupto). El propio script está publicado en este repositorio en [`install.sh`](../install.sh), por si prefieres descargarlo y leerlo antes de ejecutarlo.

**Usuarios de Go (`go install`):**

```bash
go install github.com/everyapi-ai/everyapi-ai/v3@latest
```

**Windows (PowerShell):**

```powershell
irm https://dl.everyapi.ai/install.ps1 | iex
```

Mismo flujo que el script de shell: resuelve la última etiqueta, descarga `everyapi_windows_amd64.zip` + `SHA256SUMS`, verifica el hash (y la firma si cosign está en el `PATH`), instala `everyapi.exe` en `%LOCALAPPDATA%\everyapi\bin` y lo añade al `PATH` del usuario. Para fijar una versión u otras opciones, materializa primero el script: `& ([scriptblock]::Create((irm https://dl.everyapi.ai/install.ps1))) -Version v0.2.2`. El script también vive en el repositorio, en [`install.ps1`](../install.ps1).

**Windows (manual):** descarga `everyapi_windows_amd64.zip` (o cualquier otro artefacto) desde la [página de releases](https://github.com/everyapi-ai/everyapi-ai/releases/latest), verifícalo contra `SHA256SUMS` y coloca el binario en tu `%PATH%`.

## Comandos

Ejecutar `everyapi` sin argumentos en un TTY abre un lanzador interactivo sobre este mismo conjunto; `everyapi help` lo imprime como texto.

| Comando | Para qué sirve |
|---|---|
| `everyapi auth <sub>` | Inicia y cierra sesión, y muestra el estado de la sesión (`login` / `logout` / `status`) |
| `everyapi wallet <sub>` | Recarga (con verificación de frase antiphishing), historial de pagos, métodos de pago |
| `everyapi checkin <sub>` | Reclama la cuota diaria de hoy; muestra el calendario de este mes |
| `everyapi account <sub>` | Perfil, 2FA, código de afiliado, planes de suscripción |
| `everyapi use <tool>` | Configura el entorno y ejecuta un CLI de terceros apuntando a EveryAPI |
| `everyapi token <sub>` | Gestiona las claves de relay API (list / create / key / revoke / switch / …) |
| `everyapi models <sub>` | Catálogo de modelos: list / pricing / groups |
| `everyapi stats <sub>` | Uso, registro de peticiones, rendimiento por modelo, salud de los upstream |
| `everyapi market <sub>` | Publicaciones de demanda, disputas, informes de abuso |
| `everyapi inbox <sub>` | Notificaciones dentro de la app y mensajes directos |
| `everyapi seller <sub>` | Comandos de vendedor del marketplace (list / setup / withdraw / add-key / add-oauth) |
| `everyapi edge <sub>` | Despliegue en un comando del agente proveedor BYO-GPU (register / start / status / logs / models / rename / pause / resume / stop / update / remove) |
| `everyapi artifacts <sub>` | Publica y gestiona informes HTML autocontenidos (`share` / `list` / `update` / `delete`) |
| `everyapi events` | Suscríbete al flujo de eventos en vivo (SSE) |
| `everyapi feedback` | Envía un informe de error o una petición de función al equipo |
| `everyapi proxy <sub>` | Proxy sanitizador local (`start` / `stop` / `status` / `configure`) |
| `everyapi computer <sub>` | Lee y controla ventanas de apps macOS locales a través de Accesibilidad |
| `everyapi mcp` | Ejecuta como servidor MCP (JSON-RPC por stdin/stdout) |
| `everyapi doctor` | Autodiagnóstico: credenciales, pasarela, sanitizador, herramientas instaladas |
| `everyapi settings <sub>` | Consulta y cambia las preferencias del CLI (idioma, modo de terminal) |
| `everyapi admin` | Consola de operador —— visible solo para cuentas admin |
| `everyapi version [update\|uninstall]` | Versión de compilación; comprueba y ejecuta la actualización; desinstala el CLI |
| `everyapi help` | Imprime la lista completa de comandos |

### `everyapi computer <sub>` —— computer use local en macOS

En macOS el CLI puede descubrir las apps y ventanas en ejecución, devolver una instantánea acotada de Accesibilidad y ejecutar acciones semánticas o por coordenadas. Esta superficie es solo local y no se registra en `everyapi mcp`. Las compilaciones de Linux y Windows devuelven `unsupported_platform` de forma explícita.

En macOS, `everyapi computer` controla una pequeña app auxiliar firmada de forma independiente (`EveryAPI Computer Use.app`, compilada desde `clients/desktop/native/computer-use-macos`) mediante un socket Unix local, y la descarga y lanza automáticamente en el primer uso si no está instalada —incluso cuando EveryAPI Connect ya instaló su propia copia empaquetada, que este CLI reutiliza en lugar de descargar una segunda. El auxiliar informa que no admite capturas de pantalla porque macOS no expone a través de este proveedor un identificador público y fiable de captura por ventana; nunca lo sustituye por una captura de región de pantalla que podría contener otra app superpuesta.

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

Ejecuta `everyapi computer permissions --json` y concede Accesibilidad a **EveryAPI Computer Use** —no a `everyapi`, `osascript` ni a tu terminal— en Ajustes del Sistema > Privacidad y seguridad > Accesibilidad. Como el auxiliar es su propia app firmada con su propia identidad de paquete, ese permiso queda acotado a esta única capacidad: no autoriza además cualquier script AppleScript o JXA de la máquina, y sobrevive a las actualizaciones del CLI y del auxiliar. `permissions` informa Accesibilidad directamente y Automatización como `unknown`, ya que este proveedor no depende de System Events y no tiene una comprobación previa de Automatización aparte.

Los índices de elementos provienen de la última instantánea de `get-app-state` y caducan a los dos minutos. Las ventanas se seleccionan por índice (`--window-index`), pero internamente se identifican por el id real por ventana que CoreGraphics asigna en pantalla cuando existe, con reserva a un id sintético del ámbito de la instantánea para ventanas minimizadas; en ambos casos el proveedor usa una huella interna para detectar cambios observables, pero los atributos públicos de Accesibilidad no pueden demostrar que una ventana o control de reemplazo con atributos idénticos sea la misma instancia nativa. La caché guarda únicamente datos opacos de aplicación, proceso, ventana, ruta, role, frame, nombre de acción y huella en `~/.config/everyapi/computer-use/state/` con permisos privados. Obtén una instantánea nueva tras `app_stale`, `element_stale` o `window_stale`. Una acción de GUI exitosa sigue siendo exitosa aunque falle su refresco de estado de mejor esfuerzo; el JSON incluye entonces `refreshError` en lugar de devolver un error de acción reintentable. Si la llamada al auxiliar se interrumpe o devuelve un recibo inválido después de entregar la acción, `action_outcome_unknown` significa que la acción puede haber ocurrido ya; refresca el estado antes de decidir si reintentas.

Una lista mantenida de apps de terminal conocidas, gestores de contraseñas, Acceso a Llaveros, Contraseñas, Ajustes del Sistema y EveryAPI Connect se bloquea como fricción de defensa en profundidad. El bloqueo por bundle ID no es un clasificador exhaustivo de aplicaciones: apps no listadas, editores con terminal integrada, navegadores y apps renombradas o recién publicadas pueden exponer capacidades equivalentes. El objetivo explícito de `--app`, el TCC de macOS y la autoridad del mismo usuario que invoca siguen siendo la frontera de confianza real. Al texto observado se le eliminan las secuencias de control de terminal y se le buscan credenciales antes de imprimirlo; el texto escrito o asignado que coincide con los detectores de secretos integrados se rechaza. Prefiere `--text-stdin` y `--value-stdin` para mantener el texto corriente fuera del historial del shell.

### `everyapi use <tool>` —— ejecuta un CLI de terceros apuntando a la pasarela EveryAPI

Es la razón principal para instalar este CLI: configura y lanza un cliente de programación compatible a través de EveryAPI. Las integraciones nativas (`antigravity`, `librefang`) conservan su propia ruta de autenticación y no reciben una clave de relay copiada.

```bash
everyapi use claude            # Claude Code → EveryAPI
everyapi use codex             # OpenAI Codex CLI → EveryAPI
everyapi use opencode          # OpenCode → proveedor EveryAPI con alcance de proceso
everyapi use gemini            # Google Gemini CLI → EveryAPI
everyapi use antigravity       # Antigravity (autenticación y enrutado nativos de Google)
everyapi use aider             # Aider → EveryAPI (con selección de modelo)
everyapi use goose             # Goose CLI → EveryAPI (con selección de modelo)
everyapi use crush             # Crush CLI → catálogo de modelos EveryAPI aislado
everyapi use cline             # Cline CLI → configuración de proveedor ligada al ciclo de vida
everyapi use openclaw          # TUI local de OpenClaw → catálogo EveryAPI aislado
everyapi use continue          # Continue CLI → configuración de asistente aislada
everyapi use kilo              # Kilo Code CLI → configuración de proveedor con alcance de proceso
everyapi use pi                # Agente de programación Pi → catálogo de modelos aislado
everyapi use pi-web            # UI web de Pi → entrada de proveedor en un models.json duradero
everyapi use vibe              # Mistral Vibe → proveedor genérico aislado
everyapi use copilot           # GitHub Copilot CLI → BYOK oficial con alcance de proceso
everyapi use droid             # Factory Droid → ajustes de ejecución aislados
everyapi use openhands         # OpenHands CLI → overrides de entorno explícitos solo para el proceso
everyapi use forge             # ForgeCode → sesión compatible con OpenAI aislada
everyapi use llxprt            # LLxprt Code → home aislado + flags de ejecución fijos
everyapi use grok              # xAI Grok Build → EveryAPI
everyapi use qwen-code         # Alibaba Qwen Code → EveryAPI (con selección de modelo)
everyapi use kimi-code         # Moonshot Kimi Code → EveryAPI (con selección de modelo)
everyapi use hermes            # Nous Research Hermes Agent → EveryAPI (con selección de modelo)
everyapi use librefang         # Lanza LibreFang (proceso nativo de credenciales EveryAPI)
everyapi use open-webui        # Servidor Open WebUI → EveryAPI como su backend OpenAI
everyapi use deepseek-harness  # UI web de DeepSeek Harness (dsh) → proveedor y credencial generados
everyapi use hermes --model gpt-5.1      # fija el modelo y salta el selector
everyapi use claude                      # modo transparente por defecto: se queda en api.anthropic.com
everyapi use codex                       # se queda en api.openai.com
everyapi use antigravity                 # conserva el Origin oficial de Google
everyapi use claude --transparent=false  # desactiva el modo transparente: inyecta base URL de la pasarela + clave de relay
everyapi use                             # sin argumentos → selector interactivo de herramientas instaladas
```

Cada herramienta tiene sus propias convenciones, pero el CLI las recuerda por ti:

| Herramienta | Cómo se conecta a EveryAPI |
|---|---|
| claude | env: `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`; modelos compatibles en vivo mediante descubrimiento en la pasarela |
| codex | env: `OPENAI_API_KEY` más un `CODEX_HOME` persistente de EveryAPI para conservar la sesión, con `--profile` ligado al ciclo de vida y un catálogo de modelos acotado a la clave (codex enruta por configuración, no por `OPENAI_BASE_URL`) |
| gemini | env: `GEMINI_API_KEY`, `GOOGLE_GEMINI_BASE_URL`, `GEMINI_MODEL`; overlay de configuración aislado con modo de autenticación |
| antigravity | lanzador nativo de Antigravity (`agy`) |
| aider | env compatible con OpenAI + espacio de nombres LiteLLM `openai/<model>` |
| goose | `GOOSE_PROVIDER=openai`, `GOOSE_MODEL`, `OPENAI_API_KEY`, `OPENAI_BASE_URL` |
| crush | `CRUSH_GLOBAL_CONFIG` con alcance de proceso; la clave se referencia por entorno y el catálogo de modelos se genera en vivo |
| cline | `CLINE_PROVIDER_SETTINGS_PATH` ligado al ciclo de vida, eliminado al salir |
| openclaw | TUI local integrada con configuración de alcance de proceso y SecretRef basado en entorno |
| continue | `CONTINUE_GLOBAL_DIR/config.yaml` ligado al ciclo de vida; las referencias de secretos de Continue son de entorno |
| kilo | `KILO_CONFIG_CONTENT` con alcance de proceso; proveedor compatible con OpenCode y clave por entorno |
| pi | `PI_CODING_AGENT_DIR` aislado con `models.json` y la configuración del modelo elegido. `{extensions,skills,prompts,themes}` que ya existieran en tu `PI_CODING_AGENT_DIR` (por defecto `~/.pi/agent`) antes del lanzamiento se cargan por ruta absoluta |
| pi-web | `providers.everyapi` fusionado en el `PI_CODING_AGENT_DIR/models.json` *duradero* (por defecto `~/.pi/agent`), de modo que las sesiones, la confianza de proyecto, el modelo elegido y las propias ediciones del panel Models sobreviven; la clave de relay sigue siendo una referencia de entorno y nunca se escribe en disco |
| vibe | `VIBE_HOME/config.toml` aislado; proveedor genérico con `api_key_env_var` |
| copilot | entorno BYOK oficial `COPILOT_PROVIDER_*`; la API de transporte sigue las capacidades del modelo elegido |
| droid | archivo oficial `--settings` solo para la ejecución, con un único modelo `custom:EveryAPI-0` y clave por entorno |
| openhands | `--override-with-envs` con `LLM_API_KEY`, `LLM_BASE_URL` y `LLM_MODEL` solo para el proceso |
| forge | `FORGE_CONFIG` aislado; fija proveedor/modelo compatibles con OpenAI en la configuración y en el entorno del proceso |
| llxprt | home de aplicación aislado y flags de ejecución reservados `--provider openai`, `--baseurl`, `--model` |
| grok | env: `XAI_API_KEY`, `GROK_MODELS_BASE_URL`; `GROK_HOME` aislado; descubrimiento de modelos en vivo filtrado |
| qwen-code | env: `OPENAI_API_KEY`, `OPENAI_BASE_URL`, `OPENAI_MODEL`; ajustes de usuario en un `QWEN_HOME` con alcance de proceso y `--auth-type=openai` fijo |
| kimi-code | env: `KIMI_MODEL_API_KEY`, `KIMI_MODEL_BASE_URL`, `KIMI_MODEL_PROVIDER_TYPE`, `KIMI_MODEL_NAME`; `KIMI_CODE_HOME` aislado con alias de modelo generados |
| hermes | `HERMES_HOME/config.yaml` generado con un proveedor personalizado con nombre, `base_url` y `api_key` en línea; descubrimiento de modelos en vivo filtrado |
| librefang | `librefang start` nativo. Deja el demonio en segundo plano y devuelve tu terminal (usa `librefang stop` para pararlo). LibreFang resuelve tus credenciales EveryAPI actuales en cada petición |
| open-webui | se lanza como `open-webui serve` con `OPENAI_API_BASE_URLS`, `OPENAI_API_KEYS` y `ENABLE_PERSISTENT_CONFIG=false`, de modo que el entorno del proceso gana sobre cualquier configuración guardada; `DATA_DIR` fijado a `~/.open-webui` |
| deepseek-harness | la UI oficial `dsh web`; una entrada `llm-pi-ai.providers.everyapi` generada en `$DSH_HOME/settings.yaml` (por defecto `~/.dsh`, modo `0700`) más un `.credentials.yaml` con modo `0600` que guarda la clave |

Qué variable lee cada herramienta, si hay que añadir `/v1`, qué estilo de cabecera de autenticación usa: ya no tienes que buscarlo.

**Selección de clave de relay**: sin `--group`, el CLI resuelve la clave auto-group de tu cuenta —— la única clave que enruta a todos los grupos accesibles —— y la cachea en `credentials.json`. Las cuentas sin clave auto (o los tiers que dejan de tener acceso a ese grupo) recurren a la clave habilitada más reciente. Usa `everyapi token switch` para fijar otra clave como predeterminada, o pasa `--group <id>` para una sola ejecución contra otro pool. Los overrides de grupo nunca se escriben en esa caché. La clave que uses determina el catálogo de abajo: una clave fijada a un grupo solo verá los modelos de ese grupo.

Una clave cacheada en una ejecución anterior se sigue usando —— esa consulta es deliberadamente offline y no se reelige sola. Si `/model` solo muestra los modelos de un grupo, ejecuta `everyapi token switch` y selecciona `Auto` una vez.

**Selección de modelo**: al lanzar, EveryAPI obtiene el catálogo en vivo disponible para la clave/grupo seleccionados, elimina los protocolos de medios/embedding incompatibles e inyecta esa instantánea en el selector nativo de cada cliente enrutado. Usa `/model` en Claude Code, Codex, Qwen Code y Kimi Code; `/model` o `models` en Grok; `hermes model` en Hermes. Los IDs de modelos que no son de Claude se representan internamente mediante alias compatibles con Claude, pero se muestran y se envían aguas arriba con su ID real.

Las herramientas con contrato `ModelEnv` (Gemini, Aider, Goose, Crush, Cline, OpenClaw, Continue, Kilo, Pi, Vibe, GitHub Copilot CLI, Factory Droid, OpenHands, ForgeCode, LLxprt, Hermes, Qwen Code, Kimi Code) abren el selector de EveryAPI. Pasa `--model <id>` para saltarlo. En ejecuciones no interactivas EveryAPI usa de forma determinista el primer modelo compatible. Claude, codex y grok "puros" conservan su comportamiento de modelo de arranque. `antigravity` lanza el `agy` nativo con autenticación de Google, y `librefang` usa su propio proceso de credenciales EveryAPI. `pi-web`, `open-webui` y `deepseek-harness` sirven interfaces de navegador: EveryAPI registra por adelantado el proveedor y todo el catálogo compatible, y el modelo se elige dentro de esa interfaz en lugar de con un selector de terminal.

**Nivel de razonamiento**: después del modelo, `everyapi use codex` y `everyapi use pi` te preguntan con qué nivel de razonamiento ejecutar y recuerdan la respuesta para las siguientes ejecuciones —— se pregunta una vez y luego se reutiliza sin confirmación, igual que los ajustes de seguridad anteriores. Las condiciones difieren entre ambos clientes porque lo que sabemos difiere. Codex lee los niveles que su catálogo integrado expone para ese modelo (`low` … `ultra`, distintos según el modelo —— `gpt-5.6-sol` llega a `ultra`, `gpt-5.5` a `xhigh`) y recibe la elección como `model_reasoning_effort`. No pregunta a la pasarela para esto, así que el paso no aparece con modelos que Codex desconoce. Pi no tiene una tabla por modelo para proveedores personalizados, así que el paso solo aparece cuando la pasarela confirma que ese modelo acepta effort (`supports_thinking` en `/v1/models`); las opciones van de `off` a `high` y se envían como `defaultThinkingLevel`. Un nivel recordado que el modelo actual no ofrece se descarta en lugar de fijarse. En la primera ejecución tras esta función, el cursor arranca en el effort que ya estuviera en el home persistente de Codex, así que aceptar el valor por defecto no cambia nada. Los controles dentro de la sesión de ambos clientes —— `/model` en Codex, shift+tab en pi —— siguen funcionando; el lanzador solo conserva tu elección entre ejecuciones, porque el perfil generado de Codex y el home aislado de Pi se eliminan al salir.

Los nombres de proveedor no son nombres de CLI: usa `qwen-code` o `kimi-code` para los clientes oficiales de esos proveedores, y elige los modelos del proveedor en el catálogo en vivo de cualquier cliente compatible.

**Aislamiento de configuración de hermes**: `everyapi use hermes` redirige `HERMES_HOME` a un directorio con alcance de proceso bajo `~/.config/everyapi/sessions`. La configuración con credenciales y la URL del proxy en vivo se eliminan al salir y no pueden colisionar con otra clave o grupo. Lo único que persiste es el último ID de modelo elegido, que es una configuración segura. Tu `~/.hermes` personal no se toca. La configuración generada registra EveryAPI como proveedor personalizado con nombre, de forma que `hermes model` pueda descubrir y cambiar de modelo sin caer en OpenRouter. Un `hermes` "puro" abre el chat interactivo; usa `everyapi use hermes -- --tui` si quieres la interfaz de terminal.

**Aislamiento de configuración de grok**: `everyapi use grok` redirige `GROK_HOME` a `~/.config/everyapi/grok-home`. Esto evita que una sesión de navegador xAI cacheada pise tu clave de relay de EveryAPI y separa las sesiones enrutadas por EveryAPI de un `grok` puro. Pasa los flags propios de Grok después de `--`, por ejemplo `everyapi use grok -- --model grok-4.5`.

**Aislamiento de configuración de Qwen/Kimi**: cada ejecución enrutada recibe un home con alcance de proceso bajo `~/.config/everyapi/sessions` que se elimina cuando termina el proceso hijo, así que claves o grupos usados en paralelo no pueden pisarse el catálogo ni la URL de loopback. La configuración real del sistema de Qwen se mantiene intacta y se preservan las precedencias de administrador. Si una configuración de administrador o de workspace define `modelProviders.openai` y ocultaría el catálogo en vivo de EveryAPI, la ejecución falla con un conflicto accionable en lugar de mostrar en silencio modelos obsoletos o incompatibles.

> ⚠️ **Nota de seguridad sobre el entorno del subproceso**: las variables de entorno anteriores contienen tu clave de API de relay. Los CLI de terceros pueden registrar el entorno en modo debug/verbose —— antes de ejecutar `everyapi use`, comprueba que el flag de debug que vas a activar no filtre `*_TOKEN` / `*_API_KEY`. Ejecuta `sed -i 's/sk-everyapi-[A-Za-z0-9]*/REDACTED/g'` sobre los logs de debug antes de compartirlos.

#### Connector transparente (por defecto)

El modo transparente mantiene a los clientes compatibles en el Origin oficial de la API del proveedor en lugar de configurar una base URL de terceros. Es el comportamiento por defecto en todas las herramientas que lo soportan; pasa `--transparent=false` para desactivarlo. El CLI levanta un proxy HTTP CONNECT efímero en un puerto de loopback aleatorio y genera una CA por ejecución cuya clave privada solo existe en memoria. Al proceso hijo solo se le pasan la URL del proxy, el bundle público de la CA y credenciales de marcador de posición no secretas. Las rutas de modelos registradas se descifran localmente y se retransmiten a EveryAPI con tu clave de relay real; el resto de hosts HTTPS usan CONNECT en modo pasarela. Las rutas desconocidas bajo un prefijo de modelo protegido se bloquean, y un fallo de retransmisión nunca cae de vuelta al proveedor.

Está verificado con Claude Code y el Codex CLI, que son también las herramientas donde aplica el valor por defecto. El Antigravity nativo y LibreFang saltan el connector. Las demás herramientas registradas usan sus rutas documentadas de inyección o configuración, así que pasar `--transparent` explícitamente a una herramienta no soportada falla de forma visible.

`--sanitize` no entra en conflicto con el modo transparente, se combina con él: el connector retransmite a través del sanitizador (hijo → connector → sanitizador → pasarela), de modo que el enmascarado y las protecciones de respuesta de recuperación de Claude aplican en ambas rutas de ejecución.

Si tu única variable de proxy es `ALL_PROXY`, el modo transparente se rechaza y se recurre a la ruta de inyección —— la resolución de proxy de Go no lee `ALL_PROXY`, así que el connector no puede respetarla. Define `HTTPS_PROXY` (incluido socks5, con el que net/http conecta de forma nativa) si quieres conservar el modo transparente.

Este modo es experimental y deliberadamente de alcance de proceso:

- El lado cliente que interceptamos habla actualmente HTTP/1.1 y soporta peticiones JSON/SSE normales (las respuestas HTTP/2 de la pasarela se traducen a HTTP/1.1). HTTP/2 del lado cliente, HTTP/3/QUIC, WebSockets, clientes con pinning de certificados y clientes que ignoran `HTTPS_PROXY` quedan fuera de alcance;
- el proveedor OpenAI integrado de Codex prueba una vez el WebSocket de Responses. El connector devuelve HTTP 426, así que Codex cae inmediatamente a HTTPS/SSE sin consumir presupuesto de reintentos; puede que Codex imprima una línea de log de esa prueba fallida;
- Claude Code sigue tratando el marcador de posición no secreto como autenticación por clave de API, así que los connectors de claude.ai se desactivan aunque `ANTHROPIC_BASE_URL` sea el Origin oficial `https://api.anthropic.com`. El modo transparente evita la detección de Origin de terceros; no puede hacer que la autenticación por clave de API parezca un inicio de sesión OAuth de claude.ai;
- no instala ninguna CA del sistema, no requiere privilegios de administrador y no cambia el comportamiento por defecto de `everyapi use`;
- no es indetectable: un cliente puede inspeccionar variables de proxy, cadenas de certificados locales, sockets, tiempos y diferencias en las respuestas;
- el connector ve el contenido descifrado de los modelos. La clave de firma de la CA nunca se persiste ni se sube, y el archivo público de la CA se elimina al salir;
- tu clave de relay no está en el entorno del proceso hijo ni en la configuración generada del cliente, pero un `~/.config/everyapi/credentials.json` preexistente sigue siendo legible por cualquier proceso que corra como el mismo usuario del sistema. El modo transparente es aislamiento de inyección de credenciales, no un sandbox frente a procesos hijos hostiles.

### `everyapi auth login` —— Device Authorization Grant + inicio de sesión por QR

Usa el Device Authorization Grant (estilo RFC 8628) + la Capa 1 de docs §7-5, "inicio de sesión por QR entre dispositivos":

1. El CLI crea una sesión y **renderiza un QR en la terminal** además de imprimir el código corto y la URL
2. Escanea el QR con el móvil (o abre la URL en un navegador donde ya tengas sesión en EveryAPI) —— la URL dentro del QR ya lleva `?code=USR-789`, así que el dashboard rellena el código automáticamente y el usuario solo pulsa Approve
3. El CLI recibe el token de acceso y lo guarda en `~/.config/everyapi/credentials.json` (modo 0600)

```bash
everyapi auth login                                    # producción; renderiza QR y abre el navegador por defecto
everyapi settings set gateway_region cn               # usar la pasarela acelerada de China en los comandos siguientes
everyapi auth login --api-base http://localhost:8787   # desarrollo local / self-hosted
everyapi auth login --no-browser                       # no abrir el navegador (escanea el QR)
everyapi auth login --no-qr                            # no renderizar el QR (terminales sin UTF-8 / salida por tubería)
```

Ejemplo del QR renderizado en terminal (caracteres Unicode de medio bloque, unas 18-20 filas de alto):

```
█▀▀▀▀▀█  ▀▀ ▄  █▀▀▀▀▀█
█ ███ █  ▀▄█▀  █ ███ █
█ ▀▀▀ █ ▄ ▀ █▀ █ ▀▀▀ █
▀▀▀▀▀▀▀ █▄█▄█▄ ▀▀▀▀▀▀▀
... (el QR real codifica verification_uri?code=USR-789)
```

Por qué esta ruta es más resistente al phishing:

- el usuario **no escribe una contraseña en el dispositivo nuevo** → un sitio de phishing no tiene dónde capturar credenciales
- el usuario **no es redirigido a una página de navegador desconocida** → desaparece la superficie de phishing por redirección web
- aunque el CLI fuera un fork malicioso que genera un QR falso, la página de aprobación tras escanear es el dashboard real de everyapi.ai (lanzado desde un dispositivo donde ya tienes sesión), y un usuario no aprueba un código que no reconoce

El resto de capas de docs §7-5 (pinning de certificados / frase / OAuth con PKCE) se han entregado en PRs independientes (el pinning es solo de informe: la decisión de producto es no forzar el bloqueo).

### `everyapi seller <sub>` —— subcomandos de vendedor del marketplace

Llevan a la terminal el registro de canales y el flujo de retirada del dashboard, para permitir un onboarding scriptable. Antes de registrar un canal, `seller setup` comprueba la elegibilidad (cuenta activa / email verificado / antigüedad de la cuenta / historial de gasto / límite de canales) y enumera las condiciones que fallan **antes de que el usuario escriba una clave**, para no descubrirlo con un 422 tras enviar.

```bash
everyapi seller list                          # listar canales registrados
everyapi seller withdraw                      # transferir todas las ganancias pendientes al saldo principal
everyapi seller withdraw --quota 1000         # transferencia parcial (unidades de la base de datos)
everyapi seller add-key   --type claude --name 'my-pro' \
                        --key 'sk-ant-...' --models 'claude-3-opus,claude-3-sonnet'
everyapi seller add-oauth codex --name 'my-chatgpt' --models 'gpt-4'
                                            # OAuth en un clic: el CLI inicia el device flow, el usuario
                                            # escribe el user_code en el navegador y el token aterriza en el canal
everyapi seller add-oauth claude --name 'my-claude' --models 'claude-3-opus,claude-3-sonnet'
                                            # flujo de pegado: el CLI abre la página de autorización de Anthropic
                                            # y el usuario pega en la terminal el code#state del callback
everyapi seller add-oauth gemini --name 'my-gemini' --models 'gemini-1.5-pro'
                                            # loopback de verdad en un clic: el CLI abre un listener en un puerto
                                            # aleatorio y Google entrega el code directamente al CLI, sin pegar nada
everyapi seller setup                         # asistente interactivo: comprueba la elegibilidad y luego guía el add-key
```

#### `add-key` —— pool de claves de respaldo

`--key` se puede repetir para registrar N credenciales equivalentes en el mismo canal como pool de respaldo (B2, PRODUCT §4.5). Si la clave principal devuelve 401/403, el backend hace failover automático a la siguiente. `--key-remark` también se repite y se empareja posicionalmente con `--key` (la i-ésima `--key-remark` etiqueta la i-ésima `--key`, para identificarlas luego en el dashboard). Los blobs OAuth no pueden formar un pool de respaldo: siguen siendo canales de clave única.

```
everyapi seller add-key   --type claude --name 'claude-pool' \
                        --models 'claude-3-opus' \
                        --key 'sk-ant-primary' --key-remark 'primary' \
                        --key 'sk-ant-backup'  --key-remark 'team backup'
```

El `--type` de `add-key` acepta alias (`openai` / `claude` / `gemini` / `codex` / `vertex` / `aws` / `xai` / `deepseek`) o el id numérico. El registro está sujeto a las condiciones de elegibilidad del marketplace (cuenta activa, email verificado, historial de gasto, límite de canales), y el CLI enumera la lista de comprobaciones fallidas antes de cualquier otra cosa en los tres puntos de entrada (`add-key` / `add-oauth` / `setup`).

#### `add-oauth codex` —— OAuth en un clic (device flow)

`everyapi seller add-oauth codex --name 'my-chatgpt' --models 'gpt-4'` ejecuta el flujo de autorización de dispositivo estilo RFC 8628 de Codex / ChatGPT —— el vendedor **no toca ninguna cadena de token**:

1. El CLI llama a `/api/seller/codex/device/start` y recibe un `user_code` corto y una `verification_uri`
2. El CLI abre `https://auth.openai.com/codex/device` en el navegador por defecto (`--no-browser` lo evita). El usuario escribe el `user_code` en el navegador para completar la autorización
3. El CLI hace polling a `/api/seller/codex/device/poll`. Una vez autorizado, el backend crea el canal y escribe el token OAuth en el campo `key` del canal
4. Salida: id del canal + el email de ChatGPT vinculado

Las cookies de autorización las gestiona un `http.CookieJar` en memoria y no se persisten —— el estado del device flow es efímero y ligado al proceso, lo que encaja con el modelo de amenazas.

#### `add-oauth claude` —— OAuth de pegar y enviar

`everyapi seller add-oauth claude --name … --models …`. El proveedor OAuth de Anthropic fija por su lado el `redirect_uri` a `https://console.anthropic.com/oauth/code/callback`, así que el CLI no puede recibir el callback en un listener local. El flujo:

1. El CLI llama a `/api/seller/claude/oauth/start`. El backend genera el par PKCE + state y devuelve la URL de autorización de Anthropic
2. El CLI abre el navegador por defecto (`--no-browser` lo evita). El usuario inicia sesión en Anthropic y aprueba
3. Anthropic redirige a una página de callback que muestra una cadena `<code>#<state>`
4. **El usuario pega esa cadena en el CLI**
5. El CLI llama a `/api/seller/claude/oauth/complete`. El backend intercambia code+verifier por el token y crea el canal

Un paso de pegado más que el device flow, pero mucho más fácil que buscar a mano `~/.claude/auth.json`. La cookie de sesión la emite el backend en el start y el complete debe llegar a la misma sesión —— el `http.CookieJar` del CLI vive en el proceso y está aislado por invocación.

#### `add-oauth gemini` —— OAuth loopback de verdad en un clic

`everyapi seller add-oauth gemini --name … --models … [--no-browser] [--timeout 5m]`. El cliente OAuth de aplicación instalada de gemini-cli de Google acepta `http://127.0.0.1:<port>/callback` como `redirect_uri`, así que **el CLI abre su propio listener para recibir el callback** —— el usuario solo inicia sesión en el navegador, sin pegar nada. El flujo:

1. El CLI abre un servidor HTTP de un solo uso en un puerto efímero aleatorio (`127.0.0.1:0`), con la ruta fija `/callback`
2. El CLI llama a `/api/seller/gemini/oauth/start` con `redirect_uri = http://127.0.0.1:<port>/callback`. El backend valida el redirect de forma estricta: loopback / puerto ≥ 1024 / esquema http / ruta /callback / sin query, fragmento ni userinfo (previene SSRF y secuestro de redirección)
3. El CLI abre el navegador por defecto. El usuario inicia sesión en Google y da su consentimiento
4. Google redirige con `?code=…&state=…` al listener del CLI
5. El CLI verifica que el state coincide (protege frente a flujos obsoletos o falsificados) y llama a `/api/seller/gemini/oauth/complete`
6. El backend intercambia el code + el mismo redirect_uri por el token y crea el canal

Comparación con los otros dos proveedores:

| Proveedor | UX | Motivo |
|---|---|---|
| `codex` | el usuario escribe un user_code de 6 caracteres en el navegador, el CLI hace polling automático | device flow de OpenAI, sin redirect_uri |
| `claude` | el usuario inicia sesión en el navegador y pega `code#state` en el CLI | Anthropic fija el redirect_uri a su propia URL de callback |
| `gemini` | el usuario inicia sesión en el navegador, cierra la pestaña y listo | Google permite redirecciones a loopback |

`--timeout` acota la espera (5 minutos por defecto). Al expirar, el CLI sale y cierra el listener limpiamente.

### `everyapi edge <sub>` —— despliegue en un comando del agente proveedor BYO-GPU

Permite vender tus GPUs ociosas a través de EveryAPI. El CLI comprime el despliegue en un único conjunto de comandos —— `register` / `list` / `start` / `status` / `logs` / `models` / `rename` / `pause` / `resume` / `stop` / `update` / `remove` —— para que los proveedores no tengan que copiar docker-compose a mano, rellenar un `.env` ni manejar tokens de registro. El camino habitual son ocho comandos:

```bash
everyapi auth login                              # reutiliza tu sesión existente
everyapi edge register --name "rtx-4090"    # llama a /api/seller/edge/nodes para obtener node_id + token, escribe ~/.local/share/everyapi/edge/<id>/
everyapi edge start                         # detecta NVIDIA / ROCm / Apple Silicon / CPU, docker compose up -d
everyapi edge models pull llama3.1:8b       # docker compose exec ollama ollama pull ...
everyapi edge status                        # docker compose ps local + estado online/offline del dashboard
everyapi edge logs -f                       # seguir los logs
everyapi edge update                        # docker compose pull && up -d
everyapi edge remove                        # down -v + borrar el directorio local + DELETE en el backend
```

`start` renderiza el `docker-compose.yml` en tiempo de ejecución con `text/template` (**no es un YAML estático embebido**) —— así los nombres de contenedor se namespacean con el node_id, varios nodos en el mismo host no colisionan, y el passthrough de GPU se renderiza condicionalmente por modo (NVIDIA = `deploy.resources.devices` + driver nvidia, ROCm = `/dev/kfd` + `/dev/dri` + `group_add: video`, macOS = sin contenedor de ollama, el agente se conecta al ollama nativo del host vía `host.docker.internal`).

Flujo de credenciales: el CLI llama a `POST /api/seller/edge/nodes` con tu Bearer `sk-everyapi-` existente → el backend devuelve un `registration_token` una sola vez (después solo guarda el sha256 y no vuelve a mostrarlo) → el CLI lo escribe en `~/.local/share/everyapi/edge/<id>/node.json` con modo 0600 → se renderiza como variable `EVERYAPI_REGISTRATION_TOKEN` en el compose. **El token de registro nunca se escribe en ningún archivo .env** (para que los proveedores no lo comiteen por accidente).

Requisitos: `docker` + `docker compose v2` (v1 está EOL y no se soporta). En macOS: `brew install ollama && brew services start ollama` (la aceleración Metal no funciona dentro de un contenedor docker).

### `everyapi wallet topup` —— redirección de recarga con frase antiphishing

`everyapi wallet topup` abre la página de recarga del dashboard. Antes de redirigir aplica la verificación de la Capa 3 de docs §7-5:

1. El CLI llama al backend `POST /api/cli/jump-session` y recibe un id de sesión + una frase de 4 emojis (p. ej. `🌊 🦊 🍕 🚀`)
2. El CLI imprime tanto la URL como la frase en la terminal y avisa: "en un momento deberías ver esta misma frase en la parte superior de la página"
3. El usuario pulsa Enter y el CLI abre la URL en el navegador del sistema (incluyendo `?jump_session=<id>`)
4. Al cargar, el dashboard llama al backend `GET /api/cli/jump-session/:id/phrase`, recibe la misma frase y **la muestra de forma destacada en la cabecera de la página**
5. El usuario compara visualmente: coincide → es EveryAPI de verdad; no coincide o no aparece → cierra la pestaña (posible phishing)

Por qué frena el phishing: la frase vive en la memoria del backend indexada por un id de sesión aleatorio de 32 hex. Un sitio de phishing no tiene ruta autenticada para obtenerla, y un `wallet/topup?jump_session=<id>` falsificado por un atacante tampoco puede leerla. Un TTL corto (10 minutos) + uso único (la sesión se borra cuando el dashboard la consulta) reducen aún más el riesgo de reutilización.

```bash
everyapi wallet topup                    # abre el navegador por defecto
everyapi wallet topup --no-browser       # solo imprime la URL para copiarla a mano
```

### `everyapi auth status` —— saldo / uso / cuota actuales

```
$ everyapi auth status

  alice (alice@example.com)
  quota:     $12.34 remaining   $5.67 used
  requests:  1,234
  topup:     https://app.everyapi.ai/wallet
```

### `everyapi version update` —— ejecuta la actualización por ti

No existe un `everyapi update` de primer nivel; las acciones del ciclo de vida del CLI viven bajo `version` (`everyapi version update`, `everyapi version uninstall`).

Consulta la última release del espejo de GitHub, la compara con tu versión actual y luego delega la actualización a lo que haya instalado el binario —— Homebrew (`brew update && brew upgrade everyapi`), `go install …@latest` o el script de instalación publicado. Un comando, sin copiar y pegar.

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

¿Por qué no reemplazar el binario directamente? Porque las cadenas de verificación de Homebrew y de Go (SHA / firmas de bottle / checksum de módulo) son más robustas que cualquier cosa que reconstruyéramos dentro del CLI, y que un ejecutable se sustituya a sí mismo mientras corre es un campo de minas en Windows. Una instalación hecha con el script sí se reemplaza en su sitio, pero volviendo a ejecutar el instalador publicado, que ya hace eso de forma segura.

Flags:
- `--check` —— compara en silencio. Sale con 0 si estás al día, con 1 si estás desactualizado y con 2 si no se pudo determinar la última versión (con el motivo en stderr) —— un fallo de red no debe leerse como "hay una actualización disponible". Para CI / cron:
  ```bash
  everyapi version update --check || echo "needs upgrade"
  ```
- `--dry-run` —— imprime los comandos que ejecutaría sin ejecutarlos (para confirmarlos)

### `everyapi settings` —— preferencias del CLI (idioma, etc.)

El CLI incluye i18n en 8 idiomas: inglés, chino simplificado, chino tradicional, japonés, coreano, español, alemán y francés —— las cadenas del CLI se renderizan en el idioma elegido. Los errores de la API del backend se negocian automáticamente mediante la cabecera `Accept-Language` y cubren los mismos 8.

```bash
$ everyapi settings                          # selector interactivo: elegir idioma
$ everyapi settings list                     # ver la configuración actual
$ everyapi settings set language zh          # fijarlo directamente
$ everyapi settings set language fr          # igual para francés
$ everyapi settings set terminal_mode tmux   # mantener los lanzamientos interactivos dentro de tmux
$ everyapi use codex -- resume               # reconectar al único tmux del proyecto, o abrir el selector de Codex
$ everyapi settings reset                    # volver a los valores por defecto (en + autodetección de LANG)
```

**Modo de terminal**: el primer `everyapi use` interactivo te pregunta si quieres los lanzamientos en tu terminal nativa o dentro de tmux y guarda la elección en `terminal_mode`. El modo tmux reinicia todo el proceso `everyapi use` dentro de una sesión `everyapi-v3-*` identificada por la herramienta elegida, la identidad del sistema de archivos del workspace y una identidad de lanzamiento aleatoria de 128 bits, de modo que el connector, el sanitizador, la configuración temporal y la herramienta objetivo sobreviven a un detach. El mensaje de lanzamiento imprime el comando `tmux attach -t <session>` exacto. Un `resume` de Codex "puro" busca primero esa identidad: si hay exactamente un panel de agente gestionado vivo, se revalida por nombre exacto de sesión y se reconecta; con cero o varios, no adivina y recurre al selector normal de resume de Codex. Antes de cada lanzamiento en tmux, el CLI solo considera candidatas sesiones `everyapi-v3-*`, `everyapi-v2-*` o las heredadas `everyapi-<pid>-<timestamp>` generadas de forma estricta, y solo las elimina si un único comando atómico de tmux revalida que la sesión contiene exactamente una ventana con exactamente un panel envoltorio de EveryAPI ya muerto. Los agentes vivos en detach, las sesiones de tmux normales creadas por el usuario y cualquier sesión con paneles o ventanas añadidas por el usuario se conservan siempre. Una sesión cuyo panel gestionado ha muerto pero que tiene paneles añadidos por el usuario aún vivos se conserva pero no se reutiliza. Cada cliente lanzado puede consultar `EVERYAPI_TERMINAL_MODE`, `EVERYAPI_TMUX_SESSION` y `EVERYAPI_TMUX_ATTACH_COMMAND`. Codex, Claude Code, OpenCode y Kilo reciben además el mismo contexto de sesión por su superficie documentada de instrucciones al modelo, incluida la regla de no crear sesiones tmux anidadas. Los demás clientes mantienen solo el contrato de entorno, sin inyección de mensajes de usuario. Un lanzamiento que ya está dentro de tmux no se anida, y los lanzamientos no interactivos siempre permanecen nativos. Si tmux no está disponible, el selector de primer uso desactiva esa opción. Si tu configuración de tmux existente entra en conflicto, se falla con instrucciones para instalarlo o revertir en lugar de cambiar el comportamiento en silencio.

**Autodetección**: si no lo has configurado explícitamente, al arrancar el CLI lee las variables de entorno en este orden: `EVERYAPI_LANG > LC_ALL > LC_MESSAGES > LANG`. Si tu locale del sistema es `zh_CN.UTF-8` / `ja_JP.UTF-8` / `fr_FR.UTF-8`, etc., se aplica de inmediato —— sin configurar nada.

**Override puntual**:

```bash
EVERYAPI_LANG=zh everyapi auth status             # solo esta llamada en chino, no se guarda
```

**Ejemplo de traducción** (error de sesión no iniciada, 8 idiomas × la misma frase):

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

La configuración se guarda en `~/.config/everyapi/settings.json` (mismo directorio que `credentials.json`, pero con modo `0644` —— no contiene secretos).

**Mejorar traducciones / añadir un idioma**: consulta [`internal/i18n/locales/README.md`](../internal/i18n/locales/README.md).

## Archivos de configuración

Las credenciales se guardan en `~/.config/everyapi/credentials.json` (o `$XDG_CONFIG_HOME/everyapi/` si `$XDG_CONFIG_HOME` está definido) con modo de archivo `0600`. Lo escribe `everyapi auth login` y lo leen todos los demás comandos.

> ⚠️ **El token se guarda en texto plano**. El modo `0600` + una ruta privada en `$HOME` es la misma práctica que CLIs del sector como `gh auth` o `aws configure`, pero **bajo un modelo de amenazas de robo del equipo doméstico o malware**, cualquier proceso que pueda leer ese archivo puede llamar a la API de EveryAPI como tú (incluidas las herramientas MCP —— ver el paso de fricción de la ruta de dinero más abajo). Recomendaciones:
> - no ejecutes `everyapi auth login` en equipos compartidos o públicos
> - en macOS: considera `everyapi auth logout` antes de activar FileVault
> - en Linux: activa el cifrado del directorio home (`ecryptfs` / LUKS)
> - ante sospecha de filtración → `everyapi auth logout` para borrar de inmediato las credenciales locales y rota tu clave de API en el dashboard de EveryAPI
>
> Los backends de keychain de plataforma (macOS Keychain / Windows DPAPI / Linux Secret Service) están planificados pero aún no publicados.

Campos:

- `api_base` —— URL de la pasarela EveryAPI. Por defecto `https://api.everyapi.ai`. Los usuarios self-hosted y el desarrollo local pueden sobrescribirlo con `--api-base` en `auth login`.
- `access_token` —— el bearer usado en todas las llamadas autenticadas a la API.
- `relay_key` —— la clave de API de relay (`sk-everyapi-…`), usada en el entorno del subproceso de `everyapi use`. Se obtiene de `/api/token/*` y se cachea aquí.
- `user_id` / `username` —— cacheados para que `auth status` pueda renderizar la línea de identidad antes de la primera ida y vuelta a la API.

La región de la pasarela es una preferencia del CLI en `settings.json`: si no está definida, el login interactivo pregunta una vez y guarda la elección. `everyapi settings set gateway_region cn` dirige el tráfico de la pasarela oficial a `https://api-cn.everyapi.ai`, y `global` usa `https://api.everyapi.ai`. Un `--api-base` personalizado para self-hosted sigue teniendo prioridad.

## Desarrollo

Desde el directorio de fuentes del CLI (donde están este README, `go.mod` y el `Makefile`):

```bash
go test ./...
go run . auth status       # contra producción
go run . auth login --api-base http://localhost:8787   # contra un backend local
```

Compilación cruzada local para todas las plataformas (misma receta que CI):

```bash
make cli-release           # artefactos en dist/ (6 plataformas × 1 binario = 6 archivos)
```

## Servidor MCP (subcomando `everyapi mcp`)

El binario `everyapi` **incluye** un servidor [Model Context Protocol](https://modelcontextprotocol.io) —— expuesto como subcomando (`everyapi mcp` lee de stdin y escribe en stdout). Un agente de IA (Claude Code / Cursor / Codex CLI / cualquier cliente MCP) puede llamarlo directamente **sin que el usuario abra una terminal**.

> ⚠️ **Modelo de autenticación y superficie de exposición del servidor MCP**
>
> - **No abre ningún puerto**: `everyapi mcp` es JSON-RPC puro por stdio y lo lanza el CLI anfitrión. **No escucha en ningún socket ni puerto TCP** —— superficie de red nula.
> - **Lee `~/.config/everyapi/credentials.json` directamente**: el servidor MCP no tiene flujo de autenticación propio, así que poder leer el archivo de credenciales = poder llamar como tú a todas las herramientas expuestas. Cualquier anfitrión MCP capaz de ejecutar un proceso con tus permisos de usuario tiene acceso completo.
> - **La ruta de dinero `everyapi_seller_withdraw` tiene un paso de fricción**: quien llame debe pasar `confirm: "yes"`, lo que garantiza que un agente de IA exponga la acción de transferencia a un humano en la interfaz y evita fugas silenciosas de fondos. Las demás herramientas de solo lectura (status / topup / seller_list) no lo exigen.
>
> No instales anfitriones MCP en los que no confíes.

### Instalación

Es el mismo binario que el CLI: si instalas el CLI, ya tienes el servidor MCP:

```bash
make cli                                              # compilación local, produce ./bin/everyapi
# o vía go install:
go install github.com/everyapi-ai/everyapi-ai/v3@latest
```

### Conectarlo con Claude Code

Añade a `~/.claude/settings.json`:

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

La conexión con Cursor, el Codex CLI y otros clientes MCP es similar: apunta `command` al binario `everyapi` y usa `args: ["mcp"]`.

### Requisito previo de autenticación

Debes ejecutar `everyapi auth login` en una terminal al menos una vez —— el servidor MCP es un proceso en segundo plano sin capacidad de interacción con la terminal, así que no puede completar por sí mismo el flujo de device code. Lee directamente `~/.config/everyapi/credentials.json`; si no existe, todas las herramientas devuelven un mensaje "not logged in" con `isError: true` que guía al usuario a iniciar sesión.

### Herramientas expuestas (15 en total)

| Herramienta | Entrada | Para qué sirve |
|---|---|---|
| `everyapi_status` | ninguna | saldo / uso / número de peticiones actuales |
| `everyapi_topup` | ninguna | devuelve la URL web de recarga |
| `everyapi_seller_list` | ninguna | lista los canales de vendedor del marketplace |
| `everyapi_seller_withdraw` | `{confirm: "yes", quota?: int}` | transfiere seller_quota al saldo principal; **requiere `confirm: "yes"`** (fricción en la ruta de dinero) |
| `everyapi_seller_eligibility` | ninguna | checklist de solo lectura de la puerta de montaje (marketplace abierto, cuenta activa, email verificado, antigüedad de la cuenta, uso previo, límite de canales). Llámala *antes* de pedirle una clave al usuario |
| `everyapi_seller_add_key` | `{name, type, keys[], models, key_remarks?[], remark?}` | monta un canal de vendedor a partir de claves API en texto plano —— el gemelo de `everyapi seller add-key`. Pasa únicamente claves que el usuario haya proporcionado en la conversación |
| `everyapi_seller_add_oauth_codex_start` | `{name, models}` | inicia el flujo de autorización de dispositivo de Codex / ChatGPT, devuelve `user_code` + `verification_uri` + `flow_id` |
| `everyapi_seller_add_oauth_codex_poll` | `{flow_id}` | consulta el estado de autorización de Codex. `pending`/`slow_down` = seguir haciendo polling, `authorized` devuelve el id del canal, `expired`/`denied` termina |
| `everyapi_seller_add_oauth_claude_start` | `{name, models}` | inicia el flujo OAuth de Anthropic, devuelve `authorize_url`. El usuario inicia sesión en el navegador y obtiene una cadena `<code>#<state>` |
| `everyapi_seller_add_oauth_claude_complete` | `{input}` | envía la cadena `<code>#<state>` pegada por el usuario en el paso anterior y crea el canal |
| `everyapi_edge_list` | ninguna | lista los nodos edge BYO-GPU: id, nombre, estado en línea, canal emparejado, última conexión, modelos instalados |
| `everyapi_edge_status` | `{node_id: int}` | detalle de un nodo —— flag de pausa, versión del agente, modelo / número / VRAM de GPU, modelos instalados |
| `everyapi_edge_remove` | `{node_id: int, confirm: "yes"}` | elimina un nodo (y su canal emparejado si era el último); **requiere `confirm: "yes"`** (fricción en rutas destructivas) |
| `everyapi_admin_marketplace_status` | ninguna | lee el flag `marketplace.enabled` de todo el despliegue. Requiere rol admin |
| `everyapi_admin_marketplace_set` | `{enabled: bool, confirm: "yes"}` | abre o cierra el marketplace para todo el despliegue; **requiere `confirm: "yes"`**. Los nodos y canales existentes siguen sirviendo cuando está cerrado |

**Patrón de uso de las herramientas OAuth** (así lo lleva un agente de IA en la conversación):

```
Usuario: añade un canal de vendedor de ChatGPT Plus, nombre my-chatgpt, models gpt-4
IA    → everyapi_seller_add_oauth_codex_start({name: "my-chatgpt", models: "gpt-4"})
       ← "escribe USR-789 en chatgpt.com/codex y avísame cuando termines"
Usuario: ya está, lo hice en el navegador
IA    → everyapi_seller_add_oauth_codex_poll({flow_id: "..."})
       ← "status=pending, espera unos segundos"
[sigue haciendo polling hasta authorized]
       ← "status=authorized — channel #314 mounted"

Usuario: añade también el de Claude Pro, my-claude / claude-3-opus
IA    → everyapi_seller_add_oauth_claude_start({...})
       ← "completa la autorización en [URL] y pásame la cadena code#state"
Usuario: code-abc123#state-xyz
IA    → everyapi_seller_add_oauth_claude_complete({input: "code-abc123#state-xyz"})
       ← "Channel #315 mounted"
```

El OAuth de Gemini (flujo loopback) **no se expone por MCP** —— el ciclo de vida del listener de loopback no encaja con un ciclo de vida que cruza llamadas a herramientas. Para Gemini sigue usándose `everyapi seller add-oauth gemini` desde el CLI.

### Prueba de humo manual

```bash
make cli
./bin/everyapi mcp <<'EOF'
{"jsonrpc":"2.0","id":1,"method":"initialize"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"everyapi_status","arguments":{}}}
EOF
```

Deberías ver 3 líneas de respuesta JSON: el resultado de initialize, una lista de 15 herramientas y el texto de status (o un isError de sesión no iniciada).

## Lo que este binario todavía **NO** incluye

**Sin implementar** por ahora (en orden de importancia; se añadirán de forma incremental en releases posteriores sin romper la superficie v1):

- ⚠️ Firma de código a nivel de sistema operativo (notarización de macOS / Authenticode de Windows) —— hoy dependemos de la doble verificación con sigstore cosign keyless + SHA256SUMS, ambos adjuntos a cada GitHub Release y verificados automáticamente por Homebrew
- ❌ Backends de keychain de plataforma —— el token sigue guardándose en disco en texto plano (modo 0600)

Cosas que se listaban aquí pero **ya están publicadas** (no las trates como pendientes):

- ✅ Proxy de saneamiento local —— los comandos son `everyapi proxy {start,stop,status,configure}` (no `everyapi start` / `everyapi configure`). Motor + 6 detectores integrados + regex personalizadas, integrado con `everyapi use`
- ✅ Onboarding OAuth de vendedor para los tres proveedores (codex device / claude paste / gemini loopback)
- ✅ Inicio de sesión por QR como ruta principal —— `auth login` usa device-code **+ QR como ruta principal**, con `--no-qr` como alternativa
- ✅ Capas antiphishing —— frase (`everyapi wallet topup`), comprobaciones estrictas de PKCE/state y pinning de certificados, todo entregado. El pinning es **solo de informe** (silencioso si coincide, aviso si no; nunca rechaza la conexión), y la decisión de producto es "avisar, no forzar"

## Reportar vulnerabilidades

Consulta [`SECURITY.md`](../SECURITY.md).
