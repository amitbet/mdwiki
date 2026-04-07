# Run API + Vite together: `make dev`
# Backend listens on SERVER_PORT; Vite proxies /api, /auth, /health, /ws to BACKEND_URL.
#
# Secrets: copy local.mk.example to local.mk and set MDWIKI_GITHUB_CLIENT_SECRET (local.mk is gitignored).

-include local.mk

SERVER_PORT ?= 8080
UI_PORT ?= 5173
BACKEND_URL ?= http://127.0.0.1:$(SERVER_PORT)
FRONTEND_ORIGIN ?= http://localhost:$(UI_PORT)

# GitHub OAuth app "mdwiki local" — Homepage URL http://localhost:5173/ , Device Flow enabled
MDWIKI_GITHUB_CLIENT_ID ?= Ov23li9lCWMd6ho8zGcG
MDWIKI_GITHUB_CALLBACK ?= http://localhost:$(SERVER_PORT)/auth/github/callback
# Redirect login needs MDWIKI_GITHUB_CLIENT_SECRET (see local.mk.example / local.mk).

export MDWIKI_FRONTEND_ORIGIN := $(FRONTEND_ORIGIN)
export MDWIKI_GITHUB_CLIENT_ID
export MDWIKI_GITHUB_CALLBACK

.PHONY: dev server ui build install env-check

## Run Go server and Vite dev server in parallel (open http://localhost:$(UI_PORT))
dev:
	$(MAKE) -j2 server ui

server:
	MDWIKI_LISTEN=:$(SERVER_PORT) go run ./cmd/wiki

ui:
	cd web && MDWIKI_BACKEND=$(BACKEND_URL) npm run dev -- --port $(UI_PORT) --host localhost

install:
	cd web && npm install
	go mod download

build: install
	cd web && npm run build
	go build -o bin/mdwiki ./cmd/wiki

## Print variables the Go server sees (secret shown as set/missing only)
env-check:
	@echo "MDWIKI_LISTEN        = :$(SERVER_PORT)"
	@echo "MDWIKI_FRONTEND_ORIGIN = $(MDWIKI_FRONTEND_ORIGIN)"
	@echo "MDWIKI_GITHUB_CLIENT_ID = $(MDWIKI_GITHUB_CLIENT_ID)"
	@echo "MDWIKI_GITHUB_CALLBACK  = $(MDWIKI_GITHUB_CALLBACK)"
	@if [ -n "$(MDWIKI_GITHUB_CLIENT_SECRET)" ]; then echo "MDWIKI_GITHUB_CLIENT_SECRET = set"; else echo "MDWIKI_GITHUB_CLIENT_SECRET = MISSING (OAuth redirect will fail)"; fi
	@echo "Vite MDWIKI_BACKEND  = $(BACKEND_URL)"
	@echo "Optional unset: MDWIKI_DATA MDWIKI_REGISTRY MDWIKI_SESSION_SECRET MDWIKI_REDIS_URL MDWIKI_SERVER_GIT_TOKEN MDWIKI_DEV"
	@echo "--- env passed to server (non-secret MDWIKI_*) ---"
	@MDWIKI_LISTEN=:$(SERVER_PORT) env | grep '^MDWIKI_' | grep -v SECRET | sort
	@MDWIKI_LISTEN=:$(SERVER_PORT) sh -c 'if [ -n "$$MDWIKI_GITHUB_CLIENT_SECRET" ]; then echo "MDWIKI_GITHUB_CLIENT_SECRET in process env: yes"; else echo "MDWIKI_GITHUB_CLIENT_SECRET in process env: NO"; fi'
