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

### Git auth in a real corporate setup

For production, treat git auth as two separate concerns:

1. **User identity**: users sign in with GitHub OAuth so mdwiki can attribute edits and, when available, push with the user token.
2. **Server fallback / bootstrap auth**: the server keeps a non-user credential in `MDWIKI_SERVER_GIT_TOKEN` so clones, pulls, and pushes still work before a user signs in or when a background/server-side git operation runs.

Recommended setup:

- Use **HTTPS** repo URLs in `spaces-registry.yaml` and `MDWIKI_ROOT_GIT_REPO`. The current git integration authenticates with tokens over HTTPS; do not use SSH remotes for managed corporate deployment.
- Create a **GitHub OAuth App** for mdwiki and configure:
  - `MDWIKI_GITHUB_CLIENT_ID`
  - `MDWIKI_GITHUB_CLIENT_SECRET`
  - `MDWIKI_GITHUB_CALLBACK`
- The app currently requests GitHub scopes: `read:user`, `user:email`, and `repo`. In most companies, that means the OAuth app must be reviewed/approved by the GitHub organization before end users can sign in successfully.
- Set `MDWIKI_SERVER_GIT_TOKEN` to a dedicated machine credential with read/write access to every wiki repo mdwiki manages. In practice this is usually:
  - a bot/service account PAT, or
  - an installation/access token from your internal credential broker if your company rotates tokens centrally.
- If your GitHub organization enforces **SAML SSO**, make sure both of these are authorized for the org:
  - the mdwiki OAuth app
  - the token behind `MDWIKI_SERVER_GIT_TOKEN`
- Keep the server token separate from personal user credentials. This avoids outages when an employee leaves, rotates devices, or loses repo access unexpectedly.
- Prefer a bot account with the smallest repo access footprint that still covers all configured spaces.

Example production env:

```bash
export MDWIKI_ROOT_GIT_REPO=https://github.example.com/Docs/platform-wiki.git
export MDWIKI_GITHUB_CLIENT_ID=...
export MDWIKI_GITHUB_CLIENT_SECRET=...
export MDWIKI_GITHUB_CALLBACK=https://wiki.example.com/auth/github/callback
export MDWIKI_SERVER_GIT_TOKEN=...
```

How to get the GitHub values:

1. In GitHub, go to **Settings** -> **Developer settings** -> **OAuth apps** -> **New OAuth App**.
2. Fill in:
   - **Application name**: a user-facing name such as `mdwiki Production`
   - **Homepage URL**: the public wiki URL, for example `https://wiki.example.com`
   - **Authorization callback URL**: the mdwiki backend callback endpoint, for example `https://wiki.example.com/auth/github/callback`
3. If you want mdwiki's device sign-in flow, enable **Device Flow** before you register or later from the OAuth app settings page.
4. Click **Register application**.
5. On the OAuth app page:
   - copy **Client ID** into `MDWIKI_GITHUB_CLIENT_ID`
   - click **Generate a new client secret**
   - copy that value into `MDWIKI_GITHUB_CLIENT_SECRET`
6. Set `MDWIKI_GITHUB_CALLBACK` to the same callback URL you configured in GitHub.

Important GitHub behavior:

- GitHub OAuth Apps support only **one callback URL** per app. In practice, create separate OAuth apps for `local`, `staging`, and `production`.
- The callback URL should point to the mdwiki backend route `/auth/github/callback`, not just the site root.
- mdwiki starts the browser flow on `/auth/github` and GitHub redirects the user's browser back to `/auth/github/callback`.
- If you use the device flow, mdwiki also uses:
  - `POST /auth/github/device/start`
  - `GET /auth/github/device/poll`
- GitHub's redirect URI rules are strict: host and port must match the configured callback, and the path must match that callback path or a subpath.
- GitHub references:
  - OAuth flow and redirect URL rules: https://docs.github.com/en/apps/oauth-apps/building-oauth-apps/authorizing-oauth-apps
  - Creating an OAuth app: https://github.com/settings/applications/new
  - Managing an existing OAuth app: https://github.com/settings/developers

What to whitelist, and where:

- In your **reverse proxy / ingress / API gateway**, route these paths to the Go backend:
  - `GET /auth/github`
  - `GET /auth/github/callback`
  - `POST /auth/github/device/start`
  - `GET /auth/github/device/poll`
- In your **identity-aware proxy / SSO middleware / auth gateway** that sits in front of mdwiki, allow these same `/auth/github*` endpoints to complete their flow without being trapped in another login challenge or redirect loop.
- If you keep a frontend and backend on different public origins, make sure:
  - `MDWIKI_GITHUB_CALLBACK` points at the backend origin
  - `MDWIKI_FRONTEND_ORIGIN` points at the UI origin, because after a successful GitHub login mdwiki redirects the browser back to `MDWIKI_FRONTEND_ORIGIN/`
- In GitHub organization settings, if OAuth app access restrictions are enabled, org owners must approve the mdwiki OAuth app under:
  - **Organizations** -> your org -> **Settings** -> **Third-party Access** -> **OAuth app policy**
- If your org limits who can request app approval, review that under:
  - **Organizations** -> your org -> **Settings** -> **Member privileges** -> **App access requests**

Operational notes:

- A signed-in user’s GitHub token is preferred for push/authorship; if there is no user session token, mdwiki falls back to `MDWIKI_SERVER_GIT_TOKEN`.
- For locked-down developer laptops or VDI environments, the built-in **device flow** can be easier to roll out than browser callback auth, but it still requires the same GitHub org approval and token/SAML authorization.
- Store `MDWIKI_SERVER_GIT_TOKEN` in your normal secret manager or runtime secret injection system; do not commit it to `.env`, `local.mk`, or container images.

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
