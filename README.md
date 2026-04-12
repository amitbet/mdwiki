# mdwiki

Git-backed wiki: **GFM** pages in git, **Yjs** realtime editing over **Go WebSockets**, optional **Redis** pub/sub for multi-instance, **SQLite FTS5** search, **GitHub OAuth**, per-thread comment JSON under `.mdwiki/comments/`.

## Wiki In Action

Collaborative editing, comments, diagrams, and media in a git-backed wiki UI:

![mdwiki in action](docs/images/wiki-in-action.png)

## Core Guidelines (Project Manifest)
* Text should be captured in Markdown, with support for additional markup formats for all kind of expressions (image, diagrams, charts, etc.)
* The server should save everything to git
* Git files shoud be readable and navigable (md filenames, git directories, makedown text, git history for md files - should look clean and human readable)
* UI should be feature rich and feel like a full wiki software environment.
* Project should be enterprise ready (auth, permissions, backend scalability)
* Should be based on the git-service for users and permissions and anything else (github at first but we can add gitlab or others next)

## Quick start

### API + UI together (recommended)

```bash
make install   # first time: npm + go deps
make dev       # Go server on :8080 + Vite on :5173 (parallel)
```

Open **http://localhost:5173** — the Vite dev server proxies **`/api`**, **`/auth`**, **`/health`**, and **`/ws`** to the backend (`MDWIKI_BACKEND`, default `http://127.0.0.1:8080`).

Overrides:

```bash
make dev SERVER_PORT=9090 UI_PORT=3000
# sets MDWIKI_LISTEN=:9090, BACKEND_URL=http://127.0.0.1:9090, FRONTEND_ORIGIN=http://localhost:3000
```

### Backend only

```bash
export MDWIKI_DATA=./data
export MDWIKI_REGISTRY=./spaces-registry.yaml
# Optional: GitHub OAuth
export MDWIKI_GITHUB_CLIENT_ID=...
export MDWIKI_GITHUB_CLIENT_SECRET=...
export MDWIKI_GITHUB_CALLBACK=http://localhost:8080/auth/github/callback
# Same app supports the OAuth 2.0 device flow (CLI / “Sign in with device code” in the UI).
# In GitHub → Settings → Developer settings → OAuth Apps → your app, enable **Device authorization** if GitHub shows that option.
# Git clone/push (user token after login, or server token)
export MDWIKI_SERVER_GIT_TOKEN=...

# Optional distributed mode for multiple mdwiki servers.
# Disabled by default; leave MDWIKI_REDIS_ENABLED unset for single-server mode.
export MDWIKI_REDIS_ENABLED=1

# Single Redis:
export MDWIKI_REDIS_URL=redis://127.0.0.1:6379/0

# Or Redis Cluster:
# export MDWIKI_REDIS_ADDRS=127.0.0.1:7000,127.0.0.1:7001,127.0.0.1:7002
# export MDWIKI_REDIS_CLUSTER_MODE=1
# Optional auth for either mode:
# export MDWIKI_REDIS_USERNAME=...
# export MDWIKI_REDIS_PASSWORD=...

go run ./cmd/wiki
```

Health: `GET http://localhost:8080/health`

### GitHub OAuth setup

If you want GitHub sign-in, create a GitHub OAuth App and copy its values into mdwiki.

1. In GitHub, go to **Settings** -> **Developer settings** -> **OAuth Apps** -> **New OAuth App**.
2. Fill in the app form:
   - **Application name**: anything user-facing, for example `mdwiki local`
   - **Homepage URL**: `<frontend-origin>`
   - **Authorization callback URL**: `<backend-origin>/auth/github/callback`
3. Click **Register application**.
4. On the OAuth app page:
   - copy **Client ID** into `MDWIKI_GITHUB_CLIENT_ID`
   - click **Generate a new client secret**
   - copy that secret into `MDWIKI_GITHUB_CLIENT_SECRET`
5. Set `MDWIKI_GITHUB_CALLBACK` to the same callback URL you entered in GitHub: `<backend-origin>/auth/github/callback`

Template values:

- `<frontend-origin>`: where the UI is served, for example `http://localhost:3000`
- `<backend-origin>`: where the Go server is served, for example `http://localhost:8080`
- OAuth callback/whitelist URL: `<backend-origin>/auth/github/callback`

Example:

```bash
export MDWIKI_GITHUB_CLIENT_ID=...
export MDWIKI_GITHUB_CLIENT_SECRET=...
export MDWIKI_GITHUB_CALLBACK=http://localhost:8080/auth/github/callback
```

If mdwiki also needs to clone or push without a signed-in user, set `MDWIKI_SERVER_GIT_TOKEN` to a GitHub token with repo access.

### Frontend only

```bash
cd web && npm install && npm run dev
```

With `make dev`, Vite receives `MDWIKI_BACKEND` so the proxy target matches `SERVER_PORT`.

Set `VITE_SPACE_KEY=demo` in `web/.env` to match a space key in `spaces-registry.yaml`.

### Development auth (no GitHub)

If `MDWIKI_DEV=1`, open `GET http://localhost:8080/api/dev/login` once to obtain a session cookie (insecure, local only).

## Features (plan coverage)

| Area | Implementation |
|------|----------------|
| Schemas | [schemas/](schemas/) JSON Schema for space, page meta, thread, index |
| Anchors | `internal/anchor` — `<!-- wiki:anchor:id -->` extraction |
| GFM | `web/src/md/render.ts` — remark + remark-gfm + rehype-sanitize (pinned in package-lock) |
| Yjs + Go WS | `internal/ws` hub, `web/src/WikiEditor.tsx` |
| Redis | `internal/redisx` + `MDWIKI_REDIS_ENABLED`; single-node via `MDWIKI_REDIS_URL`, cluster via `MDWIKI_REDIS_ADDRS` |
| Search | `internal/search` SQLite FTS5 |
| Git | `internal/gitops` clone / pull / commit / push |
| OAuth | `internal/oauth`, `internal/api` `/auth/github`, `/auth/github/device/*` |
| Comments | `internal/comments` thread JSON helpers |
| Layout | [docs/GIT_LAYOUT.md](docs/GIT_LAYOUT.md) |

## Production

Build the UI: `cd web && npm run build`, then serve `web/dist` with a reverse proxy or add a static file route to the Go server.

## Redis-backed multi-server mode

Single-server mode remains the default. Redis mode is enabled only when `MDWIKI_REDIS_ENABLED=1`.

When Redis mode is on, mdwiki uses Redis for two cross-server features:

1. Yjs websocket fan-out so clients connected to different mdwiki servers but in the same room stay in sync.
2. Distributed git write coordination via a Redis Streams worker queue plus Redis locks, with mdwiki servers acting as queue workers.

In Redis mode, page saves are asynchronous: `POST /api/spaces/{space}/page` returns `202 Accepted` with a `job_id` once the save is queued, and the UI tracks completion via WebSocket job updates with HTTP status fallback. Single-server mode keeps the existing synchronous save UX.

The git worker path includes:

- Redis Streams consumer groups for durable claim/ack and abandoned-job recovery
- Lock renewal for long-running git operations
- Per-job idempotency state so duplicate submissions with the same job id do not execute twice

To smoke-test the Redis integration with Docker:

```bash
# Single Redis
docker run --rm -d --name mdwiki-redis-single -p 6379:6379 redis:7-alpine
MDWIKI_REDIS_INTEGRATION=1 \
MDWIKI_REDIS_ENABLED=1 \
MDWIKI_REDIS_URL=redis://127.0.0.1:6379/0 \
go test ./internal/redisx -run TestRedisIntegration -count=1
docker stop mdwiki-redis-single

# Redis Cluster (example using a local 6-node cluster image)
docker run --rm -d --name mdwiki-redis-cluster -p 7000-7005:7000-7005 grokzen/redis-cluster:7.0.10
# This image advertises internal node IPs, so run the test from a container in the same network namespace.
docker run --rm \
  --network container:mdwiki-redis-cluster \
  -v "$PWD":/src \
  -w /src \
  -e MDWIKI_REDIS_INTEGRATION=1 \
  -e MDWIKI_REDIS_ENABLED=1 \
  -e MDWIKI_REDIS_ADDRS=127.0.0.1:7000,127.0.0.1:7001,127.0.0.1:7002 \
  -e MDWIKI_REDIS_CLUSTER_MODE=1 \
  golang:1.25 \
  go test ./internal/redisx -run TestRedisIntegration -count=1
docker stop mdwiki-redis-cluster
```

## License

MIT
