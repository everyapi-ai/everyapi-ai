> 🌐 [English](../README.md) · [简体中文](README.zh-CN.md) · [日本語](README.ja.md) · [한국어](README.ko.md) · [Español](README.es.md) · [Deutsch](README.de.md) · **Français**

# CLI `everyapi`

CLI de buyer onboarding pour la passerelle d'API IA [EveryAPI](https://everyapi.ai). Lancez Claude Code, Codex, Antigravity, Grok Build, Qwen Code ou Kimi Code **en moins d'une minute**.

État : **flux principaux livrés** —— le buyer onboarding, les commandes seller (plain-key + OAuth pour trois fournisseurs), le sanitizer proxy, le chemin principal QR sign-in et les couches anti-phishing sont tous en place. Les seuls éléments non implémentés sont le code signing au niveau du système et un backend keychain de plateforme (voir « Ce que ce binaire ne contient PAS encore » à la fin).

## Installation

```bash
brew tap everyapi-ai/tap && brew install everyapi
```

Pour les mises à jour ultérieures — d'abord `brew update` :

```bash
brew update && brew upgrade everyapi
```

Sans `brew update`, `brew upgrade everyapi` utilise le formula en cache et signale "already installed" même quand une nouvelle release existe.

## Commandes

| Commande | Rôle |
|---|---|
| `everyapi login` | Se connecter à EveryAPI depuis cet appareil |
| `everyapi logout` | Effacer les identifiants locaux |
| `everyapi status` | Voir solde, consommation, quota |
| `everyapi topup` | Ouvrir la page de rechargement (avec vérification de phrase anti-phishing) |
| `everyapi use <tool>` | Configurer env et exec dans un CLI tiers (pointé vers EveryAPI) |
| `everyapi seller <sub>` | Commandes côté vendeur du marketplace (list / withdraw / add-key / setup) |
| `everyapi edge <sub>` | Déploiement en une commande de l'agent supplier BYO-GPU (register / start / status / logs / models / stop / update / remove) |
| `everyapi mcp` | Exécuter en tant que serveur MCP (JSON-RPC sur stdin/stdout) |
| `everyapi update` | Vérifier les nouvelles versions et afficher la commande de mise à niveau pour votre méthode d'installation |
| `everyapi version` | Afficher la version de build |
| `everyapi help` | Aide |

### `everyapi use <tool>` — exec dans un CLI tiers (pointé vers la passerelle EveryAPI)

La raison principale d'installer ce CLI. Il configure et lance les clients de code pris en charge via EveryAPI ; l'entrée `gemini` lance le CLI Antigravity déjà authentifié.

```bash
everyapi use claude         # Claude Code → EveryAPI
everyapi use codex          # OpenAI Codex CLI → EveryAPI
everyapi use gemini         # Lancer Antigravity
everyapi use grok           # xAI Grok Build → EveryAPI
everyapi use qwen-code      # Alibaba Qwen Code → EveryAPI
everyapi use kimi-code      # Moonshot Kimi Code → EveryAPI
everyapi use                # sans argument → sélecteur interactif sur les outils installés
```

Chaque outil utilise des conventions d'env différentes ; le CLI les retient pour vous :

| Outil | Variables d'environnement configurées |
|---|---|
| claude | `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN` |
| codex | `OPENAI_BASE_URL`, `OPENAI_API_KEY` |
| gemini | lanceur Antigravity natif (`agy`) |
| grok | `XAI_API_KEY`, `GROK_MODELS_BASE_URL` |
| qwen-code | `OPENAI_API_KEY`, `OPENAI_BASE_URL`, `OPENAI_MODEL` ; `QWEN_HOME` isolé |
| kimi-code | `KIMI_MODEL_API_KEY`, `KIMI_MODEL_BASE_URL`, `KIMI_MODEL_NAME` ; `KIMI_CODE_HOME` isolé |

Plus besoin de chercher quelle variable chaque outil lit, s'il faut ajouter le suffixe `/v1`, ou quel style de header auth s'applique.

> ⚠️ **Note de sécurité sur l'env de sous-processus** : les variables d'environnement ci-dessus contiennent votre relay API key. Les modes debug / verbose des CLI tiers peuvent logguer l'env — avant d'exécuter `everyapi use`, assurez-vous que le flag debug que vous activez ne fuit pas `*_TOKEN` / `*_API_KEY`. Avant de partager des logs de debug, exécutez `sed -i 's/sk-everyapi-[A-Za-z0-9]*/REDACTED/g'`.

### `everyapi login` — Device Authorization Grant + QR sign-in

Utilise Device Authorization Grant (style RFC 8628) + docs §7-5 Layer 1 « QR sign-in device-to-device » :

1. Le CLI crée une session, **affiche un QR dans le terminal + imprime un code court + URL**
2. Scannez le QR avec votre téléphone (ou ouvrez l'URL dans un navigateur déjà connecté à EveryAPI) —— l'URL dans le QR porte déjà `?code=USR-789`, le dashboard remplit le code automatiquement, l'utilisateur n'a qu'à cliquer sur Approve
3. Le CLI reçoit l'access token et le stocke dans `~/.config/everyapi/credentials.json` (mode 0600)

```bash
everyapi login                                    # production ; affiche QR + ouvre le navigateur par défaut
everyapi login --api-base http://localhost:8787   # dev local / self-hosted
everyapi login --no-browser                       # ne pas ouvrir le navigateur automatiquement (scanner le QR)
everyapi login --no-qr                            # ne pas afficher le QR (terminaux non UTF-8 / piping)
```

Exemple de rendu QR dans le terminal (caractères Unicode de demi-bloc ; environ 18-20 lignes de haut) :

```
█▀▀▀▀▀█  ▀▀ ▄  █▀▀▀▀▀█
█ ███ █  ▀▄█▀  █ ███ █
█ ▀▀▀ █ ▄ ▀ █▀ █ ▀▀▀ █
▀▀▀▀▀▀▀ █▄█▄█▄ ▀▀▀▀▀▀▀
... (le vrai QR encode verification_uri?code=USR-789)
```

Pourquoi c'est un chemin anti-phishing plus fort :

- L'utilisateur **ne saisit pas de mot de passe sur le nouvel appareil** → aucune opportunité pour un site de phishing de capturer les credentials
- L'utilisateur **n'est pas redirigé vers une page de navigateur inconnue** → la surface de phishing par redirection web disparaît
- Même si le CLI est un fork malveillant produisant un faux QR, la page de confirmation après scan est le vrai dashboard everyapi.ai (déclenché depuis un appareil sur lequel l'utilisateur est déjà connecté), et un code inconnu n'est pas quelque chose qu'un utilisateur Approve

Les autres layers de docs §7-5 (cert pinning / phrase string / PKCE OAuth) ont été déployés dans des PR indépendantes (cert pinning est report-only ; enforce a été une décision produit de ne pas livrer).

### `everyapi seller <sub>` — sous-commandes côté vendeur du marketplace

Apporte au terminal les flux de mount de channels et de retrait du dashboard pour le scripted onboarding. Avant de monter un channel, `seller setup` vérifie l'éligibilité (compte actif / email vérifié / ancienneté de compte / historique de dépenses / plafond de channels), et les gates qui échouent sont listés **avant que l'utilisateur ne tape une key**, pour éviter de l'apprendre via un 422 après soumission.

```bash
everyapi seller list                          # lister les channels montés
everyapi seller withdraw                      # déplacer tous les gains seller pending vers le solde principal
everyapi seller withdraw --quota 1000         # transfert partiel (unités DB)
everyapi seller add-key   --type claude --name 'my-pro' \
                        --key 'sk-ant-...' --models 'claude-3-opus,claude-3-sonnet'
everyapi seller add-oauth codex --name 'my-chatgpt' --models 'gpt-4'
                                            # OAuth en un clic : le CLI démarre un device flow, l'utilisateur entre
                                            # le user_code dans le navigateur, le token atterrit automatiquement sur le channel
everyapi seller add-oauth claude --name 'my-claude' --models 'claude-3-opus,claude-3-sonnet'
                                            # paste flow : le CLI ouvre la page d'autorisation Anthropic ; l'utilisateur
                                            # colle le code#state affiché par le callback dans le terminal
everyapi seller add-oauth gemini --name 'my-gemini' --models 'gemini-1.5-pro'
                                            # vrai loopback en un clic : le CLI démarre un listener sur port aléatoire,
                                            # Google envoie le code directement au CLI —— pas de collage
everyapi seller setup                         # wizard interactif : vérifie d'abord l'éligibilité, puis guide vers add-key
```

#### `add-key` — pool de keys de secours

`--key` peut être répété pour monter N credentials équivalents sur le même channel comme pool de secours (B2, PRODUCT §4.5) ; quand la key primaire renvoie 401/403, le backend bascule automatiquement sur la suivante. `--key-remark` peut aussi être répété, aligné positionnellement avec `--key` (le i-ème `--key-remark` est le label du i-ème `--key`, pour identification ultérieure dans le dashboard). Les blobs OAuth ne peuvent pas aller dans le pool de secours —— ils restent des channels à key unique.

```
everyapi seller add-key   --type claude --name 'claude-pool' \
                        --models 'claude-3-opus' \
                        --key 'sk-ant-primary' --key-remark 'primary' \
                        --key 'sk-ant-backup'  --key-remark 'team backup'
```

`--type` de `add-key` accepte des aliases (`openai` / `claude` / `gemini` / `codex` / `vertex` / `aws` / `xai` / `deepseek`) ou un id numérique. Le montage est soumis à l'éligibilité marketplace (compte actif, email vérifié, historique de dépenses, plafond de channels), et le CLI liste la checklist d'échec aux trois points d'entrée (`add-key` / `add-oauth` / `setup`) avant toute autre action.

#### `add-oauth codex` — OAuth en un clic (device flow)

`everyapi seller add-oauth codex --name 'my-chatgpt' --models 'gpt-4'` exécute le device authorization flow style RFC 8628 de Codex / ChatGPT —— le vendeur **ne touche jamais la chaîne du token** :

1. Le CLI appelle `/api/seller/codex/device/start` et reçoit un `user_code` court et une `verification_uri`
2. Le CLI ouvre le navigateur par défaut sur `https://auth.openai.com/codex/device` (passer avec `--no-browser`) ; l'utilisateur entre le `user_code` dans le navigateur pour compléter l'autorisation
3. Le CLI poll `/api/seller/codex/device/poll` ; une fois autorisé, le backend crée le channel et écrit le token OAuth dans le champ `key` du channel
4. Sortie : id de channel + l'email ChatGPT lié

Les cookies d'autorisation sont gérés par un `http.CookieJar` en-process (non persisté) —— le state du device flow est éphémère et lié au process, conformément au modèle de menace.

#### `add-oauth claude` — OAuth paste-and-submit

`everyapi seller add-oauth claude --name … --models …`. Le provider OAuth d'Anthropic hardcode `redirect_uri` à `https://console.anthropic.com/oauth/code/callback` de leur côté, donc le CLI ne peut pas utiliser un listener localhost pour recevoir le callback. Flow :

1. Le CLI appelle `/api/seller/claude/oauth/start` ; le backend crée la paire PKCE + state et renvoie l'URL authorize d'Anthropic
2. Le CLI ouvre le navigateur par défaut (passer avec `--no-browser`) ; l'utilisateur se connecte à Anthropic et approuve
3. Anthropic redirige vers sa page de callback affichant une chaîne `<code>#<state>`
4. **L'utilisateur copie cette chaîne dans le CLI**
5. Le CLI appelle `/api/seller/claude/oauth/complete` ; le backend échange code+verifier contre le token et mint le channel

Une étape de collage supplémentaire vs le device flow, mais toujours beaucoup plus simple que de chercher manuellement `~/.claude/auth.json`. Le cookie de session est émis par le backend au start ; complete doit toucher la même session —— le `http.CookieJar` du CLI est en-process et isolé par invocation.

#### `add-oauth gemini` — vrai OAuth loopback en un clic

`everyapi seller add-oauth gemini --name … --models … [--no-browser] [--timeout 5m]`. Le client OAuth installed-app de gemini-cli de Google accepte `http://127.0.0.1:<port>/callback` comme `redirect_uri`, donc **le CLI exécute son propre listener pour le callback** —— l'utilisateur se connecte via le navigateur et n'a rien à coller. Flow :

1. Le CLI démarre un listener HTTP one-shot sur un port éphémère aléatoire (`127.0.0.1:0`), chemin fixe `/callback`
2. Le CLI appelle `/api/seller/gemini/oauth/start` avec `redirect_uri = http://127.0.0.1:<port>/callback` ; le backend valide strictement le redirect : loopback / port ≥ 1024 / scheme=http / path=/callback / pas de query/fragment/userinfo (prévient SSRF + redirect hijacking)
3. Le CLI ouvre le navigateur par défaut ; l'utilisateur se connecte à Google et consent
4. Google redirige avec `?code=…&state=…` vers le listener du CLI
5. Le CLI vérifie que le state correspond (prévient flux stale / forgery) et appelle `/api/seller/gemini/oauth/complete`
6. Le backend échange code + même redirect_uri contre le token et mint le channel

Comparaison avec les deux autres providers :

| Provider | UX | Raison |
|---|---|---|
| `codex` | L'utilisateur tape un user_code à 6 chiffres dans le navigateur ; le CLI auto-poll | Device flow OpenAI, pas de redirect_uri |
| `claude` | L'utilisateur se connecte via navigateur, copie `code#state` dans le CLI | Anthropic hardcode redirect_uri sur sa propre URL de callback |
| `gemini` | L'utilisateur se connecte via navigateur, ferme l'onglet, terminé | Google accepte les loopback redirects |

`--timeout` borne l'attente (5 minutes par défaut). Au timeout, le CLI sort et ferme proprement le listener.

### `everyapi edge <sub>` — déploiement en une commande de l'agent supplier BYO-GPU

Connecter des GPU inactifs à EveryAPI pour vendre du compute. Le CLI condense le déploiement en 8 sous-commandes, épargnant aux fournisseurs la copie manuelle de docker-compose, le remplissage du `.env` ou le déplacement du registration token :

```bash
everyapi login                              # réutilise la connexion existante
everyapi edge register --name "rtx-4090"    # appelle /api/seller/edge/nodes pour node_id + token, écrit dans ~/.local/share/everyapi/edge/<id>/
everyapi edge start                         # auto-détecte NVIDIA / ROCm / Apple Silicon / CPU, docker compose up -d
everyapi edge models pull llama3.1:8b       # docker compose exec ollama ollama pull ...
everyapi edge status                        # docker compose ps local + dashboard online/offline
everyapi edge logs -f                       # suivre les logs
everyapi edge update                        # docker compose pull && up -d
everyapi edge remove                        # down -v + supprimer le dir local + DELETE backend
```

`start` rend `docker-compose.yml` à l'exécution via `text/template` (**pas depuis un YAML statique embarqué**) —— cela permet aux noms de containers d'être namespacés par node_id pour que plusieurs nodes sur un même hôte ne se chevauchent pas, et le GPU passthrough est rendu conditionnellement par mode (NVIDIA = `deploy.resources.devices` + driver nvidia ; ROCm = `/dev/kfd` + `/dev/dri` + `group_add: video` ; macOS = pas de container ollama, l'agent se connecte au ollama natif de l'hôte via `host.docker.internal`).

Flux des credentials : le cli utilise un Bearer `sk-everyapi-` existant pour appeler `POST /api/seller/edge/nodes` → le backend renvoie le `registration_token` une seule fois (ensuite le backend ne stocke que le sha256, ne l'affiche plus jamais) → le cli l'écrit 0600 dans `~/.local/share/everyapi/edge/<id>/node.json` → le rend dans l'env `EVERYAPI_REGISTRATION_TOKEN` du compose. **Le registration token n'est jamais écrit dans aucun fichier .env** (pour que les fournisseurs ne le committent pas par accident).

Prérequis : `docker` + `docker compose v2` (v1 est EOL et non supporté). Sur macOS, `brew install ollama && brew services start ollama` (l'accélération Metal ne fonctionne pas dans un container docker).

### `everyapi topup` — redirect de rechargement avec phrase anti-phishing

`everyapi topup` ouvre la page de rechargement du dashboard. Avant la redirection, il passe par une vérification docs §7-5 Layer 3 :

1. Le CLI appelle le backend `POST /api/cli/jump-session` et reçoit un session id + une chaîne phrase à 4 emojis (par ex. `🌊 🦊 🍕 🚀`)
2. Le CLI imprime à la fois l'URL et la phrase dans le terminal, en disant à l'utilisateur « la même phrase devrait apparaître en haut de la page dans un instant »
3. L'utilisateur appuie sur Entrée ; le CLI ouvre l'URL via le navigateur système (avec `?jump_session=<id>`)
4. Au chargement, le dashboard appelle le backend `GET /api/cli/jump-session/:id/phrase`, reçoit la même phrase et **l'affiche de manière proéminente dans le header de la page**
5. L'utilisateur compare visuellement : phrase identique → vrai EveryAPI ; mismatch ou non affichée → fermer l'onglet, phishing possible

Pourquoi cela bloque le phishing : la phrase vit dans la mémoire du backend, clé sur un session id aléatoire de 32 hex ; un site de phishing n'a pas de chemin auth pour la récupérer, et un `wallet/topup?jump_session=<id>` forgé par un attaquant ne peut pas non plus lire la phrase. Un TTL court (10 min) + single-use (la session est supprimée après que le dashboard l'a récupérée une fois) limitent davantage le risque de réutilisation.

```bash
everyapi topup                    # ouvre le navigateur par défaut
everyapi topup --no-browser       # affiche seulement l'URL, copier manuellement
```

### `everyapi status` — solde / consommation / quota actuels

```
$ everyapi status

  alice (alice@example.com)
  quota:     $12.34 remaining   $5.67 used
  requests:  1,234
  topup:     https://app.everyapi.ai/wallet
```

### `everyapi update` —— lance automatiquement la commande de mise à jour brew

Vérifie la dernière release sur le mirror GitHub, la compare avec la version actuelle, et **lance automatiquement `brew update && brew upgrade everyapi`** —— une seule commande, pas de copier-coller.

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

Pourquoi ne pas remplacer le binaire directement ? La chaîne de vérification propre à Homebrew (SHA / bottle signing) est plus solide que tout ce que nous reconstruirions à l'intérieur du CLI, et auto-remplacer un executable en cours d'exécution est un champ de mines sur la plateforme Windows.

Flags :
- `--check` —— comparaison silencieuse ; exit 0 si à jour, exit 1 si obsolète. Pour CI / cron :
  ```bash
  everyapi update --check || echo "needs upgrade"
  ```
- `--dry-run` —— affiche la commande qui serait exécutée mais ne l'exécute pas (pour inspection)

### `everyapi settings` — préférences CLI (langue, etc.)

Le CLI est livré avec i18n en 7 langues : anglais, chinois simplifié, japonais, coréen, espagnol, allemand, français — les chaînes du CLI s'affichent dans la langue choisie. Les erreurs de l'API backend sont auto-négociées via le header `Accept-Language` et couvrent 8 langues — les 7 précédentes plus chinois traditionnel.

```bash
$ everyapi settings                          # picker interactif : choisir une langue
$ everyapi settings list                     # afficher les réglages actuels
$ everyapi settings set language zh          # définir directement
$ everyapi settings set language fr          # français identique
$ everyapi settings reset                    # réinitialiser au défaut (en + auto-détection LANG)
```

**Auto-détection** : si vous n'avez rien défini explicitement, le CLI lit les env vars dans l'ordre `EVERYAPI_LANG > LC_ALL > LC_MESSAGES > LANG` au démarrage. Un locale système `zh_CN.UTF-8` / `ja_JP.UTF-8` / `fr_FR.UTF-8` etc. prend effet immédiatement —— zéro configuration.

**Override ponctuel** :

```bash
EVERYAPI_LANG=zh everyapi status             # cette invocation s'affiche en chinois ; non persistée
```

**Exemple de traduction** (erreur not-logged-in, 7 langues × même ligne) :

```
en : Error: not logged in — run 'everyapi login' first
zh : 错误: 未登录 — 先运行 'everyapi login'
ja : エラー: ログインしていません — まず 'everyapi login' を実行してください
ko : 오류: 로그인되어 있지 않습니다 — 먼저 'everyapi login' 을 실행하세요
es : Error: no has iniciado sesión — ejecuta primero 'everyapi login'
de : Fehler: nicht angemeldet — führe zuerst 'everyapi login' aus
fr : Erreur: non connecté — exécutez d'abord 'everyapi login'
```

Les réglages vivent dans `~/.config/everyapi/settings.json` (même répertoire que `credentials.json`, mais mode `0644` —— pas de secrets).

**Pour améliorer les traductions ou ajouter une langue** : voir [`internal/i18n/locales/README.md`](internal/i18n/locales/README.md).

## Fichiers de configuration

Les credentials vivent dans `~/.config/everyapi/credentials.json` (ou `$XDG_CONFIG_HOME/everyapi/` si `$XDG_CONFIG_HOME` est défini), mode de fichier `0600`. Écrits par `everyapi login`, lus par toute autre commande.

> ⚠️ **Les tokens sont stockés en clair**. Mode de fichier `0600` + chemin privé `$HOME` correspond à la convention des CLI industriels comme `gh auth` / `aws configure`, mais **pour les modèles de menace de vol de machine personnelle / malware**, tout process pouvant lire ce fichier peut appeler l'API EveryAPI en tant que vous (y compris les tools MCP —— voir §money-path friction step ci-dessous). Recommandé :
> - Ne pas faire `everyapi login` sur des machines partagées / publiques
> - Utilisateurs macOS : envisager `everyapi logout` avant d'activer FileVault
> - Utilisateurs Linux : activer le chiffrement du home-dir (`ecryptfs` / LUKS)
> - Si vous suspectez une fuite → `everyapi logout` efface immédiatement les credentials locaux, et faites tourner l'API key depuis le dashboard EveryAPI
>
> Un backend keychain de plateforme (macOS Keychain / Windows DPAPI / Linux Secret Service) est prévu mais non livré.

Champs :

- `api_base` —— l'URL de la passerelle EveryAPI. Par défaut `https://api.everyapi.ai`. Les utilisateurs self-hosted / dev local peuvent surcharger avec `--api-base` au `login`.
- `access_token` —— bearer utilisé par chaque appel API authentifié.
- `relay_key` —— relay API key (`sk-everyapi-…`) utilisée pour l'env du sous-process de `everyapi use`. Récupérée depuis `/api/token/*` et cachée ici.
- `user_id` / `username` —— cachés pour que `status` puisse rendre la ligne d'identité avant son premier round-trip API.

## Développement

Dans le répertoire source du CLI (celui contenant ce README, `go.mod` et `Makefile`) :

```bash
go test ./...
go run . status            # contre la production
go run . login --api-base http://localhost:8787   # contre le backend local
```

Compilation croisée locale pour toutes les plateformes (même recette que CI) :

```bash
make cli-release           # artefacts dans dist/ (5 plateformes × 1 binaire = 5 fichiers)
```

## Serveur MCP (sous-commande `everyapi mcp`)

Le binaire `everyapi` **comprend un** serveur [Model Context Protocol](https://modelcontextprotocol.io) intégré —— exposé en sous-commande (`everyapi mcp` lit stdin et écrit stdout). Les agents IA (Claude Code / Cursor / Codex CLI / n'importe quel client MCP) peuvent l'invoquer directement, **sans que l'utilisateur ouvre un terminal**.

> ⚠️ **Modèle d'auth et surface d'exposition du serveur MCP**
>
> - **Pas de ports ouverts** : `everyapi mcp` est du JSON-RPC stdio pur, forké par le host CLI. **N'écoute sur aucun socket / port TCP** —— pas de surface réseau.
> - **Lit `~/.config/everyapi/credentials.json` directement** : le serveur MCP n'a pas de propre flux d'auth ; capacité à lire le fichier de credentials = capacité à appeler chaque tool exposé en tant que vous. Tout host MCP capable d'exécuter un process en tant que votre user a accès complet.
> - **Le chemin de l'argent `everyapi_seller_withdraw` a une étape de friction** : les appelants doivent passer `confirm: "yes"`, garantissant que l'agent IA fait remonter l'action de transfert dans l'UI à un humain et évite un silent drain. Les autres tools en lecture seule (status / topup / seller_list) n'ont pas cette exigence.
>
> N'installez pas de hosts MCP en lesquels vous n'avez pas confiance.

### Installation

Même binaire que le CLI —— installer le CLI vous donne le serveur MCP :

```bash
make cli                                              # build local, produit ./bin/everyapi
# ou via go install :
go install github.com/everyapi-ai/everyapi-ai@latest
```

### Branchement dans Claude Code

Ajouter à `~/.claude/settings.json` :

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

Le branchement dans Cursor, Codex CLI ou d'autres clients MCP est similaire —— pointez `command` vers le binaire `everyapi` avec `args: ["mcp"]`.

### Prérequis d'auth

Vous devez avoir exécuté `everyapi login` dans un terminal au moins une fois —— le serveur MCP est un process en arrière-plan sans capacité d'interaction terminal, il ne peut donc pas exécuter le flux device-code lui-même. Il lit `~/.config/everyapi/credentials.json` directement ; s'il est manquant, chaque tool renvoie un message `isError: true` « not logged in » guidant l'utilisateur vers la connexion.

### Tools exposés en v1 (8 au total)

| Tool | Entrée | Rôle |
|---|---|---|
| `everyapi_status` | aucune | Solde actuel / utilisé / nombre de requêtes |
| `everyapi_topup` | aucune | Renvoie l'URL web de rechargement |
| `everyapi_seller_list` | aucune | Liste les seller channels du marketplace |
| `everyapi_seller_withdraw` | `{confirm: "yes", quota?: int}` | Transfère seller_quota vers le solde principal ; **`confirm: "yes"` requis** (friction du chemin de l'argent) |
| `everyapi_seller_add_oauth_codex_start` | `{name, models}` | Démarre le flux d'autorisation de device Codex / ChatGPT ; renvoie `user_code` + `verification_uri` + `flow_id` |
| `everyapi_seller_add_oauth_codex_poll` | `{flow_id}` | Vérifie l'état d'autorisation Codex. `pending`/`slow_down` continuer le polling ; `authorized` renvoie l'id de channel ; `expired`/`denied` terminent |
| `everyapi_seller_add_oauth_claude_start` | `{name, models}` | Démarre le flux OAuth Anthropic ; renvoie `authorize_url`. Après que l'utilisateur s'est connecté via navigateur, il reçoit une chaîne `<code>#<state>` |
| `everyapi_seller_add_oauth_claude_complete` | `{input}` | Soumet la chaîne `<code>#<state>` que l'utilisateur a collée à l'étape précédente ; mint le channel |

**Modèle d'usage des tools OAuth** (comment un agent IA traverse cela dans une conversation) :

```
User: Ajoute-moi un seller channel ChatGPT Plus, appelle-le my-chatgpt, models gpt-4
AI    → everyapi_seller_add_oauth_codex_start({name: "my-chatgpt", models: "gpt-4"})
       ← « Va sur chatgpt.com/codex, entre USR-789, puis dis-moi quand c'est fait »
User: Fait dans le navigateur
AI    → everyapi_seller_add_oauth_codex_poll({flow_id: "..."})
       ← « status=pending, attends quelques secondes de plus »
[continuer le polling jusqu'à authorized]
       ← « status=authorized — channel #314 mounted »

User: Ajoute aussi le Claude Pro, my-claude / claude-3-opus
AI    → everyapi_seller_add_oauth_claude_start({...})
       ← « Va sur [URL] pour compléter l'autorisation, puis donne-moi la chaîne code#state »
User: code-abc123#state-xyz
AI    → everyapi_seller_add_oauth_claude_complete({input: "code-abc123#state-xyz"})
       ← « Channel #315 mounted »
```

Gemini OAuth (flux loopback) **n'est pas exposé via MCP** —— le cycle de vie du listener loopback ne correspond pas au cycle de vie cross-tool-call. Gemini passe toujours par le CLI `everyapi seller add-oauth gemini`.

### Smoke test manuel

```bash
make cli
./bin/everyapi mcp <<'EOF'
{"jsonrpc":"2.0","id":1,"method":"initialize"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"everyapi_status","arguments":{}}}
EOF
```

Vous devriez voir trois lignes de réponse JSON : résultat initialize, liste de 4 tools, texte de status (ou un isError not-logged-in).

## Ce que ce binaire ne contient PAS encore

Toujours **non implémentés** (triés par importance ; les releases suivantes les ajouteront de manière incrémentale sans casser la surface v1) :

- ⚠️ Code signing au niveau du système (notarisation macOS / Authenticode Windows) —— pour l'instant nous nous appuyons sur la vérification double couche sigstore cosign keyless + SHA256SUMS ; les deux sont joints à chaque GitHub Release et Homebrew les vérifie automatiquement à l'installation
- ❌ Backend keychain de plateforme —— les tokens restent stockés en clair sur disque (mode 0600)

Précédemment listés ici mais **maintenant livrés** (ne pas traiter comme non implémentés) :

- ✅ Sanitizer proxy local —— la commande est `everyapi proxy {start,stop,status,configure}` (pas `everyapi start`/`everyapi configure`) ; moteur + 6 détecteurs intégrés + regex personnalisés + intégré dans `everyapi use`
- ✅ Seller OAuth onboarding pour les trois providers (codex device / claude paste / gemini loopback)
- ✅ Chemin principal QR sign-in —— `login` utilise device-code **+ QR comme chemin principal**, avec `--no-qr` en fallback
- ✅ Couches anti-phishing —— phrase string (`everyapi topup`), strict-check PKCE/state et cert pinning sont toutes en place ; cert pinning est **report-only** (silencieux en cas de match / alerte en cas de mismatch / ne refuse jamais la connexion), avec la décision produit de « alerte seulement, ne pas enforce »

## Signaler des vulnérabilités

Voir [`SECURITY.md`](../SECURITY.md).
