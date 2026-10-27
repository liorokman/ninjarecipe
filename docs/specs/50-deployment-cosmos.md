# 50 — Deployment: Docker, Local Dev, Cosmos Cloud & Authentik

Owner: Cosmos Cloud Admin (spec team). Status: draft v1.
Audience: the implementation model and the household admin who operates the server.

> [!IMPORTANT]
> This document is canonical only for **operations**: the Dockerfile, the dev compose stack, Cosmos and Authentik
> configuration, backup/restore/upgrade procedures. Env vars, CLI subcommands, health endpoints and Taskfile targets
> are defined in `20-architecture.md` (§12, §17, §13, §18); routes in `40-api.md`. This document only *uses* them.

---

## 1. Summary of decisions

| Topic | Decision |
|---|---|
| Image | Multi-stage build → `gcr.io/distroless/static-debian12:nonroot`, CGO-free static Go binary, UID/GID `65532`. |
| Port | App listens on `:8080` (HTTP only; TLS terminated by Cosmos). |
| Persistence | One volume mounted at `/data` (SQLite DB + images + local backups). |
| Health | `GET /healthz` (liveness, no auth) + `GET /readyz` (DB reachable); Docker `HEALTHCHECK` runs the binary's `healthcheck` subcommand (no shell/curl in distroless). |
| Local dev IdP | **Real Authentik** (server + worker + postgres; no Redis since Authentik 2025.10) auto-provisioned by a **blueprint**. Lightweight alternative profile `mock` with `mock-oauth2-server` for CI/e2e. |
| Dev hostnames | App `http://localhost:8080`, Authentik `http://auth.localhost:9000` (browsers resolve `*.localhost` to loopback; container uses `extra_hosts: host-gateway`). |
| Production | Cosmos ServApp from a cosmos-compose import; Cosmos URL route `recipes.<domain>` → `recipes:8080`, Let's Encrypt HTTPS by Cosmos, **Cosmos "Authentication required" = OFF**, SmartShield ON, network isolated. |
| Absolute URLs | App derives every absolute URL and cookie `Secure` flag from `APP_BASE_URL`, **never** from `X-Forwarded-Host/Proto`. `X-Forwarded-For` is honoured only from `APP_TRUSTED_PROXIES`. |
| Backups | Online SQLite snapshot via `VACUUM INTO` (app subcommand `backup`, also daily in-process), then copy `/data/media`; off-host copy by Cosmos/host backup tooling. |
| Upgrades | Pinned semver image tags, backup → change tag → recreate; forward-only migrations at startup. Cosmos auto-update OFF. |

---

## 2. Container image (Dockerfile requirements)

### 2.1 Requirements (normative)

1. **Multi-stage**: stage `build` on `golang:<pinned>-bookworm` (or `-alpine`); final stage `gcr.io/distroless/static-debian12:nonroot`.
   - Distroless `static` is preferred over `scratch` because it ships CA certificates (needed for HTTPS
     calls to Authentik discovery/JWKS), `/etc/passwd` with the `nonroot` user, and tzdata.
2. **CGO disabled**: `CGO_ENABLED=0`, `-trimpath`, `-ldflags "-s -w -X <module>/internal/version.Version=${VERSION} -X ...Commit=${COMMIT}"`. The SQLite driver must be pure Go (e.g. `modernc.org/sqlite`) — per architecture.
3. **Static assets embedded** in the binary (`embed.FS`) — templates, CSS, JS, i18n files. The image contains only the binary and the empty `/data` skeleton.
4. **Non-root**: `USER 65532:65532`. No capabilities required. Must run with `read_only: true` root filesystem; all writes go to `/data` (the Dockerfile sets `TMPDIR=/data/tmp`, so multipart spill files and SQLite temp files land on the volume; see §2.3).
5. **`/data` ownership**: the image must contain `/data` owned by `65532:65532` so that Docker initialises an **empty named volume** with the correct owner. (Bind mounts are not initialised — the host dir must be `chown 65532:65532`; see §5.4.)
6. **Exposed port**: `EXPOSE 8080`.
7. **Volume**: `VOLUME ["/data"]`.
8. **Healthcheck**: `HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 CMD ["/app/recipes","healthcheck"]`. The `healthcheck` subcommand is defined in `20 §13`; it must not need env vars other than `APP_LISTEN_ADDR`.
9. **Entrypoint**: `ENTRYPOINT ["/app/recipes"]`, `CMD ["serve"]`. Subcommands: `20 §17`.
10. **Labels** (OCI): `org.opencontainers.image.title|version|revision|source|created`.
11. **Multi-arch**: build `linux/amd64` and `linux/arm64` with `docker buildx` (the Cosmos host may be ARM, e.g. Raspberry Pi / Ampere).
12. **Signals**: `serve` handles `SIGTERM` per `20 §4` (≤ 8 s drain, WAL checkpoint), inside Docker's default 10 s stop grace period.
13. Final image size target: < 30 MB.

### 2.2 Reference Dockerfile (spec block)

```dockerfile
# syntax=docker/dockerfile:1.7
ARG GO_VERSION=1.25
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS build
ARG TARGETOS TARGETARCH VERSION=dev COMMIT=unknown DATE=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT} -X main.date=${DATE}" \
      -o /out/recipes ./cmd/recipes \
 && mkdir -p /out/data/media /out/data/backups /out/data/tmp

FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.title="ninja-woodfire-recipe-keeper" \
      org.opencontainers.image.source="https://github.com/<owner>/<repo>"
COPY --from=build /out/recipes /app/recipes
COPY --from=build --chown=65532:65532 /out/data /data
ENV APP_LISTEN_ADDR=":8080" \
    APP_DATA_DIR="/data" \
    TMPDIR="/data/tmp"
USER 65532:65532
EXPOSE 8080
VOLUME ["/data"]
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
  CMD ["/app/recipes","healthcheck"]
ENTRYPOINT ["/app/recipes"]
CMD ["serve"]
```

`.dockerignore` must exclude: `.git`, `data/`, `dev-data/`, `*.db*`, `deploy/dev/.env`, `node_modules`, `dist/`, `coverage*`.

### 2.3 `/data` layout (normative)

```
/data/
├── recipes.db            # SQLite main file (WAL mode)
├── recipes.db-wal        # WAL (never copy alone; see §8)
├── recipes.db-shm
├── media/                # processed images, immutable (layout: 20 §9)
│   └── ab/abcdef…/       # 2-char fan-out, one dir per storage key
│       ├── full.jpg
│       └── thumb.jpg
├── tmp/                  # TMPDIR: upload staging, SQLite temp; same filesystem as media/ for atomic rename; wiped at startup
└── backups/              # output of `recipes backup` (rotated, see §8)
```

The app must: create missing subdirectories at startup; fail fast with a clear log line if `/data`
is not writable (common bind-mount permission mistake); never store anything outside `/data`.

---

## 3. Environment variables per environment

Definitions and defaults: `20 §12` (the only env-var table). Values used by deployment:

| Variable | Dev (`deploy/dev`) | Production (Cosmos) |
|---|---|---|
| `APP_BASE_URL` | `http://localhost:8080` | `https://recipes.<domain>` |
| `OIDC_ISSUER_URL` | `http://auth.localhost:9000/application/o/recipes/` (mock: `http://auth.localhost:9090/default`) | `https://auth.<domain>/application/o/recipes/` |
| `OIDC_CLIENT_ID` / `OIDC_CLIENT_SECRET` | from `.env` (blueprint uses the same values) | from the Authentik provider |
| `OIDC_ALLOWED_GROUPS` | `recipes-users,recipes-admins` | `recipes-users,recipes-admins` |
| `OIDC_ADMIN_GROUP` | `recipes-admins` | `recipes-admins` |
| `APP_TRUSTED_PROXIES` | empty | `172.16.0.0/12,10.0.0.0/8,192.168.0.0/16` (Docker bridge ranges; the container is network-isolated so only Cosmos reaches it) |
| `LOG_FORMAT` / `LOG_LEVEL` | `text` / `debug` | `json` / `info` |
| `TZ` | host default | e.g. `Asia/Jerusalem` |
| `GOMEMLIMIT` | — | `300MiB` |

There is no session secret: sessions are opaque random tokens stored hashed in the DB (`20 §5.3`).

---

## 4. Local development

### 4.1 Recommendation

Use a **real Authentik** in compose, auto-configured by a **blueprint** so `task dev:up` produces a
working login with zero clicks. Rationale: identical issuer/claims/group behaviour to production
(trailing-slash issuer, `groups` claim, RS256 signing, end-session endpoint), so OIDC bugs surface
locally. Cost: ~1 GB RAM and ~60 s first start.

Provide a second, **lighter profile `mock`** using `ghcr.io/navikt/mock-oauth2-server` for CI,
e2e tests and quick UI work (no setup, any username accepted, claims configurable via JSON).

> [!NOTE]
> Since Authentik **2025.10, Redis is no longer required** (all cache/tasks moved to PostgreSQL).
> The stack is therefore postgres + server + worker. Pin `AUTHENTIK_TAG` to a release ≥ 2025.10.

### 4.2 Hostnames and localhost redirect URIs

- App: `http://localhost:8080`; redirect URI `http://localhost:8080/auth/callback`.
- Authentik: `http://auth.localhost:9000`; issuer `http://auth.localhost:9000/application/o/recipes/`.
- Why a different hostname for Authentik: **cookies are not isolated by port**, so two apps on
  `localhost:8080` and `localhost:9000` share a cookie jar; separate hosts avoid collisions.
- Chrome and Firefox resolve `*.localhost` to `127.0.0.1` natively (no `/etc/hosts` needed). Windows/macOS
  CLI tools may not — add `127.0.0.1 auth.localhost` to the hosts file if `curl` fails.
- **Issuer consistency problem**: the browser reaches Authentik at `auth.localhost:9000`, but inside the
  app container `localhost` is the container itself. Authentik derives the issuer from the request `Host`,
  so the app must call Authentik using the **same** host. Solution: the app container gets
  `extra_hosts: ["auth.localhost:host-gateway"]`, and Authentik publishes port 9000 on the host. Discovery,
  token exchange and JWKS then use exactly the issuer URL the browser sees. (Works on Docker Engine ≥ 20.10 and Docker Desktop.)
- Authentik allows `http://` redirect URIs; plain-HTTP localhost is fine for dev. Cookies use `Secure=false`
  automatically because `APP_BASE_URL` is `http://`. Session cookie must be `SameSite=Lax` so it
  survives the top-level redirect back from the IdP.
- Running the app on the host (`task dev:run`, `go run`) uses the same env values; `auth.localhost`
  resolves to loopback on most Linux systems (systemd-resolved), otherwise add the hosts entry.

### 4.3 Files

```
deploy/
├── dev/
│   ├── docker-compose.yml
│   ├── .env.example                # copied to .env by `task dev:init`
│   ├── authentik/blueprints/recipes-dev.yaml
│   └── mock-oidc/config.json
└── cosmos/
    ├── cosmos-compose.json          # §5.3
    └── README.md                    # points to this spec
```

### 4.4 `deploy/dev/.env.example` (spec block)

```dotenv
# Generated/edited by `task dev:init`. Never commit .env.
AUTHENTIK_TAG=2025.10
PG_PASS=devpostgres-change-me
AUTHENTIK_SECRET_KEY=dev-secret-key-change-me-at-least-50-chars-long-xxxxxxxxxxxx
AUTHENTIK_BOOTSTRAP_PASSWORD=admin
AUTHENTIK_BOOTSTRAP_EMAIL=admin@example.test
OIDC_CLIENT_ID=recipes-dev
OIDC_CLIENT_SECRET=recipes-dev-secret
```

### 4.5 `deploy/dev/docker-compose.yml` (spec block)

```yaml
name: recipes-dev

x-authentik-env: &authentik-env
  AUTHENTIK_SECRET_KEY: ${AUTHENTIK_SECRET_KEY:?set in .env}
  AUTHENTIK_POSTGRESQL__HOST: postgresql
  AUTHENTIK_POSTGRESQL__NAME: authentik
  AUTHENTIK_POSTGRESQL__USER: authentik
  AUTHENTIK_POSTGRESQL__PASSWORD: ${PG_PASS:?set in .env}
  AUTHENTIK_ERROR_REPORTING__ENABLED: "false"
  AUTHENTIK_DISABLE_UPDATE_CHECK: "true"
  # consumed by the blueprint via !Env
  OIDC_CLIENT_ID: ${OIDC_CLIENT_ID}
  OIDC_CLIENT_SECRET: ${OIDC_CLIENT_SECRET}

services:
  app:
    build:
      context: ../..
      dockerfile: Dockerfile
      args: { VERSION: dev }
    image: recipe-keeper:dev
    profiles: ["", "authentik"]       # default profile
    ports: ["8080:8080"]
    environment:
      APP_BASE_URL: http://localhost:8080
      OIDC_ISSUER_URL: http://auth.localhost:9000/application/o/recipes/
      OIDC_CLIENT_ID: ${OIDC_CLIENT_ID}
      OIDC_CLIENT_SECRET: ${OIDC_CLIENT_SECRET}
      OIDC_ADMIN_GROUP: recipes-admins
      OIDC_ALLOWED_GROUPS: recipes-users,recipes-admins
      LOG_LEVEL: debug
      LOG_FORMAT: text
      APP_TRUSTED_PROXIES: ""
    extra_hosts:
      - "auth.localhost:host-gateway"
    volumes:
      - app-data:/data
    read_only: true
    depends_on:
      authentik-server: { condition: service_healthy }
    restart: unless-stopped

  postgresql:
    image: docker.io/library/postgres:16-alpine
    environment:
      POSTGRES_DB: authentik
      POSTGRES_USER: authentik
      POSTGRES_PASSWORD: ${PG_PASS:?set in .env}
    volumes: [authentik-db:/var/lib/postgresql/data]
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -d authentik -U authentik"]
      interval: 10s
      timeout: 5s
      retries: 10
      start_period: 20s
    restart: unless-stopped

  authentik-server:
    image: ghcr.io/goauthentik/server:${AUTHENTIK_TAG:-2025.10}
    command: server
    environment:
      <<: *authentik-env
      AUTHENTIK_BOOTSTRAP_PASSWORD: ${AUTHENTIK_BOOTSTRAP_PASSWORD}
      AUTHENTIK_BOOTSTRAP_EMAIL: ${AUTHENTIK_BOOTSTRAP_EMAIL}
    ports: ["9000:9000"]               # http://auth.localhost:9000 ; admin user: akadmin
    volumes:
      - ./authentik/blueprints:/blueprints/custom:ro
    depends_on:
      postgresql: { condition: service_healthy }
    healthcheck:
      test: ["CMD", "ak", "healthcheck"]
      interval: 15s
      timeout: 5s
      retries: 20
      start_period: 60s
    restart: unless-stopped

  authentik-worker:
    image: ghcr.io/goauthentik/server:${AUTHENTIK_TAG:-2025.10}
    command: worker
    environment:
      <<: *authentik-env
      AUTHENTIK_BOOTSTRAP_PASSWORD: ${AUTHENTIK_BOOTSTRAP_PASSWORD}
      AUTHENTIK_BOOTSTRAP_EMAIL: ${AUTHENTIK_BOOTSTRAP_EMAIL}
    volumes:
      - ./authentik/blueprints:/blueprints/custom:ro
    depends_on:
      postgresql: { condition: service_healthy }
    restart: unless-stopped

  # ---- Lightweight alternative: `docker compose --profile mock up app-mock mock-oidc` ----
  mock-oidc:
    image: ghcr.io/navikt/mock-oauth2-server:2.1.10
    profiles: ["mock"]
    ports: ["9090:8080"]
    environment:
      SERVER_PORT: "8080"
      JSON_CONFIG_PATH: /config/config.json
    volumes: [./mock-oidc/config.json:/config/config.json:ro]

  app-mock:
    image: recipe-keeper:dev
    build: { context: ../.., dockerfile: Dockerfile, args: { VERSION: dev } }
    profiles: ["mock"]
    ports: ["8080:8080"]
    environment:
      APP_BASE_URL: http://localhost:8080
      OIDC_ISSUER_URL: http://auth.localhost:9090/default
      OIDC_CLIENT_ID: recipes-dev
      OIDC_CLIENT_SECRET: anything
      OIDC_ADMIN_GROUP: recipes-admins
      LOG_FORMAT: text
    extra_hosts: ["auth.localhost:host-gateway"]
    volumes: [app-data-mock:/data]

volumes:
  app-data:
  app-data-mock:
  authentik-db:
```

Notes for the implementer:
- Compose `profiles: [""]` is not valid in all Compose versions; if rejected, drop `profiles` from `app`
  and the Authentik services (they then start by default) and keep `profiles: ["mock"]` only on the mock services.
- `mock-oauth2-server` with `interactiveLogin: true` shows a form where any username/claims can be entered;
  `config.json` should set `"interactiveLogin": true` and a default `groups` claim
  `["recipes-users"]`.
- First start: Authentik needs ~60 s to migrate; `app` waits on `authentik-server` health.

### 4.6 Dev Authentik blueprint `deploy/dev/authentik/blueprints/recipes-dev.yaml` (spec block)

Creates groups, two test users, the OAuth2 provider and the application. Uses `!Env` so the client
id/secret match `.env`.

```yaml
version: 1
metadata:
  name: recipes-dev
  labels:
    blueprints.goauthentik.io/instantiate: "true"
entries:
  - model: authentik_core.group
    id: grp-users
    identifiers: { name: recipes-users }
  - model: authentik_core.group
    id: grp-admins
    identifiers: { name: recipes-admins }

  - model: authentik_core.user
    identifiers: { username: alice }
    attrs:
      name: Alice Dev
      email: alice@example.test
      password: alice            # dev only
      groups: [!KeyOf grp-users, !KeyOf grp-admins]
  - model: authentik_core.user
    identifiers: { username: bob }
    attrs:
      name: Bob Dev
      email: bob@example.test
      password: bob              # dev only
      groups: [!KeyOf grp-users]

  - model: authentik_providers_oauth2.oauth2provider
    id: provider
    identifiers: { name: recipes-dev }
    attrs:
      client_type: confidential
      client_id: !Env OIDC_CLIENT_ID
      client_secret: !Env OIDC_CLIENT_SECRET
      authorization_flow: !Find [authentik_flows.flow, [slug, default-provider-authorization-implicit-consent]]
      invalidation_flow: !Find [authentik_flows.flow, [slug, default-provider-invalidation-flow]]
      signing_key: !Find [authentik_crypto.certificatekeypair, [name, authentik Self-signed Certificate]]
      sub_mode: hashed_user_id
      include_claims_in_id_token: true
      issuer_mode: per_provider
      access_token_validity: minutes=10
      redirect_uris:
        - { url: "http://localhost:8080/auth/callback", matching_mode: strict }
        - { url: "http://localhost:8080/auth/logged-out", matching_mode: strict }   # post-logout redirect
      property_mappings:
        - !Find [authentik_providers_oauth2.scopemapping, [scope_name, openid]]
        - !Find [authentik_providers_oauth2.scopemapping, [scope_name, email]]
        - !Find [authentik_providers_oauth2.scopemapping, [scope_name, profile]]

  - model: authentik_core.application
    id: app
    identifiers: { slug: recipes }
    attrs:
      name: Recipe Keeper (dev)
      provider: !KeyOf provider
      meta_launch_url: http://localhost:8080/
      policy_engine_mode: any

  - model: authentik_policies.policybinding
    identifiers: { target: !KeyOf app, group: !KeyOf grp-users, order: 0 }
  - model: authentik_policies.policybinding
    identifiers: { target: !KeyOf app, group: !KeyOf grp-admins, order: 1 }
```

> [!WARNING]
> Blueprint attribute names occasionally change between Authentik releases (e.g. `redirect_uris`
> became a list of `{url, matching_mode}` objects in 2024.10). If the blueprint fails to apply
> (Admin → Customization → Blueprints shows status), compare with an exported blueprint from the
> pinned Authentik version and fix. The **application slug must be `recipes`** because the issuer URL contains it.
> If user `password` is not accepted via blueprint, `task dev:init` documents setting passwords in the admin UI.

---

## 5. Production on Cosmos Cloud

### 5.1 Cosmos concepts used

- **ServApp**: a Docker container managed by Cosmos (create via form, via **Import compose**
  (docker-compose / cosmos-compose YAML or JSON), or via the Market).
- **URL / Route**: Cosmos's built-in Go reverse proxy on ports 80/443. Each route maps a host
  (and optional path prefix) to a target (`mode: SERVAPP`, target `http://<container>:<port>`).
  Per-route toggles: **Authentication required** (`AuthEnabled`), **SmartShield**, throttle, timeout,
  bot blocking, CORS, IP whitelist, "restrict to Constellation" (Cosmos VPN), header options.
- **HTTPS**: Cosmos obtains/renews Let's Encrypt certs automatically (HTTP-01 or DNS-01 / wildcard, configured
  once in Cosmos *Config → HTTPS*). The app never sees TLS.
- **Network isolation** ("Isolate network" / label `cosmos-force-network-secured=true`): Cosmos puts the
  container on its own bridge network shared only with Cosmos and removes published ports, so all
  traffic must go through the proxy.
- **Labels Cosmos reads** (useful subset): `cosmos-force-network-secured`, `cosmos-auto-update`
  (+ `cosmos-auto-update-type`), `cosmos-stack` / `cosmos-stack-main` (grouping), `cosmos-icon` (dashboard icon URL).
- **Cosmos auth layer**: when `AuthEnabled` is on, unauthenticated requests are redirected to the Cosmos
  login page (Cosmos's own user DB) and Cosmos forwards identity headers to the app.

### 5.2 Cosmos "Authentication required": **turn it OFF for this route**

Justification:
1. **API clients break**: the AI assistant calls the REST API with `Authorization: Bearer <token>`. Cosmos
   auth would intercept those requests and answer with a login redirect/401 because the assistant has no
   Cosmos session.
2. **Double login, two user stores**: Cosmos users are not Authentik users; household members would log in
   to Cosmos and then to Authentik, and identity would be inconsistent.
3. **OIDC flow interference**: the `/auth/callback` redirect from Authentik must reach the app untouched.
4. The app already enforces authentication on every non-public route (OIDC session or API token); Authentik
   enforces group membership (§6.5).

Compensating controls kept ON: **SmartShield** (rate/abuse limits), **Block common bots**, HTTPS-only,
network isolation. Leave **"Block API abuse"** OFF (it can reject non-browser clients such as the assistant).
Optional hardening if the household never uses the app outside home/VPN: **Restrict to Constellation** or an
inbound IP whitelist — but the assistant's source IP then must be allowed too.

### 5.3 cosmos-compose for import (spec block)

Save as `deploy/cosmos/cosmos-compose.json` and paste into *ServApps → Create → Import compose*.
Replace `<…>` placeholders. Field names follow the cosmos-compose format as exported by Cosmos;
**verify against an export from the installed Cosmos version** (create any ServApp with a URL, then
*Export*), because Cosmos may add/rename route fields between versions.

```json
{
  "services": {
    "recipes": {
      "image": "ghcr.io/<owner>/recipe-keeper:1.0.0",
      "container_name": "recipes",
      "restart": "unless-stopped",
      "read_only": true,
      "environment": [
        "APP_BASE_URL=https://recipes.<domain>",
        "OIDC_ISSUER_URL=https://auth.<domain>/application/o/recipes/",
        "OIDC_CLIENT_ID=<from-authentik>",
        "OIDC_CLIENT_SECRET=<from-authentik>",
        "OIDC_ALLOWED_GROUPS=recipes-users,recipes-admins",
        "OIDC_ADMIN_GROUP=recipes-admins",
        "APP_TRUSTED_PROXIES=172.16.0.0/12,10.0.0.0/8,192.168.0.0/16",
        "BACKUP_AT=03:30",
        "BACKUP_KEEP=7",
        "GOMEMLIMIT=300MiB",
        "TZ=Asia/Jerusalem"
      ],
      "labels": {
        "cosmos-force-network-secured": "true",
        "cosmos-auto-update": "false",
        "cosmos-icon": "https://recipes.<domain>/icon-192.png"
      },
      "volumes": [
        { "type": "bind", "source": "/srv/recipes/data", "target": "/data" }
      ],
      "deploy": { "resources": { "limits": { "cpus": "1.0", "memory": "384M" } } },
      "routes": [
        {
          "Name": "recipes",
          "Description": "Ninja Woodfire Recipe Keeper",
          "UseHost": true,
          "Host": "recipes.<domain>",
          "UsePathPrefix": false,
          "Target": "http://recipes:8080",
          "Mode": "SERVAPP",
          "AuthEnabled": false,
          "AdminOnly": false,
          "SmartShield": { "Enabled": true },
          "BlockCommonBots": true,
          "BlockAPIAbuse": false,
          "Timeout": 120000,
          "ThrottlePerMinute": 3000,
          "MaxBandwidth": 0,
          "CORSOrigin": "",
          "VerboseForwardHeader": false,
          "DisableHeaderHardening": false
        }
      ]
    }
  },
  "volumes": {}
}
```

If `extra_hosts` is needed (hairpin NAT, §5.6) add to the service: `"extra_hosts": ["auth.<domain>:host-gateway"]`.

### 5.4 Step-by-step deployment recipe

Prerequisites: Cosmos installed with HTTPS working; DNS `recipes.<domain>` and `auth.<domain>` → Cosmos host
(A/AAAA or wildcard); Authentik running (§6.1); image pushed to a registry reachable by the host.

1. **Build & push** on a dev machine: `task docker:release VERSION=1.0.0` (multi-arch push, §9).
   If the registry is private, add credentials in Cosmos (*ServApps → Docker registry auth* / `docker login` on the host).
2. **Authentik**: create the production provider/application (§6.2–6.5); copy client id/secret.
3. (No app secret to generate: the only secret is the OIDC client secret.)
4. **Create the ServApp**: *ServApps → Create → Import compose*, paste §5.3 with real values. Alternatively
   the *Create ServApp* form: image, name `recipes`, env vars (§3), bind mount `/srv/recipes/data` → `/data`,
   *Isolate network* on, no published ports.
   - **Bind mount (production default, needed for host backups §8.3)**: `mkdir -p /srv/recipes/data && chown -R 65532:65532 /srv/recipes/data`, then mount `/srv/recipes/data:/data`.
   - Named volume (alternative without host backups): Docker copies the image's `/data` (owned 65532) into it automatically.
5. **Route**: if not created by import, *URLs → Create URL*: target type ServApp → `recipes`, port `8080`,
   host `recipes.<domain>`; **Authentication: OFF**; SmartShield: ON; Timeout 120 s; no path prefix
   (the app assumes it is served at `/`).
6. **HTTPS**: nothing per-app if Cosmos uses a wildcard cert; otherwise Cosmos requests a cert for the new host
   on save. Confirm the padlock and that `http://` redirects to `https://` (Cosmos default).
7. **Verify**:
   - `https://recipes.<domain>/healthz` → `200 ok` (unauthenticated).
   - Cosmos ServApp shows *healthy*.
   - Login as a `recipes-users` member → lands on recipe list; a non-member gets Authentik's "access denied".
   - Upload a ~8 MB photo; create an API token and call `GET /api/v1/categories` with `curl -H "Authorization: Bearer …"`.
   - Logs (*ServApps → recipes → Logs*) show no secret values.

### 5.5 Proxy headers & trusted proxy behaviour (app requirements)

- Cosmos (Go `httputil.ReverseProxy`) forwards `X-Forwarded-For`; depending on version/route options it may
  also set `X-Forwarded-Proto`/`X-Forwarded-Host` (`VerboseForwardHeader` adds more). **The app must not depend on them.**
- The app's rules for absolute URLs, client IP and the Host check are in `20 §4`. The app ignores `X-Cosmos-*`
  identity headers entirely (Cosmos auth is off; trusting them would be a spoofing risk).
- Keep "Spoof hostname"/"Overwrite Host header" OFF on the route so the original `Host` reaches the app's Host check.

### 5.6 Container → Authentik connectivity

The app container must reach `https://auth.<domain>` (discovery, JWKS, token endpoint). Usually this works via
public DNS and the router's hairpin NAT. If the router lacks hairpin NAT (timeouts in logs at startup):
add `extra_hosts: ["auth.<domain>:host-gateway"]` so the request hits Cosmos's :443 on the host directly; the
TLS cert remains valid. The app must start even if Authentik is temporarily unreachable (lazy/retrying OIDC
discovery; `/readyz` reports `oidc: pending`, `20 §13`) so a reboot order race does not crash-loop it.

### 5.7 Upload size, timeouts, SmartShield

- Cosmos has no nginx-style `client_max_body_size`; request size is enforced **by the app**
  (`MAX_IMAGE_MB`, `API_MAX_BODY_MB`, `20 §8`) → `413` with a localized message. Browsers resize photos before
  upload (`20 §9`), so typical uploads are < 1 MB.
- Keep route `MaxBandwidth = 0` (unlimited) and Timeout ≥ 120 s for slow mobile uploads over garden Wi-Fi.
- SmartShield may throttle bursts (e.g. assistant bulk submissions or a page loading many thumbnails). If
  users see `429`/blocked responses, raise the route throttle or add the assistant's IP/user to SmartShield
  exceptions. The app should send long-lived `Cache-Control: public, max-age=31536000, immutable` for
  hashed static assets and private caching for images to reduce request counts.
- If Cloudflare (proxied DNS) is in front of Cosmos, its free plan caps bodies at 100 MB — far above our limits.

### 5.8 Resource limits

| Resource | Limit | Reason |
|---|---|---|
| Memory | 384 MiB (reservation 64 MiB) | Go + SQLite are small; headroom for one image decode at a time (`IMAGE_WORKERS=1`, ≤ 50 MP ≈ 200 MB). `GOMEMLIMIT=300MiB`. |
| CPU | 1.0 | image resizing bursts |
| PIDs | 256 | |
| Disk | volume on host disk; alert when < 1 GB free (Cosmos monitoring) | images dominate; ~1–3 MB per recipe |

---

## 6. Authentik setup (production)

### 6.1 Running Authentik alongside Cosmos

- Typical: install Authentik from the **Cosmos Market** or import Authentik's official docker-compose
  (postgres + server + worker; no Redis for ≥ 2025.10) as a Cosmos stack.
- Cosmos route `auth.<domain>` → `authentik-server:9000`, **Authentication OFF** (Authentik is the IdP),
  SmartShield ON, Timeout ≥ 60 s; websockets must pass (Cosmos supports them).
- Authentik must see the original scheme/host; leave host overwriting off. Check *Admin → System → System Info*
  that URLs are `https://auth.<domain>`; the discovery doc
  `https://auth.<domain>/application/o/recipes/.well-known/openid-configuration` must show `https://` URLs.
  If it shows `http://`, enable Cosmos `VerboseForwardHeader` (passes `X-Forwarded-Proto`) on the Authentik route.
- Authentik and the app are separate ServApps/stacks; they communicate only via the public HTTPS URL.

### 6.2 Groups (optional but recommended)

*Directory → Groups → Create*: `recipes-users` (may use the app), `recipes-admins` (may manage the category list,
`20 §7`). Add household members. If the owner prefers "every Authentik user may use
the app", skip groups and the binding in §6.5 and leave `OIDC_ALLOWED_GROUPS` empty.

### 6.3 Provider + application (wizard)

*Applications → Applications → Create with provider* (wizard):

1. **Application**: Name `Recipe Keeper`, **Slug `recipes`** (part of the issuer URL), Launch URL `https://recipes.<domain>/`, optional icon.
2. **Provider type**: OAuth2/OpenID Connect.
3. **Provider settings**:
   - Name `recipes`; Authorization flow `default-provider-authorization-implicit-consent` (no consent screen for household use); Invalidation flow `default-provider-invalidation-flow`.
   - Client type **Confidential**; copy **Client ID** and **Client Secret**.
   - Redirect URIs: `strict` → `https://recipes.<domain>/auth/callback`. (Do **not** add localhost here; see §6.6.)
   - **Signing key**: `authentik Self-signed Certificate` (or a dedicated key). Required — without it Authentik signs ID tokens with HS256 using the client secret; the app expects **RS256** via JWKS.
   - Advanced: Subject mode `Based on the User's hashed ID` (stable `sub`, the app keys users by `iss`+`sub`); **Include claims in id_token: ON**; Issuer mode `Each provider has a different issuer` (default); Access code validity 1 min; Access token validity 10 min. Refresh tokens are not used (the app keeps its own session, `20 §5.3`).
   - Scopes (property mappings): `openid`, `email`, `profile` (the default `profile` mapping includes the `groups` claim with group names). Do not add `offline_access`.
   - PKCE: the app always sends `code_challenge_method=S256`; Authentik accepts it for confidential clients.
4. Bindings: see §6.5. Save.

### 6.4 Values handed to the app

| Authentik | App env |
|---|---|
| `https://auth.<domain>/application/o/recipes/` (OpenID Configuration Issuer shown on provider page) | `OIDC_ISSUER_URL` |
| Client ID | `OIDC_CLIENT_ID` |
| Client Secret | `OIDC_CLIENT_SECRET` |
| End session URL `https://auth.<domain>/application/o/recipes/end-session/` | discovered automatically (`end_session_endpoint`); app's logout redirects there with `id_token_hint` and `post_logout_redirect_uri=${APP_BASE_URL}/auth/logged-out` |

Also add `https://recipes.<domain>/auth/logged-out` (strict) to the provider's redirect URIs so the post-logout
redirect is accepted.

### 6.5 Access restriction

The **primary** gate is the app's `OIDC_ALLOWED_GROUPS` check (`20 §5.2`): a household member who is not yet in
`recipes-users` signs in successfully at Authentik and then sees the app's friendly page "You can't get in yet.
Ask Lior to add you." (story A3).

Optional defence in depth: *Application `recipes` → Policy / Group / User Bindings → Bind existing group*
`recipes-users` and `recipes-admins` (policy engine mode *any*). Then non-members are stopped by Authentik with
Authentik's own "access denied" page instead of the friendly one. Recommended only if the Authentik instance has
users outside the household.

### 6.5a Forgotten passwords

**Decision (owner): no self-service recovery; Lior resets passwords manually** in *Authentik → Directory → Users →
<user> → Set password* (or sends a one-time *recovery link* from the same page and shares it over WhatsApp).
No recovery flow or SMTP is configured. The app's sign-in page shows the line "Forgot your password? Ask Lior."
(`70 §4.10`).

### 6.6 Dev against the production Authentik (optional)

Preferred dev path is the local Authentik (§4). If a developer wants to test against the real IdP, create a
**separate** provider+application `recipes-dev` (slug `recipes-dev`) with redirect URI
`http://localhost:8080/auth/callback` (strict), and run the app with that issuer/client. Never add localhost
redirect URIs to the production provider.

### 6.7 Verification checklist

- Discovery JSON loads and `issuer` equals `OIDC_ISSUER_URL` byte-for-byte.
- *Provider → Preview* (or log in once and inspect via app debug log at `debug` level) shows claims `sub`, `email`, `name`, `preferred_username`, `groups`.
- `id_token` header `alg` = `RS256`, `kid` present in JWKS.

---

## 7. Logging & monitoring

- Logs to stdout (JSON in prod); Cosmos shows them per ServApp. Fields: `ts, level, msg, req_id, method, path, status, dur_ms, client_ip, user_id` — never tokens, cookies, secrets, or `Authorization` headers.
- Cosmos monitoring tracks CPU/RAM/network per container; set a Cosmos alert for container unhealthy/stopped if available.

---

## 8. Backup, restore, upgrade

### 8.1 What to back up

`/data/recipes.db` (consistent snapshot, **never** a raw file copy of a live WAL DB) and `/data/media/`.
`/data/tmp` is disposable. `/data/backups` holds the snapshots themselves.

### 8.2 Online backup (app responsibility)

- Behaviour of `recipes backup` and the daily in-process run (`BACKUP_AT`): `20 §16`.
- Run manually on Cosmos/host (distroless has no shell, exec the binary directly):
  `docker exec recipes /app/recipes backup`
- Media files are immutable (never modified in place; hard-deleted with their recipe). Copy them **after**
  the DB snapshot. A rare race (image deleted between snapshot and copy) yields a missing file, which the app
  renders as a placeholder (`20 §9`).

### 8.3 Off-host copy

The in-volume snapshots do not protect against disk loss. **Decision (owner): host cron + restic.** Production
therefore uses the **bind mount** `/srv/recipes/data:/data` (§5.4 alternative) so the host can read the files
directly.

Host setup (root crontab on the Cosmos host), running after the app's daily snapshot (`BACKUP_AT=03:30`):

```bash
# /etc/cron.d/recipes-backup
45 4 * * * root /usr/local/bin/recipes-offsite-backup.sh >> /var/log/recipes-backup.log 2>&1
```

```bash
#!/bin/sh
# /usr/local/bin/recipes-offsite-backup.sh
set -eu
export RESTIC_REPOSITORY=<repo, e.g. sftp:backup@nas:/restic/recipes or s3:…>
export RESTIC_PASSWORD_FILE=/root/.restic-recipes-pass
DATA=/srv/recipes/data
restic backup "$DATA/backups" "$DATA/media" --tag recipes
restic forget --tag recipes --keep-daily 14 --keep-weekly 8 --keep-monthly 12 --prune
```

- Only `backups/` (consistent DB snapshots) and `media/` are copied; the live `recipes.db`, `-wal`, `-shm` and `tmp/`
  are never copied.
- Monthly: `restic check`, and a test restore of the newest snapshot into a scratch directory (`restic restore latest
  --target /tmp/recipes-restore-test`), opening the DB with `sqlite3 … 'PRAGMA integrity_check'`.
- rsync to a second disk is an acceptable substitute for restic (`rsync -a --delete` of the same two directories),
  but keeps no history.

### 8.4 Restore

1. Stop the ServApp (`recipes`).
2. Copy the desired snapshot over the live DB and remove WAL/SHM:
   `recipes.db` ← `backups/recipes-<ts>.db`; delete `recipes.db-wal`, `recipes.db-shm`.
   (With a named volume, use a helper container: `docker run --rm -v recipes-data:/data -u 65532 busybox sh -c 'cp /data/backups/recipes-<ts>.db /data/recipes.db && rm -f /data/recipes.db-wal /data/recipes.db-shm'`.)
3. Restore `media/` from the off-host copy if lost (extra orphan files are harmless; the nightly sweeper removes them, `20 §9`).
4. Ensure ownership `65532:65532`. Start the ServApp; check `/readyz` and log in.
5. If the snapshot predates a schema migration, the app migrates it forward on start. Restoring a DB **newer** than the binary is refused (see §8.5).

### 8.5 Upgrade procedure

- Images are tagged `X.Y.Z` (immutable) plus `X.Y` and `latest` moving tags; Cosmos uses the **exact** tag.
- Steps: (1) read release notes; (2) `docker exec recipes /app/recipes backup`; (3) Cosmos *ServApps → recipes → Docker/Settings*: change image tag → *Update/Recreate* (Cosmos pulls and recreates the container, keeping volume, env, route); (4) watch logs for migration success and healthy status; (5) smoke test login + a recipe page.
- App behaviour on `serve`: `20 §14` (forward-only goose migrations; refuses to start on a DB newer than the binary).
- Rollback: stop, restore the pre-upgrade snapshot (§8.4), set the previous tag, start.
- `cosmos-auto-update` stays `false` so migrations happen only when the admin intends; enabling it with a moving `X.Y` tag is acceptable for patch releases if the owner prefers.

---

## 9. Taskfile targets (build/deploy subset)

Reference implementation of the deployment-related targets listed in `20 §18` (that list is canonical;
names here must match it). Variables are overridable (`task docker:release VERSION=1.2.3`).

```yaml
version: "3"

vars:
  IMAGE: '{{.IMAGE | default "ghcr.io/<owner>/recipe-keeper"}}'
  VERSION: '{{.VERSION | default "dev"}}'
  COMMIT:
    sh: git rev-parse --short HEAD 2>/dev/null || echo unknown
  DATE:
    sh: date -u +%Y-%m-%dT%H:%M:%SZ
  PLATFORMS: linux/amd64,linux/arm64
  DEV_COMPOSE: docker compose -f deploy/dev/docker-compose.yml --env-file deploy/dev/.env

tasks:
  docker:build:
    desc: Build local single-arch image {{.IMAGE}}:{{.VERSION}}
    cmds:
      - docker build --build-arg VERSION={{.VERSION}} --build-arg COMMIT={{.COMMIT}} --build-arg DATE={{.DATE}} -t {{.IMAGE}}:{{.VERSION}} -t recipe-keeper:dev .

  docker:release:
    desc: Multi-arch build and push {{.IMAGE}}:{{.VERSION}} (+ X.Y and latest)
    preconditions:
      - sh: '[ "{{.VERSION}}" != "dev" ]'
        msg: "Set VERSION=X.Y.Z"
    cmds:
      - >
        docker buildx build --platform {{.PLATFORMS}} --push
        --build-arg VERSION={{.VERSION}} --build-arg COMMIT={{.COMMIT}} --build-arg DATE={{.DATE}}
        -t {{.IMAGE}}:{{.VERSION}} -t {{.IMAGE}}:{{splitList "." .VERSION | initial | join "."}} -t {{.IMAGE}}:latest .

  dev:init:
    desc: Create deploy/dev/.env from example with random secrets (idempotent)
    status: [test -f deploy/dev/.env]
    cmds:
      - cp deploy/dev/.env.example deploy/dev/.env
      - sed -i "s|^PG_PASS=.*|PG_PASS=$(openssl rand -hex 16)|" deploy/dev/.env
      - sed -i "s|^AUTHENTIK_SECRET_KEY=.*|AUTHENTIK_SECRET_KEY=$(openssl rand -base64 60 | tr -d '\n')|" deploy/dev/.env

  dev:up:
    desc: Start app + local Authentik (http://localhost:8080, IdP http://auth.localhost:9000, users alice/alice, bob/bob)
    deps: [dev:init]
    cmds:
      - '{{.DEV_COMPOSE}} up -d --build'

  dev:up:mock:
    desc: Start app with lightweight mock OIDC server
    deps: [dev:init]
    cmds:
      - '{{.DEV_COMPOSE}} --profile mock up -d --build app-mock mock-oidc'

  dev:run:
    desc: Run the Go server on the host against the compose Authentik (fast reload loop)
    cmds:
      - '{{.DEV_COMPOSE}} up -d postgresql authentik-server authentik-worker'
      - go run ./cmd/recipes serve
    env:
      APP_BASE_URL: http://localhost:8080
      OIDC_ISSUER_URL: http://auth.localhost:9000/application/o/recipes/
      APP_DATA_DIR: ./dev-data
      APP_ENV: development
      APP_TEMPLATES_DIR: ./web
      TMPDIR: ./dev-data/tmp
      LOG_FORMAT: text
    dotenv: [deploy/dev/.env]

  dev:logs:
    cmds: ['{{.DEV_COMPOSE}} logs -f --tail=100 {{.CLI_ARGS}}']

  dev:down:
    cmds: ['{{.DEV_COMPOSE}} --profile mock down']

  dev:reset:
    desc: Destroy dev containers AND volumes (app data + Authentik DB)
    prompt: This deletes all local dev data. Continue?
    cmds: ['{{.DEV_COMPOSE}} --profile mock down -v']

  dev:backup:
    desc: Take an online backup inside the dev app container
    cmds: ['{{.DEV_COMPOSE}} exec app /app/recipes backup']
```

---

## 10. Acceptance criteria (deployment)

1. `task dev:up` on a clean Linux machine with Docker yields a working login as `alice` at `http://localhost:8080` within 2 minutes, with no manual Authentik clicks.
2. `task dev:up:mock` works without Authentik.
3. Image runs as UID 65532, read-only rootfs, on amd64 and arm64; `docker inspect` shows a healthy status.
4. With an empty named volume the app starts without permission errors; with a root-owned bind mount it exits with a clear "data dir not writable" error.
5. Behind Cosmos with Cosmos auth OFF: browser login works over HTTPS, cookies are `Secure; HttpOnly; SameSite=Lax`, the API works with a bearer token, a 15 MB image uploads (with JS disabled, i.e. no client resize), a 16 MB image gets `413`.
6. Spoofed `X-Forwarded-For` from an untrusted peer is ignored; `X-Forwarded-Host` never changes generated URLs.
7. `docker exec recipes /app/recipes backup` produces a snapshot passing `integrity_check` while the app is serving writes; restore procedure §8.4 recovers it.
8. Upgrading the tag keeps data; a downgrade to a binary with an older schema refuses to start.
