# TODO — viewdoc

Tracks the build. See [AGENTS.md](./AGENTS.md) for stable constraints.

## v1 — minimal working stack ✓

v1 stack verified `2026-05-17`:

- `docker compose up -d` brings up control-center + 2× kasm + 2× webtop.
- `/healthz` returns `{ready:4,total:4}`.
- Proxy: kasm slots return HTTP 401 (NoVNC password prompt — by design); webtop slots return HTTP 200.
- Sidecar: POST `/api/slot/{i}/params` writes `/tmp/viewdoc.{json,env}` inside each slot and fires `viewdoc.sh`.
- Headless-browser DOM render not attempted — sandbox policy blocked `chromium --no-sandbox`. Curl-equivalent of the page-load asset fetch + JS XHR confirmed end-to-end.

Punch list (now all done):

### Control-center (Go)
- [ ] `go.mod` (module `github.com/dockette/viewdoc`, Go 1.23)
- [ ] `cmd/control-center/main.go` — wire env, mux, server
- [ ] `internal/slots/pool.go` — slot table from `VIEWER_SLOTS` env (comma-separated hostnames; default `viewer-1,viewer-2,viewer-3`)
- [ ] `internal/proxy/proxy.go` — `httputil.ReverseProxy` per slot, strips `/slot/{i}` prefix, passes through WebSocket Upgrade
- [ ] `internal/api/handlers.go` — `/healthz`, `/api/slots`, `/api/slot/{i}/params` (POST → JSON forward to `http://<slot[i]>:7000/params`)
- [ ] `internal/sanitize/params.go` — key regex, count + size limits
- [ ] `web/` (embed.FS) — `index.html` + `app.js` (iframe + popstate/hash/poll → POST params, 500ms debounce)
- [ ] root `Dockerfile` — multi-stage `golang:1.23-alpine` → `gcr.io/distroless/static-debian12`

### Viewer images (both share one sidecar)
- [ ] `cmd/sidecar/main.go` — HTTP listener on `:7000`, `POST /params`, atomic write + fork hook
- [ ] `.docker/viewdoc.sh` — dispatch by extension (vlc / xdg-open / firefox)
- [ ] `.docker/webtop/Dockerfile` — `FROM lscr.io/linuxserver/webtop:ubuntu-xfce` + vlc + xdg-utils + sidecar binary + hook, autostarted via `/custom-cont-init.d/`
- [ ] `.docker/kasm/Dockerfile` — `FROM kasmweb/ubuntu-noble-desktop` + vlc + sidecar + hook, autostarted via `/dockerstartup/`

### Stack & infra
- [ ] `docker-compose.yml` — control-center on `:8080` + mixed pool: 2× kasm (`viewer-kasm-1/2`) + 2× webtop (`viewer-webtop-1/2`) on the same network
- [ ] `Makefile` — `build` / `up` / `down` / `logs` / `push` with `IMAGE`/`TAG` vars
- [ ] `.dockerignore`
- [ ] `README.md` — quickstart, port table, env vars (apidoc vibe)

### Verification
- [ ] `docker compose build` clean
- [ ] `docker compose up -d` — all containers `Up`
- [ ] `curl http://localhost:8080/healthz` returns 3 ready slots
- [ ] browse `http://localhost:8080/` via agent's browser — iframe loads slot 0, URL param propagates, file opens in viewer

## Out of scope for v1
- TLS / certs
- Auth, session leasing, TTLs
- Round-robin slot picking from a queue
- GH Actions multi-arch publish
