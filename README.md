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

go run ./cmd/wiki
```

Health: `GET http://localhost:8080/health`

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
| Redis | `internal/redisx` + `MDWIKI_REDIS_URL` (publish stub; subscribe wiring optional) |
| Search | `internal/search` SQLite FTS5 |
| Git | `internal/gitops` clone / pull / commit / push |
| OAuth | `internal/oauth`, `internal/api` `/auth/github`, `/auth/github/device/*` |
| Comments | `internal/comments` thread JSON helpers |
| Layout | [docs/GIT_LAYOUT.md](docs/GIT_LAYOUT.md) |

## Production

Build the UI: `cd web && npm run build`, then serve `web/dist` with a reverse proxy or add a static file route to the Go server.

## License

MIT
