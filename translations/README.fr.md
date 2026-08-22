> 🌐 [English](../README.md) · [简体中文](README.zh-CN.md) · [日本語](README.ja.md) · [한국어](README.ko.md) · [Español](README.es.md) · [Deutsch](README.de.md) · **Français**

# CLI `everyapi`

CLI d'onboarding acheteur pour la passerelle d'API IA [EveryAPI](https://everyapi.ai). Elle met en route n'importe quel agent de code pris en charge **en moins d'une minute**, via un registre unique et audité.

État : **flux principaux livrés** —— onboarding acheteur, commandes vendeur (clé simple + OAuth pour trois fournisseurs), proxy d'assainissement, connexion par QR comme chemin principal et couches anti-hameçonnage sont en place. Il ne manque que la signature de code au niveau système et les backends de trousseau de la plateforme (voir « Ce que ce binaire n'inclut PAS encore » à la fin).

## Installation

**macOS (Homebrew) :**

```bash
brew tap everyapi-ai/tap && brew install everyapi
```

Pour les mises à jour suivantes, lancez d'abord `brew update` (sinon `brew upgrade everyapi` utilise la formule en cache et annonce « already installed » alors qu'une nouvelle version existe) :

```bash
brew update && brew upgrade everyapi
```

**Linux / macOS (script d'installation) :**

```bash
curl -fsSL https://dl.everyapi.ai/install.sh | bash
```

Le script détecte le système et l'architecture, télécharge le `everyapi_{os}_{arch}.tar.gz` correspondant, vérifie son SHA256 et l'installe dans `~/.local/bin` (ou `/usr/local/bin` si vous l'exécutez en root). Si [cosign](https://github.com/sigstore/cosign) est installé, il vérifie aussi la signature keyless —— passez `--require-signature` pour rendre cette étape obligatoire (recommandé en CI ou dans les environnements sensibles à la chaîne d'approvisionnement).

Une seule ligne partout dans le monde : le script choisit la source de téléchargement à l'exécution —— GitHub Releases s'il est joignable, un miroir en Chine continentale quand GitHub est lent ou bloqué —— la même commande fonctionne donc dedans comme dehors. Définissez `EVERYAPI_DOWNLOAD_BASE` pour forcer un miroir précis.

Options courantes :

```bash
curl -fsSL https://dl.everyapi.ai/install.sh | bash -s -- --version v0.2.2     # figer une version
curl -fsSL https://dl.everyapi.ai/install.sh | bash -s -- --prefix /usr/local  # choisir le préfixe
curl -fsSL https://dl.everyapi.ai/install.sh | bash -s -- --require-signature  # abandonner si cosign échoue
curl -fsSL https://dl.everyapi.ai/install.sh | bash -s -- --force              # réinstaller la même version
```

Pour mettre à jour plus tard, relancez la même commande : le script résout le dernier tag de release et remplace le binaire sur place si une version plus récente existe. Si vous êtes déjà sur la version cible, il se termine par `already at vX.Y.Z — nothing to do`, il est donc sans danger dans un script de provisioning ou vos dotfiles. Passez `--force` pour réinstaller par-dessus (utile pour vérifier l'intégrité ou réparer un fichier corrompu). Le script lui-même est publié dans ce dépôt sous [`install.sh`](../install.sh), si vous préférez le télécharger et le lire d'abord.

**Utilisateurs Go (`go install`) :**

```bash
go install github.com/everyapi-ai/everyapi-ai/v3@latest
```

**Windows (PowerShell) :**

```powershell
irm https://dl.everyapi.ai/install.ps1 | iex
```

Même flux que le script shell : il résout le dernier tag, télécharge `everyapi_windows_amd64.zip` + `SHA256SUMS`, vérifie le hash (et la signature si cosign est dans le `PATH`), installe `everyapi.exe` dans `%LOCALAPPDATA%\everyapi\bin` et l'ajoute au `PATH` utilisateur. Pour figer une version ou passer d'autres options, matérialisez d'abord le script : `& ([scriptblock]::Create((irm https://dl.everyapi.ai/install.ps1))) -Version v0.2.2`. Ce script vit également dans le dépôt sous [`install.ps1`](../install.ps1).

**Windows (manuel) :** téléchargez `everyapi_windows_amd64.zip` (ou un autre artefact) depuis la [page des releases](https://github.com/everyapi-ai/everyapi-ai/releases/latest), vérifiez-le contre `SHA256SUMS` et placez le binaire dans votre `%PATH%`.

## Commandes

Lancer `everyapi` sans argument dans un TTY ouvre un lanceur interactif sur ce même ensemble ; `everyapi help` l'affiche sous forme de texte.

| Commande | À quoi ça sert |
|---|---|
| `everyapi auth <sub>` | Se connecter, se déconnecter et afficher l'état de la session (`login` / `logout` / `status`) |
| `everyapi wallet <sub>` | Rechargement (avec vérification de la phrase anti-hameçonnage), historique des paiements, moyens de paiement |
| `everyapi checkin <sub>` | Récupérer le quota quotidien du jour ; afficher le calendrier du mois |
| `everyapi account <sub>` | Profil, 2FA, code d'affiliation, formules d'abonnement |
| `everyapi use <tool>` | Configurer l'environnement et lancer un CLI tiers pointant vers EveryAPI |
| `everyapi token <sub>` | Gérer les clés API de relais (list / create / key / revoke / switch / …) |
| `everyapi models <sub>` | Catalogue de modèles : list / pricing / groups |
| `everyapi stats <sub>` | Consommation, journal des requêtes, performance par modèle, santé des upstreams |
| `everyapi market <sub>` | Annonces de demande, litiges, signalements d'abus |
| `everyapi inbox <sub>` | Notifications in-app et messages directs |
| `everyapi seller <sub>` | Commandes vendeur de la marketplace (list / setup / withdraw / add-key / add-oauth) |
| `everyapi edge <sub>` | Déploiement en une commande de l'agent fournisseur BYO-GPU (register / start / status / logs / models / rename / pause / resume / stop / update / remove) |
| `everyapi artifacts <sub>` | Publier et gérer des rapports HTML autonomes (`share` / `list` / `update` / `delete`) |
| `everyapi events` | S'abonner au flux d'événements en direct (SSE) |
| `everyapi feedback` | Envoyer un rapport de bug ou une demande de fonctionnalité à l'équipe |
| `everyapi proxy <sub>` | Proxy d'assainissement local (`start` / `stop` / `status` / `configure`) |
| `everyapi computer <sub>` | Lire et piloter les fenêtres d'apps macOS locales via l'Accessibilité |
| `everyapi mcp` | Fonctionner comme serveur MCP (JSON-RPC sur stdin/stdout) |
| `everyapi doctor` | Autodiagnostic : identifiants, passerelle, sanitizer, outils installés |
| `everyapi settings <sub>` | Consulter et modifier les préférences du CLI (langue, mode terminal) |
| `everyapi admin` | Console opérateur —— visible uniquement pour les comptes admin |
| `everyapi version [update\|uninstall]` | Version du build ; vérifier et lancer la mise à jour ; désinstaller le CLI |
| `everyapi help` | Afficher la liste complète des commandes |

### `everyapi computer <sub>` —— computer use local sur macOS

Sur macOS, le CLI peut découvrir les apps et fenêtres en cours d'exécution, renvoyer un instantané borné de l'Accessibilité et effectuer des actions sémantiques ou par coordonnées. Cette surface est purement locale et n'est pas enregistrée dans `everyapi mcp`. Les builds Linux et Windows renvoient explicitement `unsupported_platform`.

Sur macOS, `everyapi computer` pilote une petite app auxiliaire signée indépendamment (`EveryAPI Computer Use.app`, construite depuis `clients/desktop/native/computer-use-macos`) via une socket Unix locale, et la télécharge puis la lance automatiquement à la première utilisation si elle n'est pas déjà installée — y compris lorsque EveryAPI Connect a déjà installé sa propre copie embarquée, que ce CLI réutilise au lieu d'en télécharger une seconde. L'auxiliaire annonce la prise en charge des captures d'écran comme false, car macOS n'expose pas via ce fournisseur d'identifiant public fiable de capture au niveau fenêtre ; il ne la remplace jamais par une capture de région d'écran susceptible de contenir une app en superposition.

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

Lancez `everyapi computer permissions --json` puis accordez l'Accessibilité à **EveryAPI Computer Use** — pas à `everyapi`, `osascript` ni à votre terminal — dans Réglages Système > Confidentialité et sécurité > Accessibilité. Comme l'auxiliaire est une app signée à part avec sa propre identité de bundle, cette autorisation reste cantonnée à cette seule capacité : elle n'autorise pas au passage tous les scripts AppleScript ou JXA de la machine, et elle survit aux mises à jour du CLI et de l'auxiliaire. `permissions` rapporte directement l'Accessibilité et l'Automatisation comme `unknown`, puisque ce fournisseur ne dépend pas de System Events et n'a pas de contrôle préalable d'Automatisation distinct.

Les index d'éléments proviennent du dernier instantané `get-app-state` et expirent au bout de deux minutes. Les fenêtres se sélectionnent par index (`--window-index`) mais sont identifiées en interne par le véritable id de fenêtre que CoreGraphics attribue à l'écran lorsqu'il existe, avec repli sur un id synthétique limité à l'instantané pour les fenêtres réduites ; dans les deux cas le fournisseur utilise une empreinte interne pour détecter les changements observables, mais les attributs publics d'Accessibilité ne peuvent pas prouver qu'une fenêtre ou un contrôle de remplacement aux attributs identiques est bien la même instance native. Le cache ne stocke que des données opaques d'application, de processus, de fenêtre, de chemin, de role, de frame, de nom d'action et d'empreinte sous `~/.config/everyapi/computer-use/state/` avec des permissions privées. Reprenez un instantané après `app_stale`, `element_stale` ou `window_stale`. Une action GUI réussie reste réussie même si son rafraîchissement d'état au mieux échoue ; le JSON contient alors `refreshError` au lieu de renvoyer une erreur d'action réessayable. Si l'appel à l'auxiliaire est interrompu ou renvoie un reçu invalide après la remise d'une action, `action_outcome_unknown` signifie que l'action a peut-être déjà eu lieu ; rafraîchissez l'état avant de décider de réessayer.

Une liste maintenue d'apps de terminal connues, de gestionnaires de mots de passe, de Trousseaux d'accès, de Mots de passe, des Réglages Système et d'EveryAPI Connect est bloquée à titre de friction en défense en profondeur. Le blocage par bundle ID n'est pas un classificateur d'applications exhaustif : des apps non listées, des éditeurs à terminal intégré, des navigateurs et des apps renommées ou nouvellement publiées peuvent exposer des capacités équivalentes. La véritable frontière de confiance reste la cible `--app` explicite, le TCC de macOS et les droits du même utilisateur que l'appelant. Le texte observé est débarrassé des séquences de contrôle du terminal et analysé à la recherche d'identifiants avant affichage ; le texte saisi ou assigné qui correspond aux détecteurs de secrets intégrés est refusé. Préférez `--text-stdin` et `--value-stdin` pour tenir le texte ordinaire hors de l'historique du shell.

### `everyapi use <tool>` —— lancer un CLI tiers pointant vers la passerelle EveryAPI

C'est la raison principale d'installer ce CLI : configurer et démarrer un client de code pris en charge via EveryAPI. Les intégrations natives (`antigravity`, `librefang`) conservent leur propre chemin d'authentification et ne reçoivent pas de clé de relais copiée.

```bash
everyapi use claude            # Claude Code → EveryAPI
everyapi use codex             # OpenAI Codex CLI → EveryAPI
everyapi use opencode          # OpenCode → fournisseur EveryAPI limité au processus
everyapi use gemini            # Google Gemini CLI → EveryAPI
everyapi use antigravity       # Antigravity (authentification et routage Google natifs)
everyapi use aider             # Aider → EveryAPI (avec sélection de modèle)
everyapi use goose             # Goose CLI → EveryAPI (avec sélection de modèle)
everyapi use crush             # Crush CLI → catalogue de modèles EveryAPI isolé
everyapi use cline             # Cline CLI → configuration de fournisseur liée au cycle de vie
everyapi use openclaw          # TUI locale OpenClaw → catalogue EveryAPI isolé
everyapi use continue          # Continue CLI → configuration d'assistant isolée
everyapi use kilo              # Kilo Code CLI → configuration de fournisseur limitée au processus
everyapi use pi                # agent de code Pi → catalogue de modèles isolé
everyapi use pi-web            # UI web de Pi → entrée de fournisseur dans un models.json durable
everyapi use vibe              # Mistral Vibe → fournisseur générique isolé
everyapi use copilot           # GitHub Copilot CLI → BYOK officiel limité au processus
everyapi use droid             # Factory Droid → réglages d'exécution isolés
everyapi use openhands         # OpenHands CLI → surcharges d'environnement explicites, propres au processus
everyapi use forge             # ForgeCode → session compatible OpenAI isolée
everyapi use llxprt            # LLxprt Code → home isolé + options d'exécution fixes
everyapi use grok              # xAI Grok Build → EveryAPI
everyapi use qwen-code         # Alibaba Qwen Code → EveryAPI (avec sélection de modèle)
everyapi use kimi-code         # Moonshot Kimi Code → EveryAPI (avec sélection de modèle)
everyapi use hermes            # Nous Research Hermes Agent → EveryAPI (avec sélection de modèle)
everyapi use librefang         # lancer LibreFang (processus d'identifiants EveryAPI natif)
everyapi use open-webui        # serveur Open WebUI → EveryAPI comme backend OpenAI
everyapi use deepseek-harness  # UI web DeepSeek Harness (dsh) → fournisseur et identifiant générés
everyapi use hermes --model gpt-5.1      # figer le modèle et sauter le sélecteur
everyapi use claude                      # mode transparent par défaut : reste sur api.anthropic.com
everyapi use codex                       # reste sur api.openai.com
everyapi use antigravity                 # conserve l'Origin officiel de Google
everyapi use claude --transparent=false  # désactive le mode transparent : injecte l'URL de base de la passerelle + la clé de relais
everyapi use                             # sans argument → sélecteur interactif des outils installés
```

Chaque outil a ses conventions, mais le CLI les retient pour vous :

| Outil | Comment il se connecte à EveryAPI |
|---|---|
| claude | env : `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN` ; modèles compatibles en direct via la découverte de la passerelle |
| codex | env : `OPENAI_API_KEY` plus un `CODEX_HOME` EveryAPI persistant pour conserver la session, avec un `--profile` lié au cycle de vie et un catalogue de modèles limité à la clé (codex route par configuration, pas par `OPENAI_BASE_URL`) |
| gemini | env : `GEMINI_API_KEY`, `GOOGLE_GEMINI_BASE_URL`, `GEMINI_MODEL` ; surcouche de configuration isolée avec mode d'authentification |
| antigravity | lanceur Antigravity natif (`agy`) |
| aider | env compatible OpenAI + espace de noms de modèles LiteLLM `openai/<model>` |
| goose | `GOOSE_PROVIDER=openai`, `GOOSE_MODEL`, `OPENAI_API_KEY`, `OPENAI_BASE_URL` |
| crush | `CRUSH_GLOBAL_CONFIG` limité au processus ; la clé est référencée via l'environnement et le catalogue de modèles est généré en direct |
| cline | `CLINE_PROVIDER_SETTINGS_PATH` lié au cycle de vie, supprimé à la sortie |
| openclaw | TUI locale intégrée, avec configuration limitée au processus et SecretRef basée sur l'environnement |
| continue | `CONTINUE_GLOBAL_DIR/config.yaml` lié au cycle de vie ; les références de secrets Continue passent par l'environnement |
| kilo | `KILO_CONFIG_CONTENT` limité au processus ; fournisseur compatible OpenCode avec clé issue de l'environnement |
| pi | `PI_CODING_AGENT_DIR` isolé contenant `models.json` et la configuration du modèle choisi. Les `{extensions,skills,prompts,themes}` déjà présents dans votre `PI_CODING_AGENT_DIR` (par défaut `~/.pi/agent`) avant le lancement sont chargés par chemin absolu |
| pi-web | `providers.everyapi` fusionné dans le `PI_CODING_AGENT_DIR/models.json` *durable* (par défaut `~/.pi/agent`), afin que les sessions, la confiance projet, le modèle choisi et les modifications faites depuis le panneau Models survivent ; la clé de relais reste une référence d'environnement et n'est jamais écrite sur disque |
| vibe | `VIBE_HOME/config.toml` isolé ; fournisseur générique avec `api_key_env_var` |
| copilot | environnement BYOK officiel `COPILOT_PROVIDER_*` ; l'API de transport suit les capacités du modèle choisi |
| droid | fichier `--settings` officiel valable uniquement pour l'exécution, avec un seul modèle `custom:EveryAPI-0` et une clé issue de l'environnement |
| openhands | `--override-with-envs` avec `LLM_API_KEY`, `LLM_BASE_URL`, `LLM_MODEL` propres au processus |
| forge | `FORGE_CONFIG` isolé ; fige le fournisseur/modèle compatible OpenAI dans la configuration et l'environnement du processus |
| llxprt | home applicatif isolé et options d'exécution réservées `--provider openai`, `--baseurl`, `--model` |
| grok | env : `XAI_API_KEY`, `GROK_MODELS_BASE_URL` ; `GROK_HOME` isolé ; découverte de modèles en direct filtrée |
| qwen-code | env : `OPENAI_API_KEY`, `OPENAI_BASE_URL`, `OPENAI_MODEL` ; réglages utilisateur dans un `QWEN_HOME` limité au processus et `--auth-type=openai` figé |
| kimi-code | env : `KIMI_MODEL_API_KEY`, `KIMI_MODEL_BASE_URL`, `KIMI_MODEL_PROVIDER_TYPE`, `KIMI_MODEL_NAME` ; `KIMI_CODE_HOME` isolé avec alias de modèles générés |
| hermes | `HERMES_HOME/config.yaml` générée avec un fournisseur personnalisé nommé, `base_url` et `api_key` en ligne ; découverte de modèles en direct filtrée |
| librefang | `librefang start` natif. Il détache le démon et vous rend votre terminal (`librefang stop` pour l'arrêter). LibreFang résout vos identifiants EveryAPI courants à chaque requête |
| open-webui | lancé via `open-webui serve` avec `OPENAI_API_BASE_URLS`, `OPENAI_API_KEYS` et `ENABLE_PERSISTENT_CONFIG=false`, si bien que l'environnement du processus l'emporte sur toute configuration enregistrée ; `DATA_DIR` est fixé à `~/.open-webui` |
| deepseek-harness | l'UI officielle `dsh web` ; une entrée `llm-pi-ai.providers.everyapi` générée dans `$DSH_HOME/settings.yaml` (par défaut `~/.dsh`, mode `0700`) plus un `.credentials.yaml` en mode `0600` qui contient la clé |

Quelle variable chaque outil lit, s'il faut ajouter `/v1`, quel style d'en-tête d'authentification il attend : vous n'avez plus à le chercher.

**Choix de la clé de relais** : sans `--group`, le CLI résout la clé auto-group de votre compte —— l'unique clé qui route vers tous les groupes accessibles —— et la met en cache dans `credentials.json`. Les comptes sans clé auto (ou les paliers qui perdent l'accès à ce groupe) retombent sur la clé activée la plus récente. Utilisez `everyapi token switch` pour définir une autre clé par défaut, ou passez `--group <id>` pour une exécution ponctuelle sur un autre pool. Les surcharges de groupe ne sont jamais écrites dans ce cache. La clé utilisée détermine le catalogue ci-dessous : une clé figée sur un groupe ne verra que les modèles de ce groupe.

Une clé mise en cache lors d'une exécution précédente reste utilisée —— cette recherche est délibérément hors ligne et ne se re-sélectionne pas d'elle-même. Si `/model` n'affiche que les modèles d'un seul groupe, lancez `everyapi token switch` et choisissez `Auto` une fois.

**Sélection de modèle** : au lancement, EveryAPI récupère le catalogue en direct disponible pour la clé/le groupe sélectionné, retire les protocoles média/embedding incompatibles et injecte cet instantané dans le sélecteur natif de chaque client routé. Utilisez `/model` dans Claude Code, Codex, Qwen Code et Kimi Code ; `/model` ou `models` dans Grok ; `hermes model` dans Hermes. Les identifiants de modèles non-Claude sont représentés en interne via des alias compatibles Claude, mais affichés et envoyés en amont avec leur véritable identifiant.

Les outils dotés du contrat `ModelEnv` (Gemini, Aider, Goose, Crush, Cline, OpenClaw, Continue, Kilo, Pi, Vibe, GitHub Copilot CLI, Factory Droid, OpenHands, ForgeCode, LLxprt, Hermes, Qwen Code, Kimi Code) ouvrent le sélecteur EveryAPI. Passez `--model <id>` pour le sauter. En exécution non interactive, EveryAPI utilise de façon déterministe le premier modèle compatible. Les claude, codex et grok « purs » conservent leur propre comportement de modèle de démarrage. `antigravity` lance le `agy` natif avec l'authentification Google, et `librefang` utilise son propre processus d'identifiants EveryAPI. `pi-web`, `open-webui` et `deepseek-harness` servent des interfaces web : EveryAPI enregistre à l'avance le fournisseur et l'intégralité du catalogue compatible, et le modèle se choisit dans cette interface plutôt que via un sélecteur en terminal.

**Niveau de raisonnement** : après le modèle, `everyapi use codex` et `everyapi use pi` demandent avec quel niveau de raisonnement démarrer et mémorisent la réponse pour les exécutions suivantes —— on demande une fois, puis on réutilise sans confirmation, exactement comme pour les réglages de sécurité précédents. Les conditions diffèrent entre les deux clients parce que ce que nous savons diffère. Codex lit les paliers que son catalogue embarqué expose pour ce modèle (`low` … `ultra`, variable selon le modèle —— `gpt-5.6-sol` va jusqu'à `ultra`, `gpt-5.5` jusqu'à `xhigh`) et reçoit le choix sous forme de `model_reasoning_effort`. Il n'interroge pas la passerelle pour cela, donc l'étape n'apparaît pas pour les modèles que Codex ne connaît pas. Pi n'a pas de table par modèle pour les fournisseurs personnalisés, donc l'étape n'apparaît que lorsque la passerelle confirme que le modèle accepte un effort (`supports_thinking` dans `/v1/models`) ; les choix vont de `off` à `high` et sont transmis via `defaultThinkingLevel`. Un niveau mémorisé que le modèle actuel ne propose pas est abandonné plutôt que figé. À la première exécution après cette fonctionnalité, le curseur démarre sur l'effort déjà présent dans le home persistant de Codex : accepter la valeur par défaut ne change donc rien. Les contrôles en session des deux clients —— `/model` dans Codex, shift+tab dans pi —— restent disponibles ; le lanceur ne fait que conserver votre choix d'une exécution à l'autre, car le profil généré par Codex et le home isolé de Pi sont supprimés à la sortie.

Les noms de fournisseurs ne sont pas des noms de CLI : utilisez `qwen-code` ou `kimi-code` pour les clients officiels de ces fournisseurs, et choisissez leurs modèles dans le catalogue en direct de n'importe quel client pris en charge.

**Isolation de configuration hermes** : `everyapi use hermes` redirige `HERMES_HOME` vers un répertoire limité au processus sous `~/.config/everyapi/sessions`. La configuration porteuse d'identifiants et l'URL du proxy en direct sont supprimées à la sortie et ne peuvent entrer en collision avec une autre clé ou un autre groupe. Seul l'identifiant du dernier modèle choisi persiste, un réglage sans risque. Votre `~/.hermes` personnel n'est pas touché. La configuration générée enregistre EveryAPI comme fournisseur personnalisé nommé, afin que `hermes model` puisse découvrir et changer de modèle sans retomber sur OpenRouter. Un `hermes` « pur » ouvre le chat interactif ; utilisez `everyapi use hermes -- --tui` si vous voulez l'interface terminal.

**Isolation de configuration grok** : `everyapi use grok` redirige `GROK_HOME` vers `~/.config/everyapi/grok-home`. Cela empêche une session navigateur xAI en cache d'écraser votre clé de relais EveryAPI et sépare les sessions routées par EveryAPI d'un `grok` pur. Passez les options propres à Grok après `--`, par exemple `everyapi use grok -- --model grok-4.5`.

**Isolation de configuration Qwen/Kimi** : chaque exécution routée reçoit un home limité au processus sous `~/.config/everyapi/sessions`, supprimé à la fin du processus enfant —— des clés ou groupes utilisés en parallèle ne peuvent donc pas s'écraser mutuellement catalogue ou URL de loopback. La véritable configuration système de Qwen reste intacte et les priorités administrateur sont préservées. Si une configuration administrateur ou de workspace définit `modelProviders.openai` et masquerait le catalogue EveryAPI en direct, l'exécution échoue sur un conflit exploitable au lieu d'afficher silencieusement des modèles obsolètes ou incompatibles.

> ⚠️ **Note de sécurité sur l'environnement du sous-processus** : les variables d'environnement ci-dessus contiennent votre clé d'API de relais. Les CLI tiers peuvent journaliser l'environnement en mode debug/verbose —— avant de lancer `everyapi use`, vérifiez que l'option de debug que vous activez ne laisse pas fuiter `*_TOKEN` / `*_API_KEY`. Passez `sed -i 's/sk-everyapi-[A-Za-z0-9]*/REDACTED/g'` sur les journaux de debug avant de les partager.

#### Connecteur transparent (par défaut)

Le mode transparent maintient les clients pris en charge sur l'Origin officiel de l'API du fournisseur au lieu de configurer une URL de base tierce. C'est le comportement par défaut pour tous les outils qui le supportent ; passez `--transparent=false` pour le désactiver. Le CLI démarre un proxy HTTP CONNECT éphémère sur un port loopback aléatoire et génère une CA par exécution dont la clé privée n'existe qu'en mémoire. Seuls l'URL du proxy, le bundle public de la CA et des identifiants d'espace réservé non secrets sont transmis au processus enfant. Les chemins de modèles enregistrés sont déchiffrés localement et relayés vers EveryAPI avec votre véritable clé de relais ; tous les autres hôtes HTTPS utilisent un CONNECT en pur passe-plat. Les chemins inconnus sous un préfixe de modèle protégé sont bloqués, et un échec de relais ne retombe jamais sur le fournisseur.

Vérifié avec Claude Code et le CLI Codex, les deux outils où le comportement par défaut s'applique aussi. Antigravity natif et LibreFang contournent le connecteur. Les autres outils enregistrés utilisent leurs chemins documentés d'injection ou de configuration, donc passer explicitement `--transparent` à un outil non pris en charge échoue de façon visible.

`--sanitize` n'entre pas en conflit avec le mode transparent, il s'y combine : le connecteur relaie à travers l'assainisseur (enfant → connecteur → assainisseur → passerelle), de sorte que le masquage et les garde-fous de réponse de récupération de Claude s'appliquent aux deux chemins d'exécution.

Si `ALL_PROXY` est votre seule variable de proxy, le mode transparent est refusé et l'on retombe sur le chemin d'injection —— la résolution de proxy de Go ne lit pas `ALL_PROXY`, le connecteur ne peut donc pas la respecter. Définissez `HTTPS_PROXY` (y compris socks5, auquel net/http se connecte nativement) si vous voulez conserver le mode transparent.

Ce mode est expérimental et délibérément limité au processus :

- le côté client que nous interceptons parle actuellement HTTP/1.1 et prend en charge les requêtes JSON/SSE normales (les réponses HTTP/2 de la passerelle sont traduites en HTTP/1.1). HTTP/2 côté client, HTTP/3/QUIC, WebSockets, clients à épinglage de certificat et clients qui ignorent `HTTPS_PROXY` sont hors périmètre ;
- le fournisseur OpenAI intégré de Codex sonde une fois le WebSocket Responses. Le connecteur renvoie HTTP 426, donc Codex retombe immédiatement sur HTTPS/SSE sans consommer son budget de réessais ; Codex peut afficher une ligne de journal pour cette sonde échouée ;
- Claude Code traite toujours l'espace réservé non secret comme une authentification par clé d'API, donc les connecteurs claude.ai sont désactivés même si `ANTHROPIC_BASE_URL` est l'Origin officiel `https://api.anthropic.com`. Le mode transparent évite la détection d'un Origin tiers ; il ne peut pas faire passer une authentification par clé d'API pour une connexion OAuth claude.ai ;
- il n'installe aucune CA système, ne demande aucun privilège administrateur et ne change pas le comportement par défaut de `everyapi use` ;
- il n'est pas indétectable : un client peut inspecter les variables de proxy, les chaînes de certificats locales, les sockets, le timing et les différences de réponse ;
- le connecteur voit le contenu déchiffré des modèles. La clé de signature de la CA n'est jamais persistée ni téléversée, et le fichier public de la CA est supprimé à la sortie ;
- votre clé de relais n'est ni dans l'environnement du processus enfant ni dans la configuration générée du client, mais un `~/.config/everyapi/credentials.json` préexistant reste lisible par tout processus s'exécutant sous le même utilisateur système. Le mode transparent est une isolation de l'injection d'identifiants, pas un bac à sable contre des processus enfants hostiles.

### `everyapi auth login` —— Device Authorization Grant + connexion par QR

Utilise le Device Authorization Grant (style RFC 8628) + la couche 1 de docs §7-5, « connexion par QR entre appareils » :

1. Le CLI crée une session et **affiche un QR code dans le terminal**, en plus du code court et de l'URL
2. Scannez le QR avec votre téléphone (ou ouvrez l'URL dans un navigateur où vous êtes déjà connecté à EveryAPI) —— l'URL contenue dans le QR porte déjà `?code=USR-789`, le tableau de bord remplit donc le code automatiquement et l'utilisateur n'a plus qu'à cliquer sur Approve
3. Le CLI reçoit le jeton d'accès et l'enregistre dans `~/.config/everyapi/credentials.json` (mode 0600)

```bash
everyapi auth login                                    # production ; affiche le QR et ouvre le navigateur par défaut
everyapi settings set gateway_region cn               # utiliser la passerelle accélérée pour la Chine pour les commandes suivantes
everyapi auth login --api-base http://localhost:8787   # développement local / auto-hébergement
everyapi auth login --no-browser                       # ne pas ouvrir le navigateur (scannez le QR)
everyapi auth login --no-qr                            # ne pas afficher le QR (terminaux non UTF-8 / sortie redirigée)
```

Exemple de QR rendu dans le terminal (caractères Unicode demi-bloc, environ 18-20 lignes de haut) :

```
█▀▀▀▀▀█  ▀▀ ▄  █▀▀▀▀▀█
█ ███ █  ▀▄█▀  █ ███ █
█ ▀▀▀ █ ▄ ▀ █▀ █ ▀▀▀ █
▀▀▀▀▀▀▀ █▄█▄█▄ ▀▀▀▀▀▀▀
... (le vrai QR encode verification_uri?code=USR-789)
```

Pourquoi ce chemin résiste mieux à l'hameçonnage :

- l'utilisateur **ne saisit aucun mot de passe sur le nouvel appareil** → un site d'hameçonnage n'a nulle part où capter des identifiants
- l'utilisateur **n'est pas redirigé vers une page de navigateur inconnue** → la surface d'hameçonnage par redirection disparaît
- même si le CLI était un fork malveillant générant un faux QR, la page d'approbation après le scan est le véritable tableau de bord everyapi.ai (déclenché depuis un appareil où vous êtes déjà connecté), et un utilisateur n'approuve pas un code qu'il ne reconnaît pas

Les autres couches de docs §7-5 (épinglage de certificat / phrase / OAuth PKCE) ont été livrées dans des PR distinctes (l'épinglage est en mode rapport uniquement : la décision produit est de ne pas bloquer).

### `everyapi seller <sub>` —— sous-commandes vendeur de la marketplace

Elles amènent dans le terminal l'enregistrement de canaux et le flux de retrait du tableau de bord, pour permettre un onboarding scriptable. Avant d'enregistrer un canal, `seller setup` vérifie l'éligibilité (compte actif / e-mail vérifié / ancienneté du compte / historique de dépenses / plafond de canaux) et liste les conditions en échec **avant que l'utilisateur saisisse une clé** —— pour ne pas le découvrir via un 422 après soumission.

```bash
everyapi seller list                          # lister les canaux enregistrés
everyapi seller withdraw                      # transférer tous les gains vendeur en attente vers le solde principal
everyapi seller withdraw --quota 1000         # transfert partiel (unités de la base de données)
everyapi seller add-key   --type claude --name 'my-pro' \
                        --key 'sk-ant-...' --models 'claude-3-opus,claude-3-sonnet'
everyapi seller add-oauth codex --name 'my-chatgpt' --models 'gpt-4'
                                            # OAuth en un clic : le CLI démarre le device flow, l'utilisateur saisit
                                            # le user_code dans le navigateur et le jeton atterrit sur le canal
everyapi seller add-oauth claude --name 'my-claude' --models 'claude-3-opus,claude-3-sonnet'
                                            # flux par collage : le CLI ouvre la page d'autorisation d'Anthropic,
                                            # l'utilisateur colle dans le terminal le code#state affiché au callback
everyapi seller add-oauth gemini --name 'my-gemini' --models 'gemini-1.5-pro'
                                            # vrai loopback en un clic : le CLI ouvre un listener sur un port
                                            # aléatoire, Google livre le code directement au CLI, rien à coller
everyapi seller setup                         # assistant interactif : vérifie d'abord l'éligibilité, puis guide add-key
```

#### `add-key` —— pool de clés de secours

`--key` peut être répété pour enregistrer N identifiants équivalents sur le même canal en tant que pool de secours (B2, PRODUCT §4.5). Si la clé principale renvoie 401/403, le backend bascule automatiquement sur la suivante. `--key-remark` est également répétable et s'apparie par position avec `--key` (le i-ème `--key-remark` étiquette la i-ème `--key`, pour les identifier plus tard dans le tableau de bord). Les blobs OAuth ne peuvent pas former de pool de secours —— ils restent des canaux à clé unique.

```
everyapi seller add-key   --type claude --name 'claude-pool' \
                        --models 'claude-3-opus' \
                        --key 'sk-ant-primary' --key-remark 'primary' \
                        --key 'sk-ant-backup'  --key-remark 'team backup'
```

Le `--type` de `add-key` accepte des alias (`openai` / `claude` / `gemini` / `codex` / `vertex` / `aws` / `xai` / `deepseek`) ou l'identifiant numérique. L'enregistrement est soumis aux conditions d'éligibilité de la marketplace (compte actif, e-mail vérifié, historique de dépenses, plafond de canaux), et le CLI liste la check-list en échec avant toute autre chose, sur les trois points d'entrée (`add-key` / `add-oauth` / `setup`).

#### `add-oauth codex` —— OAuth en un clic (device flow)

`everyapi seller add-oauth codex --name 'my-chatgpt' --models 'gpt-4'` déroule le flux d'autorisation d'appareil façon RFC 8628 de Codex / ChatGPT —— le vendeur **ne touche à aucune chaîne de jeton** :

1. Le CLI appelle `/api/seller/codex/device/start` et reçoit un `user_code` court et une `verification_uri`
2. Le CLI ouvre `https://auth.openai.com/codex/device` dans le navigateur par défaut (`--no-browser` l'évite). L'utilisateur saisit le `user_code` dans le navigateur pour terminer l'autorisation
3. Le CLI interroge `/api/seller/codex/device/poll`. Une fois autorisé, le backend crée le canal et écrit le jeton OAuth dans son champ `key`
4. Sortie : identifiant du canal + l'e-mail ChatGPT associé

Les cookies d'autorisation sont gérés par un `http.CookieJar` en mémoire et ne sont pas persistés —— l'état du device flow est éphémère et lié au processus, ce qui correspond au modèle de menace.

#### `add-oauth claude` —— OAuth par coller-soumettre

`everyapi seller add-oauth claude --name … --models …`. Le fournisseur OAuth d'Anthropic fige de son côté la `redirect_uri` sur `https://console.anthropic.com/oauth/code/callback`, le CLI ne peut donc pas recevoir le callback via un listener localhost. Le flux :

1. Le CLI appelle `/api/seller/claude/oauth/start`. Le backend génère la paire PKCE + state et renvoie l'URL d'autorisation d'Anthropic
2. Le CLI ouvre le navigateur par défaut (`--no-browser` l'évite). L'utilisateur se connecte à Anthropic et approuve
3. Anthropic redirige vers une page de callback qui affiche une chaîne `<code>#<state>`
4. **L'utilisateur colle cette chaîne dans le CLI**
5. Le CLI appelle `/api/seller/claude/oauth/complete`. Le backend échange code+verifier contre le jeton et crée le canal

Une étape de collage de plus que le device flow, mais bien plus simple que d'aller chercher `~/.claude/auth.json` à la main. Le cookie de session est émis par le backend au démarrage et le complete doit atteindre la même session —— le `http.CookieJar` du CLI vit dans le processus et est isolé par invocation.

#### `add-oauth gemini` —— véritable OAuth loopback en un clic

`everyapi seller add-oauth gemini --name … --models … [--no-browser] [--timeout 5m]`. Le client OAuth d'application installée de gemini-cli chez Google accepte `http://127.0.0.1:<port>/callback` comme `redirect_uri`, donc **le CLI ouvre son propre listener pour recevoir le callback** —— l'utilisateur se contente de se connecter dans le navigateur, rien à coller. Le flux :

1. Le CLI ouvre un serveur HTTP à usage unique sur un port éphémère aléatoire (`127.0.0.1:0`), chemin fixé à `/callback`
2. Le CLI appelle `/api/seller/gemini/oauth/start` avec `redirect_uri = http://127.0.0.1:<port>/callback`. Le backend valide strictement la redirection : loopback / port ≥ 1024 / schéma http / chemin /callback / ni query, ni fragment, ni userinfo (prévient le SSRF et le détournement de redirection)
3. Le CLI ouvre le navigateur par défaut. L'utilisateur se connecte à Google et consent
4. Google redirige avec `?code=…&state=…` vers le listener du CLI
5. Le CLI vérifie la correspondance du state (protège contre les flux périmés ou falsifiés) et appelle `/api/seller/gemini/oauth/complete`
6. Le backend échange le code + la même redirect_uri contre le jeton et crée le canal

Comparaison avec les deux autres fournisseurs :

| Fournisseur | UX | Raison |
|---|---|---|
| `codex` | l'utilisateur saisit un user_code de 6 caractères dans le navigateur, le CLI interroge automatiquement | device flow d'OpenAI, pas de redirect_uri |
| `claude` | l'utilisateur se connecte dans le navigateur et colle `code#state` dans le CLI | Anthropic fige la redirect_uri sur sa propre URL de callback |
| `gemini` | l'utilisateur se connecte dans le navigateur, ferme l'onglet, c'est fini | Google autorise les redirections loopback |

`--timeout` borne l'attente (5 minutes par défaut). À l'expiration, le CLI se termine et ferme proprement le listener.

### `everyapi edge <sub>` —— déploiement en une commande de l'agent fournisseur BYO-GPU

Permet de vendre vos GPU inutilisés via EveryAPI. Le CLI comprime le déploiement en un seul jeu de commandes —— `register` / `list` / `start` / `status` / `logs` / `models` / `rename` / `pause` / `resume` / `stop` / `update` / `remove` —— pour que les fournisseurs n'aient pas à copier un docker-compose à la main, remplir un `.env` ni jongler avec des jetons d'enregistrement. Le chemin habituel tient en huit commandes :

```bash
everyapi auth login                              # réutilise votre session existante
everyapi edge register --name "rtx-4090"    # appelle /api/seller/edge/nodes pour obtenir node_id + jeton, écrit ~/.local/share/everyapi/edge/<id>/
everyapi edge start                         # détecte NVIDIA / ROCm / Apple Silicon / CPU, docker compose up -d
everyapi edge models pull llama3.1:8b       # docker compose exec ollama ollama pull ...
everyapi edge status                        # docker compose ps local + état online/offline du tableau de bord
everyapi edge logs -f                       # suivre les journaux
everyapi edge update                        # docker compose pull && up -d
everyapi edge remove                        # down -v + suppression du répertoire local + DELETE côté backend
```

`start` génère le `docker-compose.yml` à l'exécution avec `text/template` (**pas un YAML statique embarqué**) —— ainsi les noms de conteneurs sont préfixés par le node_id, plusieurs nœuds sur un même hôte n'entrent pas en collision, et le passthrough GPU est rendu conditionnellement selon le mode (NVIDIA = `deploy.resources.devices` + pilote nvidia, ROCm = `/dev/kfd` + `/dev/dri` + `group_add: video`, macOS = pas de conteneur ollama, l'agent se connecte à l'ollama natif de l'hôte via `host.docker.internal`).

Flux d'identifiants : le CLI appelle `POST /api/seller/edge/nodes` avec votre Bearer `sk-everyapi-` existant → le backend renvoie un `registration_token` une seule fois (ensuite il ne conserve que le sha256 et ne le réaffiche jamais) → le CLI l'écrit en mode 0600 dans `~/.local/share/everyapi/edge/<id>/node.json` → il est rendu comme variable `EVERYAPI_REGISTRATION_TOKEN` dans le compose. **Le jeton d'enregistrement n'est écrit dans aucun fichier .env** (pour que les fournisseurs ne le commitent pas par accident).

Prérequis : `docker` + `docker compose v2` (v1 est en fin de vie et non pris en charge). Sur macOS : `brew install ollama && brew services start ollama` (l'accélération Metal ne fonctionne pas dans un conteneur docker).

### `everyapi wallet topup` —— redirection de rechargement avec phrase anti-hameçonnage

`everyapi wallet topup` ouvre la page de rechargement du tableau de bord. Avant de rediriger, la vérification de la couche 3 de docs §7-5 s'applique :

1. Le CLI appelle le backend `POST /api/cli/jump-session` et reçoit un identifiant de session + une phrase de 4 emojis (par ex. `🌊 🦊 🍕 🚀`)
2. Le CLI affiche à la fois l'URL et la phrase dans le terminal et précise : « dans un instant, vous devriez voir cette même phrase en haut de la page »
3. L'utilisateur appuie sur Entrée et le CLI ouvre l'URL dans le navigateur système (avec `?jump_session=<id>`)
4. Au chargement, le tableau de bord appelle le backend `GET /api/cli/jump-session/:id/phrase`, reçoit la même phrase et **l'affiche bien en évidence dans l'en-tête de la page**
5. L'utilisateur compare visuellement : correspondance → c'est le vrai EveryAPI ; pas de correspondance ou rien d'affiché → fermez l'onglet (hameçonnage possible)

Pourquoi cela freine l'hameçonnage : la phrase vit en mémoire du backend, indexée par un identifiant de session aléatoire de 32 caractères hexadécimaux. Un site d'hameçonnage n'a aucun chemin authentifié pour la récupérer, et un `wallet/topup?jump_session=<id>` falsifié par un attaquant ne peut pas la lire non plus. Un TTL court (10 minutes) + un usage unique (la session est supprimée dès que le tableau de bord la récupère) réduisent encore le risque de réutilisation.

```bash
everyapi wallet topup                    # ouvre le navigateur par défaut
everyapi wallet topup --no-browser       # affiche seulement l'URL à copier manuellement
```

### `everyapi auth status` —— solde / consommation / quota actuels

```
$ everyapi auth status

  alice (alice@example.com)
  quota:     $12.34 remaining   $5.67 used
  requests:  1,234
  topup:     https://app.everyapi.ai/wallet
```

### `everyapi version update` —— exécute la mise à jour pour vous

Il n'existe pas de `everyapi update` de premier niveau ; les actions liées au cycle de vie du CLI se trouvent sous `version` (`everyapi version update`, `everyapi version uninstall`).

Consulte la dernière release du miroir GitHub, la compare à votre version actuelle, puis confie la mise à jour à ce qui a installé le binaire —— Homebrew (`brew update && brew upgrade everyapi`), `go install …@latest` ou le script d'installation publié. Une commande, sans copier-coller.

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

Pourquoi ne pas remplacer le binaire directement ? Parce que les chaînes de vérification de Homebrew et de Go (SHA / signatures de bottles / somme de contrôle de module) sont plus robustes que tout ce que nous reconstruirions dans le CLI, et qu'un exécutable qui se remplace lui-même pendant son exécution est un champ de mines sous Windows. Une installation faite par script est bien remplacée sur place —— mais en relançant l'installateur publié, qui fait déjà cela correctement.

Options :
- `--check` —— compare silencieusement. Sort avec 0 si à jour, 1 si obsolète, 2 si la dernière version n'a pas pu être déterminée (raison sur stderr) —— une coupure réseau ne doit pas se lire comme « une mise à jour est disponible ». Pour CI / cron :
  ```bash
  everyapi version update --check || echo "needs upgrade"
  ```
- `--dry-run` —— affiche les commandes qui seraient exécutées sans les exécuter (pour confirmation)

### `everyapi settings` —— préférences du CLI (langue, etc.)

Le CLI embarque l'i18n pour 8 langues : anglais, chinois simplifié, chinois traditionnel, japonais, coréen, espagnol, allemand et français —— les chaînes du CLI s'affichent dans la langue choisie. Les erreurs de l'API backend sont négociées automatiquement via l'en-tête `Accept-Language` et couvrent les mêmes 8.

```bash
$ everyapi settings                          # sélecteur interactif : choisir la langue
$ everyapi settings list                     # voir les réglages actuels
$ everyapi settings set language zh          # définir directement
$ everyapi settings set language fr          # idem pour le français
$ everyapi settings set terminal_mode tmux   # garder les lancements interactifs dans tmux
$ everyapi use codex -- resume               # se rattacher à l'unique tmux du projet, ou ouvrir le sélecteur de Codex
$ everyapi settings reset                    # revenir aux valeurs par défaut (en + détection auto de LANG)
```

**Mode terminal** : le premier `everyapi use` interactif demande si les lancements doivent se faire dans votre terminal natif ou dans tmux, et enregistre le choix dans `terminal_mode`. Le mode tmux relance tout le processus `everyapi use` à l'intérieur d'une session `everyapi-v3-*` identifiée par l'outil choisi, l'identité du système de fichiers de l'espace de travail et une identité de lancement aléatoire de 128 bits, de sorte que le connecteur, l'assainisseur, la configuration temporaire et l'outil cible survivent à un détachement. Le message de lancement affiche la commande `tmux attach -t <session>` exacte. Un `resume` Codex « pur » cherche d'abord cette identité : s'il existe exactement un panneau d'agent géré vivant, il est revalidé par nom de session exact et rattaché ; à zéro ou plusieurs, il ne devine pas et retombe sur le sélecteur de reprise normal de Codex. Avant chaque lancement tmux, le CLI ne considère comme candidates que des sessions `everyapi-v3-*`, `everyapi-v2-*` ou héritées `everyapi-<pid>-<timestamp>` générées strictement, et ne les supprime que si une unique commande tmux atomique revalide que la session contient exactement une fenêtre avec exactement un panneau d'enveloppe EveryAPI déjà mort. Les agents détachés encore vivants, les sessions tmux ordinaires créées par l'utilisateur et toute session comportant des panneaux ou fenêtres ajoutés par l'utilisateur sont toujours préservés. Une session dont le panneau géré est mort mais qui conserve des panneaux ajoutés par l'utilisateur encore vivants est préservée mais pas réutilisée. Chaque client lancé peut consulter `EVERYAPI_TERMINAL_MODE`, `EVERYAPI_TMUX_SESSION` et `EVERYAPI_TMUX_ATTACH_COMMAND`. Codex, Claude Code, OpenCode et Kilo reçoivent en plus le même contexte de session via leur surface documentée d'instructions au modèle, y compris la règle de ne pas créer de sessions tmux imbriquées. Les autres clients conservent uniquement le contrat d'environnement, sans injection de message utilisateur. Un lancement déjà dans tmux ne s'imbrique pas, et les lancements non interactifs restent toujours natifs. Si tmux n'est pas disponible, le sélecteur de première utilisation désactive cette option. Si votre configuration tmux existante entre en conflit, l'exécution échoue avec des instructions d'installation ou de retour arrière plutôt que de changer le comportement en silence.

**Détection automatique** : si vous n'avez rien défini explicitement, le CLI lit au démarrage les variables d'environnement dans cet ordre : `EVERYAPI_LANG > LC_ALL > LC_MESSAGES > LANG`. Si votre locale système est `zh_CN.UTF-8` / `ja_JP.UTF-8` / `fr_FR.UTF-8`, etc., cela s'applique immédiatement —— sans configuration.

**Surcharge ponctuelle** :

```bash
EVERYAPI_LANG=zh everyapi auth status             # cet appel uniquement en chinois, non enregistré
```

**Exemple de traduction** (erreur « non connecté », 8 langues × la même phrase) :

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

Les réglages sont enregistrés dans `~/.config/everyapi/settings.json` (même répertoire que `credentials.json`, mais en mode `0644` —— aucun secret dedans).

**Améliorer les traductions / ajouter une langue** : voir [`internal/i18n/locales/README.md`](../internal/i18n/locales/README.md).

## Fichiers de configuration

Les identifiants sont stockés dans `~/.config/everyapi/credentials.json` (ou `$XDG_CONFIG_HOME/everyapi/` si `$XDG_CONFIG_HOME` est défini) avec le mode de fichier `0600`. Écrit par `everyapi auth login`, lu par toutes les autres commandes.

> ⚠️ **Le jeton est stocké en clair**. Le mode `0600` + un chemin privé sous `$HOME` correspondent à la pratique des CLI du secteur comme `gh auth` ou `aws configure`, mais **dans un modèle de menace de vol d'appareil domestique ou de malware**, tout processus capable de lire ce fichier peut appeler l'API EveryAPI en votre nom (y compris les outils MCP —— voir l'étape de friction du chemin monétaire plus bas). Recommandations :
> - ne lancez pas `everyapi auth login` sur des machines partagées ou publiques
> - sur macOS : envisagez `everyapi auth logout` avant d'activer FileVault
> - sur Linux : activez le chiffrement du répertoire personnel (`ecryptfs` / LUKS)
> - en cas de suspicion de fuite → `everyapi auth logout` efface immédiatement les identifiants locaux, puis faites tourner votre clé d'API dans le tableau de bord EveryAPI
>
> Les backends de trousseau de la plateforme (macOS Keychain / Windows DPAPI / Linux Secret Service) sont prévus mais pas encore livrés.

Champs :

- `api_base` —— URL de la passerelle EveryAPI. Par défaut `https://api.everyapi.ai`. Les utilisateurs auto-hébergés et le développement local peuvent la surcharger avec `--api-base` sur `auth login`.
- `access_token` —— le bearer utilisé pour tous les appels d'API authentifiés.
- `relay_key` —— la clé d'API de relais (`sk-everyapi-…`), utilisée dans l'environnement du sous-processus de `everyapi use`. Récupérée depuis `/api/token/*` et mise en cache ici.
- `user_id` / `username` —— mis en cache pour que `auth status` puisse afficher la ligne d'identité avant le premier aller-retour d'API.

La région de la passerelle est une préférence du CLI dans `settings.json` : si elle n'est pas définie, la connexion interactive pose la question une fois et enregistre le choix. `everyapi settings set gateway_region cn` dirige le trafic de la passerelle officielle vers `https://api-cn.everyapi.ai`, et `global` utilise `https://api.everyapi.ai`. Une `--api-base` personnalisée pour l'auto-hébergement reste prioritaire.

## Développement

Depuis le répertoire source du CLI (là où se trouvent ce README, `go.mod` et le `Makefile`) :

```bash
go test ./...
go run . auth status       # contre la production
go run . auth login --api-base http://localhost:8787   # contre un backend local
```

Compilation croisée locale pour toutes les plateformes (même recette que la CI) :

```bash
make cli-release           # artefacts dans dist/ (6 plateformes × 1 binaire = 6 fichiers)
```

## Serveur MCP (sous-commande `everyapi mcp`)

Le binaire `everyapi` **embarque** un serveur [Model Context Protocol](https://modelcontextprotocol.io) —— exposé comme sous-commande (`everyapi mcp` lit sur stdin et écrit sur stdout). Un agent IA (Claude Code / Cursor / Codex CLI / n'importe quel client MCP) peut l'appeler directement, **sans que l'utilisateur ouvre un terminal**.

> ⚠️ **Modèle d'authentification et surface d'exposition du serveur MCP**
>
> - **N'ouvre aucun port** : `everyapi mcp` est du JSON-RPC stdio pur, forké par le CLI hôte. **Il n'écoute sur aucun socket ni port TCP** —— surface réseau nulle.
> - **Lit directement `~/.config/everyapi/credentials.json`** : le serveur MCP n'a pas de flux d'authentification propre, donc pouvoir lire le fichier d'identifiants = pouvoir appeler tous les outils exposés en votre nom. Tout hôte MCP capable de lancer un processus avec vos droits utilisateur a un accès complet.
> - **Le chemin monétaire `everyapi_seller_withdraw` comporte une étape de friction** : l'appelant doit passer `confirm: "yes"`, ce qui garantit qu'un agent IA expose l'action de transfert à un humain dans l'interface et prévient les fuites de fonds silencieuses. Les autres outils en lecture seule (status / topup / seller_list) ne l'exigent pas.
>
> N'installez pas d'hôtes MCP auxquels vous ne faites pas confiance.

### Installation

C'est le même binaire que le CLI : si vous installez le CLI, vous avez déjà le serveur MCP :

```bash
make cli                                              # build local, produit ./bin/everyapi
# ou via go install :
go install github.com/everyapi-ai/everyapi-ai/v3@latest
```

### Branchement avec Claude Code

Ajoutez dans `~/.claude/settings.json` :

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

Le branchement avec Cursor, le CLI Codex et d'autres clients MCP est similaire : pointez `command` vers le binaire `everyapi` et mettez `args: ["mcp"]`.

### Prérequis d'authentification

Vous devez lancer `everyapi auth login` dans un terminal au moins une fois —— le serveur MCP est un processus d'arrière-plan sans interaction terminal, il ne peut donc pas mener seul le flux device-code. Il lit directement `~/.config/everyapi/credentials.json` ; si le fichier n'existe pas, tous les outils renvoient un message « not logged in » avec `isError: true` qui guide l'utilisateur vers la connexion.

### Outils exposés (15 au total)

| Outil | Entrée | À quoi ça sert |
|---|---|---|
| `everyapi_status` | aucune | solde / consommation / nombre de requêtes actuels |
| `everyapi_topup` | aucune | renvoie l'URL web de rechargement |
| `everyapi_seller_list` | aucune | liste les canaux vendeur de la marketplace |
| `everyapi_seller_withdraw` | `{confirm: "yes", quota?: int}` | transfère seller_quota vers le solde principal ; **exige `confirm: "yes"`** (friction du chemin monétaire) |
| `everyapi_seller_eligibility` | aucune | checklist en lecture seule de la porte de montage (marketplace ouverte, compte actif, e-mail vérifié, ancienneté du compte, usage antérieur, plafond de canaux). Appelez-la *avant* de demander une clé à l'utilisateur |
| `everyapi_seller_add_key` | `{name, type, keys[], models, key_remarks?[], remark?}` | monte un canal vendeur à partir de clés API en clair —— le jumeau de `everyapi seller add-key`. Ne transmettez que des clés fournies par l'utilisateur dans la conversation |
| `everyapi_seller_add_oauth_codex_start` | `{name, models}` | démarre le flux d'autorisation d'appareil Codex / ChatGPT, renvoie `user_code` + `verification_uri` + `flow_id` |
| `everyapi_seller_add_oauth_codex_poll` | `{flow_id}` | interroge l'état d'autorisation Codex. `pending`/`slow_down` = continuer à interroger, `authorized` renvoie l'identifiant du canal, `expired`/`denied` termine |
| `everyapi_seller_add_oauth_claude_start` | `{name, models}` | démarre le flux OAuth Anthropic, renvoie `authorize_url`. L'utilisateur se connecte dans le navigateur et obtient une chaîne `<code>#<state>` |
| `everyapi_seller_add_oauth_claude_complete` | `{input}` | soumet la chaîne `<code>#<state>` collée par l'utilisateur à l'étape précédente et crée le canal |
| `everyapi_edge_list` | aucune | liste les nœuds edge BYO-GPU : id, nom, état en ligne, canal associé, dernière connexion, modèles installés |
| `everyapi_edge_status` | `{node_id: int}` | détail d'un nœud —— indicateur de pause, version de l'agent, modèle / nombre / VRAM des GPU, modèles installés |
| `everyapi_edge_remove` | `{node_id: int, confirm: "yes"}` | supprime un nœud (et son canal associé si c'était le dernier) ; **exige `confirm: "yes"`** (friction des chemins destructifs) |
| `everyapi_admin_marketplace_status` | aucune | lit l'indicateur `marketplace.enabled` de tout le déploiement. Rôle admin requis |
| `everyapi_admin_marketplace_set` | `{enabled: bool, confirm: "yes"}` | ouvre ou ferme la marketplace pour tout le déploiement ; **exige `confirm: "yes"`**. Les nœuds et canaux existants continuent de servir lorsqu'elle est fermée |

**Schéma d'utilisation des outils OAuth** (voilà comment un agent IA le mène dans la conversation) :

```
Utilisateur : ajoute un canal vendeur ChatGPT Plus, nom my-chatgpt, models gpt-4
IA          → everyapi_seller_add_oauth_codex_start({name: "my-chatgpt", models: "gpt-4"})
             ← « saisissez USR-789 sur chatgpt.com/codex et prévenez-moi quand c'est fait »
Utilisateur : c'est fait, dans le navigateur
IA          → everyapi_seller_add_oauth_codex_poll({flow_id: "..."})
             ← « status=pending, patientez quelques secondes »
[continue d'interroger jusqu'à authorized]
             ← « status=authorized — channel #314 mounted »

Utilisateur : ajoute aussi celui de Claude Pro, my-claude / claude-3-opus
IA          → everyapi_seller_add_oauth_claude_start({...})
             ← « terminez l'autorisation sur [URL] puis donnez-moi la chaîne code#state »
Utilisateur : code-abc123#state-xyz
IA          → everyapi_seller_add_oauth_claude_complete({input: "code-abc123#state-xyz"})
             ← « Channel #315 mounted »
```

L'OAuth Gemini (flux loopback) **n'est pas exposé via MCP** —— le cycle de vie du listener loopback ne correspond pas à un cycle de vie réparti sur plusieurs appels d'outils. Pour Gemini, on garde `everyapi seller add-oauth gemini` dans le CLI.

### Test de fumée manuel

```bash
make cli
./bin/everyapi mcp <<'EOF'
{"jsonrpc":"2.0","id":1,"method":"initialize"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"everyapi_status","arguments":{}}}
EOF
```

Vous devriez voir 3 lignes de réponse JSON : le résultat d'initialize, une liste de 15 outils et le texte de status (ou un isError « non connecté »).

## Ce que ce binaire n'inclut **PAS** encore

**Non implémenté** pour l'instant (par ordre d'importance ; ajouté progressivement dans les releases suivantes sans casser la surface v1) :

- ⚠️ Signature de code au niveau système (notarisation macOS / Authenticode Windows) —— aujourd'hui nous reposons sur la double vérification sigstore cosign keyless + SHA256SUMS, tous deux joints à chaque GitHub Release et vérifiés automatiquement par Homebrew
- ❌ Backends de trousseau de la plateforme —— le jeton reste écrit en clair sur le disque (mode 0600)

Ce qui était listé ici mais est **déjà livré** (ne le traitez pas comme en attente) :

- ✅ Proxy d'assainissement local —— les commandes sont `everyapi proxy {start,stop,status,configure}` (et non `everyapi start` / `everyapi configure`). Moteur + 6 détecteurs intégrés + regex personnalisées, intégré à `everyapi use`
- ✅ Onboarding OAuth vendeur pour les trois fournisseurs (codex device / claude paste / gemini loopback)
- ✅ Connexion par QR en chemin principal —— `auth login` utilise le device-code **+ QR en chemin principal**, avec `--no-qr` en repli
- ✅ Couches anti-hameçonnage —— phrase (`everyapi wallet topup`), contrôles stricts PKCE/state et épinglage de certificat, tous livrés. L'épinglage est **en mode rapport uniquement** (silencieux si ça correspond, avertissement sinon, ne refuse jamais la connexion), et la décision produit est « avertir, ne pas imposer »

## Signaler une vulnérabilité

Voir [`SECURITY.md`](../SECURITY.md).
