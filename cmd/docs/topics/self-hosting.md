# Running your own deployment

EveryAPI is a Go backend you can run yourself. The default image is the API gateway only; an optional image embeds the dashboard for a single-container deployment.

## Requirements

```
CPU       2 cores or more
Memory    2 GB or more
Storage   10 GB or more
Database  SQLite, MySQL >= 8.0, or PostgreSQL >= 9.6
Cache     Redis, optional — there is an in-process fallback
```

All database code supports the three engines simultaneously, so the choice is yours and reversible in the sense that nothing in the schema is engine-specific.

## Production, Docker Compose

```
make env     Generate .env from the example, with random secrets; idempotent
make up      Bring up the production stack on :8787
make logs
make ps
make down
```

The default stack runs the API-only image: `:8787` has no UI and `/` returns a status page. For one container that also serves the dashboard, build `deploy/Dockerfile.embed`.

## From source

```
go build -o everyapi ./backend/cmd/everyapi
./everyapi
```

With no `SQL_DSN` it uses SQLite at `everyapi.db`; override the path with `SQLITE_PATH`.

## Environment variables

The full list is in `deploy/.env.example` and the comments in `deploy/docker-compose*.yml`. The ones that matter on day one:

```
SQL_DSN         Database DSN. MySQL: user:pass@tcp(host:port)/db
                PostgreSQL: postgresql://user:pass@host:port/db
                Empty falls back to SQLite
SQLITE_PATH     SQLite file path; default everyapi.db
LOG_SQL_DSN     Separate log database; empty means the same one
REDIS_CONN_STRING   Redis DSN; optional
SESSION_SECRET  Required for multi-node deployments
CRYPTO_SECRET   Encryption key
NODE_NAME       Node name, recorded in multi-node audit logs
TZ              Time zone
ENABLE_SWAGGER  Serve the OpenAPI UI. Public deployments must also
                set SWAGGER_USER and SWAGGER_PASSWORD
```

Optional subsystems stay off until you give them an endpoint. Each has a shared per-UTC-day call cap across replicas so an integration cannot run away with your budget:

```
SEMANTIC_CACHE_EMBEDDING_BASE_URL / _API_KEY
    Semantic response matching
SEMANTIC_CACHE_EMBEDDING_DAILY_CALL_LIMIT     default 5000
SEMANTIC_CACHE_VECTOR_MAX_ENTRIES             default 50000
AGENT_SESSION_HMAC_SECRET
    Stable, non-rotating; enables durable session correlation
QUALITY_JUDGE_BASE_URL                        Consent-gated Judge jobs
QUALITY_JUDGE_DAILY_CALL_LIMIT                default 100
OPS_COPILOT_BASE_URL / _MODEL / _API_KEY      Ops explanations
OPS_COPILOT_DAILY_CALL_LIMIT                  default 50
```

## Health endpoints

```
GET /health      Liveness: the process is up. No auth
GET /api/health  Readiness: dependencies are reachable
```

`/health` returning 200 while `/api/health` fails means the app is up and something it depends on is not — database, cache, or object storage.

## Local development

```
make setup          Initialise the docs submodule; once after cloning
make dev            Postgres and Redis in Docker, API on :8787
make dev-deps       Data dependencies only
make dev-api-watch  API with hot reload
make dev-down       Stop the data dependencies
```

The development DSN is `postgresql://root:123456@localhost:5432/everyapi`.

Pointing the CLI at it:

```
everyapi auth login --api-base http://localhost:8787
```

A custom `--api-base` wins over the `gateway_region` setting, so a self-hosted login is unaffected by the global/CN switch.

## Marketplace and supply

Both are off until an operator opens them:

```
everyapi admin marketplace status
everyapi admin marketplace on
everyapi admin marketplace off
```

Closing the marketplace stops new mounts. Nodes and channels that already exist keep serving.

## Operator commands

`everyapi admin` reuses your normal login; the backend enforces the role, so a non-admin gets a 403 rather than a hidden menu being the only guard. Every action lands in the same audit log the dashboard writes to.

```
everyapi admin marketplace status | on | off
everyapi admin user search <keyword> | show <id> | list [--page P]
everyapi admin user manage <id> --action enable|disable|delete
                                |promote_admin|demote_admin
everyapi admin channel test <id>
everyapi admin channel tag <name> --enable|--disable
everyapi admin log tail [--user U] [--model M] [--channel C] [--since W]
everyapi admin abuse list [--status] | show <id> | update <id> --status X
everyapi admin audit [--page P]
everyapi admin redemption list | search <kw> | show <id>
                      | create --name N --quota Q [--count C] [--expires E]
                      | update <id> | status <id> enable|disable
                      | delete <id> | clear-invalid
```

`redemption create` mints vouchers and prints their keys once.

## Operations

Deployment assets are under `deploy/`; operational procedures — incident triage, dependency checks, upstream drains, rollout history — live in `deploy/RUNBOOK.md`.
