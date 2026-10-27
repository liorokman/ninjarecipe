# 50 — Deployment: Docker, Local Dev, Cosmos Cloud & Authentik

Owner: Cosmos Cloud Admin (spec team). Status: draft v1.
Audience: the implementation model and the household admin who operates the server.

> [!IMPORTANT]
> Names of env vars, URL paths (`/auth/callback`, `/healthz`, …) and CLI subcommands below are
> the **deployment-side contract**. If `20-architecture.md` defines different names, **20-architecture.md
> wins** and this document must be updated to match. All conflicts must be resolved before implementation.

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
| Absolute URLs | App derives every absolute URL and cookie `Secure` flag from `RECIPES_BASE_URL`, **never** from `X-Forwarded-Host/Proto`. `X-Forwarded-For` is honoured only from `RECIPES_TRUSTED_PROXIES`. |
| Backups | Online SQLite snapshot via `VACUUM INTO` (app subcommand `backup`), then copy `/data/images`; off-host copy by Cosmos/host backup tooling. |
| Upgrades | Pinned semver image tags, backup → change tag → recreate; forward-only migrations at startup. Cosmos auto-update OFF. |

---

## 2. Container image (Dockerfile requirements)

### 2.1 Requirements (normative)

1. **Multi-stage**: stage `build` on `golang:<pinned>-bookworm` (or `-alpine`); final stage `gcr.io/distroless/static-debian12:nonroot`.
   - Distroless `static` is preferred over `scratch` because it ships CA certificates (needed for HTTPS
     calls to Authentik discovery/JWKS), `/etc/passwd` with the `nonroot` user, and tzdata.
2. **CGO disabled**: `CGO_ENABLED=0`, `-trimpath`, `-ldflags "-s -w -X <module>/internal/version.Version=${VERSION} -X ...Commit=${COMMIT}"`. The SQLite driver must be pure Go (e.g. `modernc.org/sqlite`) — per architecture.
3. **Static assets embedded** in the binary (`embed.FS`) — templates, CSS, JS, i18n files. The image contains only the binary and the empty `/data` skeleton.
4. **Non-root**: `USER 65532:65532`. No capabilities required. Must run with `read_only: true` root filesystem; all writes go to `/data` (and `/tmp` via tmpfs if the Go runtime needs it — multipart spill files must go to `RECIPES_DATA_DIR/tmp` instead, see §2.3).
5. **`/data` ownership**: the image must contain `/data` owned by `65532:65532` so that Docker initialises an **empty named volume** with the correct owner. (Bind mounts are not initialised — the host dir must be `chown 65532:65532`; see §5.4.)
6. **Exposed port**: `EXPOSE 8080`.
7. **Volume**: `VOLUME ["/data"]`.
8. **Healthcheck**: `HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 CMD ["/app/recipes","healthcheck"]`. The `healthcheck` subcommand performs `GET http://127.0.0.1:${PORT}/healthz` with a 2 s timeout and exits 0/1. It must not need env vars other than the listen address.
9. **Entrypoint**: `ENTRYPOINT ["/app/recipes"]`, `CMD ["serve"]`. Subcommands required by deployment: `serve`, `healthcheck`, `backup`, `migrate` (optional, `serve` migrates automatically), `version`.
10. **Labels** (OCI): `org.opencontainers.image.title|version|revision|source|created`.
11. **Multi-arch**: build `linux/amd64` and `linux/arm64` with `docker buildx` (the Cosmos host may be ARM, e.g. Raspberry Pi / Ampere).
12. **Signals**: `serve` handles `SIGTERM` with graceful shutdown (≤ 10 s drain), then closes the DB cleanly (WAL checkpoint).
13. Final image size target: < 30 MB.

### 2.2 Reference Dockerfile (spec block)

```dockerfile
# syntax=docker/dockerfile:1.7
ARG GO_VERSION=1.25
FROM --platform=$BUILDPLATFORM golang:${GO_VERSION}-bookworm AS build
ARG TARGETOS TARGETARCH VERSION=dev COMMIT=unknown
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath \
      -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
      -o /out/recipes ./cmd/recipes \
 && mkdir -p /out/data/images /out/data/backups /out/data/tmp

FROM gcr.io/distroless/static-debian12:nonroot
LABEL org.opencontainers.image.title="ninja-woodfire-recipe-keeper" \
      org.opencontainers.image.source="https://github.com/<owner>/<repo>"
COPY --from=build /out/recipes /app/recipes
COPY --from=build --chown=65532:65532 /out/data /data
ENV RECIPES_LISTEN_ADDR=":8080" \
    RECIPES_DATA_DIR="/data"
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
├── images/               # uploaded images, immutable files, content-addressed or UUID-named
│   └── ab/abcdef…​.webp  # 2-char fan-out dirs; originals + derived sizes per architecture
├── tmp/                  # upload staging; same filesystem as images/ for atomic rename; wiped at startup
└── backups/              # output of `recipes backup` (rotated, see §8)
```

The app must: create missing subdirectories at startup; fail fast with a clear log line if `/data`
is not writable (common bind-mount permission mistake); never store anything outside `/data`.

---

## 3. Environment variables (deployment contract)

Every secret var also accepts a `<NAME>_FILE` variant (path to a file whose trimmed content is the value; used with Docker secrets). If both are set, startup fails.

| Variable | Required | Default | Example / notes |
|---|---|---|---|
| `RECIPES_BASE_URL` | **yes** | — | `https://recipes.example.com` (prod), `http://localhost:8080` (dev). No trailing slash. Source of all absolute URLs, OIDC redirect URI (`${BASE_URL}/auth/callback`), and cookie `Secure` flag (`https` ⇒ Secure). |
| `RECIPES_LISTEN_ADDR` | no | `:8080` | |
| `RECIPES_DATA_DIR` | no | `/data` | |
| `RECIPES_DB_PATH` | no | `${DATA_DIR}/recipes.db` | |
| `RECIPES_OIDC_ISSUER` | **yes** | — | `https://auth.example.com/application/o/recipes/` (Authentik per-provider issuer, **trailing slash included** — must match the `iss` claim exactly). |
| `RECIPES_OIDC_CLIENT_ID` | **yes** | — | from Authentik provider |
| `RECIPES_OIDC_CLIENT_SECRET` | **yes** (secret) | — | from Authentik provider (confidential client) |
| `RECIPES_OIDC_SCOPES` | no | `openid profile email` | add `offline_access` only if architecture uses refresh tokens |
| `RECIPES_OIDC_GROUPS_CLAIM` | no | `groups` | |
| `RECIPES_OIDC_ALLOWED_GROUPS` | no | *(empty = any user Authentik lets through)* | comma list, e.g. `recipes-users,recipes-admins` (defence in depth; primary gate is the Authentik app binding, §6.5) |
| `RECIPES_OIDC_ADMIN_GROUPS` | no | *(empty = no admins)* | e.g. `recipes-admins` |
| `RECIPES_SESSION_KEY` | **yes** (secret) | — | ≥ 32 random bytes, base64. Generate: `openssl rand -base64 48`. Rotating it logs everyone out. |
| `RECIPES_TRUSTED_PROXIES` | no | *(empty = trust none)* | CIDR list whose `X-Forwarded-For` is honoured. Prod on Cosmos: `172.16.0.0/12,10.0.0.0/8,192.168.0.0/16` (Docker bridge ranges; container is network-isolated so only Cosmos can reach it). |
| `RECIPES_MAX_UPLOAD_BYTES` | no | `10485760` (10 MiB) | per image file |
| `RECIPES_MAX_REQUEST_BYTES` | no | `26214400` (25 MiB) | whole request body cap (`http.MaxBytesReader`) |
| `RECIPES_LOG_LEVEL` | no | `info` | `debug|info|warn|error` |
| `RECIPES_LOG_FORMAT` | no | `json` | `json|text` (`text` in dev) |
| `RECIPES_BACKUP_KEEP` | no | `7` | number of snapshots kept in `/data/backups` |
| `RECIPES_BACKUP_INTERVAL` | no | `24h` | in-process scheduled snapshot; `0` disables |
| `TZ` | no | `UTC` | e.g. `Asia/Jerusalem` / `Europe/London` (only affects log/backup filenames) |

The app must log (at startup, info level) the effective non-secret configuration and **must never log secrets**.

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
  automatically because `RECIPES_BASE_URL` is `http://`. Session cookie must be `SameSite=Lax` so it
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
RECIPES_OIDC_CLIENT_ID=recipes-dev
RECIPES_OIDC_CLIENT_SECRET=recipes-dev-secret
RECIPES_SESSION_KEY=ZGV2LXNlc3Npb24ta2V5LWRldi1zZXNzaW9uLWtleS1kZXYtc2Vzc2lvbg==
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
  RECIPES_OIDC_CLIENT_ID: ${RECIPES_OIDC_CLIENT_ID}
  RECIPES_OIDC_CLIENT_SECRET: ${RECIPES_OIDC_CLIENT_SECRET}

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
      RECIPES_BASE_URL: http://localhost:8080
      RECIPES_OIDC_ISSUER: http://auth.localhost:9000/application/o/recipes/
      RECIPES_OIDC_CLIENT_ID: ${RECIPES_OIDC_CLIENT_ID}
      RECIPES_OIDC_CLIENT_SECRET: ${RECIPES_OIDC_CLIENT_SECRET}
      RECIPES_OIDC_ADMIN_GROUPS: recipes-admins
      RECIPES_OIDC_ALLOWED_GROUPS: recipes-users,recipes-admins
      RECIPES_SESSION_KEY: ${RECIPES_SESSION_KEY}
      RECIPES_LOG_LEVEL: debug
      RECIPES_LOG_FORMAT: text
      RECIPES_TRUSTED_PROXIES: ""
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
      RECIPES_BASE_URL: http://localhost:8080
      RECIPES_OIDC_ISSUER: http://auth.localhost:9090/default
      RECIPES_OIDC_CLIENT_ID: recipes-dev
      RECIPES_OIDC_CLIENT_SECRET: anything
      RECIPES_OIDC_ADMIN_GROUPS: recipes-admins
      RECIPES_SESSION_KEY: ${RECIPES_SESSION_KEY}
      RECIPES_LOG_FORMAT: text
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
      client_id: !Env RECIPES_OIDC_CLIENT_ID
      client_secret: !Env RECIPES_OIDC_CLIENT_SECRET
      authorization_flow: !Find [authentik_flows.flow, [slug, default-provider-authorization-implicit-consent]]
      invalidation_flow: !Find [authentik_flows.flow, [slug, default-provider-invalidation-flow]]
      signing_key: !Find [authentik_crypto.certificatekeypair, [name, authentik Self-signed Certificate]]
      sub_mode: hashed_user_id
      include_claims_in_id_token: true
      issuer_mode: per_provider
      access_token_validity: minutes=10
      redirect_uris:
        - { url: "http://localhost:8080/auth/callback", matching_mode: strict }
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
        "RECIPES_BASE_URL=https://recipes.<domain>",
        "RECIPES_OIDC_ISSUER=https://auth.<domain>/application/o/recipes/",
        "RECIPES_OIDC_CLIENT_ID=<from-authentik>",
        "RECIPES_OIDC_CLIENT_SECRET=<from-authentik>",
        "RECIPES_OIDC_ALLOWED_GROUPS=recipes-users,recipes-admins",
        "RECIPES_OIDC_ADMIN_GROUPS=recipes-admins",
        "RECIPES_SESSION_KEY=<openssl rand -base64 48>",
        "RECIPES_TRUSTED_PROXIES=172.16.0.0/12,10.0.0.0/8,192.168.0.0/16",
        "RECIPES_MAX_UPLOAD_BYTES=10485760",
        "RECIPES_BACKUP_INTERVAL=24h",
        "RECIPES_BACKUP_KEEP=7",
        "TZ=Asia/Jerusalem"
      ],
      "labels": {
        "cosmos-force-network-secured": "true",
        "cosmos-auto-update": "false",
        "cosmos-icon": "https://recipes.<domain>/static/icon-192.png"
      },
      "volumes": [
        { "type": "volume", "source": "recipes-data", "target": "/data" }
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
  "volumes": { "recipes-data": {} }
}
```

If `extra_hosts` is needed (hairpin NAT, §5.6) add to the service: `"extra_hosts": ["auth.<domain>:host-gateway"]`.

### 5.4 Step-by-step deployment recipe

Prerequisites: Cosmos installed with HTTPS working; DNS `recipes.<domain>` and `auth.<domain>` → Cosmos host
(A/AAAA or wildcard); Authentik running (§6.1); image pushed to a registry reachable by the host.

1. **Build & push** on a dev machine: `task docker:release VERSION=1.0.0` (multi-arch push, §9).
   If the registry is private, add credentials in Cosmos (*ServApps → Docker registry auth* / `docker login` on the host).
2. **Authentik**: create the production provider/application (§6.2–6.5); copy client id/secret.
3. **Generate the session key**: `openssl rand -base64 48`.
4. **Create the ServApp**: *ServApps → Create → Import compose*, paste §5.3 with real values. Alternatively
   the *Create ServApp* form: image, name `recipes`, env vars (§3), volume `recipes-data` → `/data`,
   *Isolate network* on, no published ports.
   - Named volume (recommended): Docker copies the image's `/data` (owned 65532) into it automatically.
   - Bind mount alternative (easier host-side backups): `mkdir -p /srv/recipes/data && chown -R 65532:65532 /srv/recipes/data`, then mount `/srv/recipes/data:/data`.
5. **Route**: if not created by import, *URLs → Create URL*: target type ServApp → `recipes`, port `8080`,
   host `recipes.<domain>`; **Authentication: OFF**; SmartShield: ON; Timeout 120 s; no path prefix
   (the app assumes it is served at `/`).
6. **HTTPS**: nothing per-app if Cosmos uses a wildcard cert; otherwise Cosmos requests a cert for the new host
   on save. Confirm the padlock and that `http://` redirects to `https://` (Cosmos default).
7. **Verify**:
   - `https://recipes.<domain>/healthz` → `200 ok` (unauthenticated).
   - Cosmos ServApp shows *healthy*.
   - Login as a `recipes-users` member → lands on recipe list; a non-member gets Authentik's "access denied".
   - Upload a ~8 MB photo; create an API token and call `GET /api/v1/...` with `curl -H "Authorization: Bearer …"`.
   - Logs (*ServApps → recipes → Logs*) show no secret values.

### 5.5 Proxy headers & trusted proxy behaviour (app requirements)

- Cosmos (Go `httputil.ReverseProxy`) forwards `X-Forwarded-For`; depending on version/route options it may
  also set `X-Forwarded-Proto`/`X-Forwarded-Host` (`VerboseForwardHeader` adds more). **The app must not depend on them.**
- App rules:
  1. Scheme/host for redirects, OIDC `redirect_uri`, links in API responses, cookie `Secure` → from `RECIPES_BASE_URL` only.
  2. Client IP (logs, rate limiting, audit) = right-most untrusted address in `X-Forwarded-For`, **only if** the TCP peer is inside `RECIPES_TRUSTED_PROXIES`; else the TCP peer address.
  3. Set `Strict-Transport-Security` only when `BASE_URL` is https (Cosmos header hardening may also add it; duplicates are harmless).
  4. Ignore `X-Cosmos-*` identity headers entirely (Cosmos auth is off; trusting them would be a spoofing risk).
- Host header check: the app should reject (400) requests whose `Host` differs from `BASE_URL`'s host, except `/healthz`/`/readyz` (Docker healthcheck uses `127.0.0.1`). Keep "Spoof hostname"/"Overwrite Host header" OFF on the route so the original host is passed.

### 5.6 Container → Authentik connectivity

The app container must reach `https://auth.<domain>` (discovery, JWKS, token endpoint). Usually this works via
public DNS and the router's hairpin NAT. If the router lacks hairpin NAT (timeouts in logs at startup):
add `extra_hosts: ["auth.<domain>:host-gateway"]` so the request hits Cosmos's :443 on the host directly; the
TLS cert remains valid. The app must start even if Authentik is temporarily unreachable (lazy/retrying OIDC
discovery; `/readyz` reports degraded) so a reboot order race does not crash-loop it.

### 5.7 Upload size, timeouts, SmartShield

- Cosmos has no nginx-style `client_max_body_size`; request size is enforced **by the app**
  (`RECIPES_MAX_REQUEST_BYTES`, `RECIPES_MAX_UPLOAD_BYTES`) → `413` with a localized message.
- Keep route `MaxBandwidth = 0` (unlimited) and Timeout ≥ 120 s for slow mobile uploads over garden Wi-Fi.
- SmartShield may throttle bursts (e.g. assistant bulk submissions or a page loading many thumbnails). If
  users see `429`/blocked responses, raise the route throttle or add the assistant's IP/user to SmartShield
  exceptions. The app should send long-lived `Cache-Control: public, max-age=31536000, immutable` for
  hashed static assets and private caching for images to reduce request counts.
- If Cloudflare (proxied DNS) is in front of Cosmos, its free plan caps bodies at 100 MB — far above our limits.

### 5.8 Resource limits

| Resource | Limit | Reason |
|---|---|---|
| Memory | 384 MiB (reservation 64 MiB) | Go + SQLite are small; headroom for image decoding/resizing of 10 MB photos. If server-side resizing is not implemented, 256 MiB suffices. Set `GOMEMLIMIT` ≈ 80% of limit (e.g. `300MiB`). |
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

*Directory → Groups → Create*: `recipes-users` (may use the app), `recipes-admins` (app admin features, if any
are defined in architecture). Add household members. If the owner prefers "every Authentik user may use
the app", skip groups and the binding in §6.5 and leave `RECIPES_OIDC_ALLOWED_GROUPS` empty.

### 6.3 Provider + application (wizard)

*Applications → Applications → Create with provider* (wizard):

1. **Application**: Name `Recipe Keeper`, **Slug `recipes`** (part of the issuer URL), Launch URL `https://recipes.<domain>/`, optional icon.
2. **Provider type**: OAuth2/OpenID Connect.
3. **Provider settings**:
   - Name `recipes`; Authorization flow `default-provider-authorization-implicit-consent` (no consent screen for household use); Invalidation flow `default-provider-invalidation-flow`.
   - Client type **Confidential**; copy **Client ID** and **Client Secret**.
   - Redirect URIs: `strict` → `https://recipes.<domain>/auth/callback`. (Do **not** add localhost here; see §6.6.)
   - **Signing key**: `authentik Self-signed Certificate` (or a dedicated key). Required — without it Authentik signs ID tokens with HS256 using the client secret; the app expects **RS256** via JWKS.
   - Advanced: Subject mode `Based on the User's hashed ID` (stable `sub`, the app keys users by `iss`+`sub`); **Include claims in id_token: ON**; Issuer mode `Each provider has a different issuer` (default); Access code validity 1 min; Access token validity 10 min; Refresh token validity per architecture's session design (e.g. 30 days if `offline_access` is used).
   - Scopes (property mappings): `openid`, `email`, `profile` (the default `profile` mapping includes the `groups` claim with group names). Add `offline_access` only if architecture requires refresh tokens.
   - PKCE: the app always sends `code_challenge_method=S256`; Authentik accepts it for confidential clients.
4. Bindings: see §6.5. Save.

### 6.4 Values handed to the app

| Authentik | App env |
|---|---|
| `https://auth.<domain>/application/o/recipes/` (OpenID Configuration Issuer shown on provider page) | `RECIPES_OIDC_ISSUER` |
| Client ID | `RECIPES_OIDC_CLIENT_ID` |
| Client Secret | `RECIPES_OIDC_CLIENT_SECRET` |
| End session URL `https://auth.<domain>/application/o/recipes/end-session/` | discovered automatically (`end_session_endpoint`); app's logout redirects there with `id_token_hint` and `post_logout_redirect_uri=${BASE_URL}/` |

If the Authentik version supports typed redirect URIs, also add `https://recipes.<domain>/` as a
**post-logout** redirect URI.

### 6.5 Access restriction

*Application `recipes` → Policy / Group / User Bindings → Bind existing group*: `recipes-users` and
`recipes-admins` (policy engine mode *any*). Non-members are stopped by Authentik before a code is issued.
The app's `RECIPES_OIDC_ALLOWED_GROUPS` is a second line of defence (shows a friendly "not authorised" page).

### 6.6 Dev against the production Authentik (optional)

Preferred dev path is the local Authentik (§4). If a developer wants to test against the real IdP, create a
**separate** provider+application `recipes-dev` (slug `recipes-dev`) with redirect URI
`http://localhost:8080/auth/callback` (strict), and run the app with that issuer/client. Never add localhost
redirect URIs to the production provider.

### 6.7 Verification checklist

- Discovery JSON loads and `issuer` equals `RECIPES_OIDC_ISSUER` byte-for-byte.
- *Provider → Preview* (or log in once and inspect via app debug log at `debug` level) shows claims `sub`, `email`, `name`, `preferred_username`, `groups`.
- `id_token` header `alg` = `RS256`, `kid` present in JWKS.

---

## 7. Logging & monitoring

- Logs to stdout (JSON in prod); Cosmos shows them per ServApp. Fields: `ts, level, msg, req_id, method, path, status, dur_ms, client_ip, user_id` — never tokens, cookies, secrets, or `Authorization` headers.
- Cosmos monitoring tracks CPU/RAM/network per container; set a Cosmos alert for container unhealthy/stopped if available.

---

## 8. Backup, restore, upgrade

### 8.1 What to back up

`/data/recipes.db` (consistent snapshot, **never** a raw file copy of a live WAL DB) and `/data/images/`.
`/data/tmp` is disposable. `/data/backups` holds the snapshots themselves.

### 8.2 Online backup (app responsibility)

- Subcommand: `recipes backup [--out /data/backups]` — opens the DB read-only and runs
  `VACUUM INTO '/data/backups/recipes-YYYYMMDDTHHMMSSZ.db'` (safe while the server is running in WAL mode;
  produces a compact, consistent single file), runs `PRAGMA integrity_check` on the result, then
  writes `…/recipes-<ts>.images.txt` (list of image files referenced) and prunes snapshots beyond `RECIPES_BACKUP_KEEP`.
- The same routine runs in-process every `RECIPES_BACKUP_INTERVAL` (default 24 h) while `serve` is up.
- Run manually on Cosmos/host (distroless has no shell, exec the binary directly):
  `docker exec recipes /app/recipes backup`
- Images are immutable files (never modified in place; hard-deleted with their recipe). Copy them **after**
  the DB snapshot. A rare race (image deleted between snapshot and copy) yields a missing file; the app must
  render a placeholder for a missing image file instead of erroring.

### 8.3 Off-host copy

The in-volume snapshots do not protect against disk loss. Off-host options (pick one, see open questions):
- **Cosmos built-in backups** (Restic-based, in recent Cosmos versions): add the `recipes-data` volume (or
  bind path) to a backup job with a remote/second-disk repository, scheduled after the app's daily snapshot
  (e.g. app 02:00 UTC, Cosmos backup 03:00). Exclude `recipes.db-wal`, `recipes.db-shm`, `recipes.db` and `tmp/` from the job
  — the snapshots in `backups/` are the consistent copies.
- **Host cron + restic/rsync** of the bind-mount path with the same exclusions.

### 8.4 Restore

1. Stop the ServApp (`recipes`).
2. Copy the desired snapshot over the live DB and remove WAL/SHM:
   `recipes.db` ← `backups/recipes-<ts>.db`; delete `recipes.db-wal`, `recipes.db-shm`.
   (With a named volume, use a helper container: `docker run --rm -v recipes-data:/data -u 65532 busybox sh -c 'cp /data/backups/recipes-<ts>.db /data/recipes.db && rm -f /data/recipes.db-wal /data/recipes.db-shm'`.)
3. Restore `images/` from the off-host copy if lost (extra orphan images are harmless; a startup/maintenance job may GC unreferenced images older than 24 h).
4. Ensure ownership `65532:65532`. Start the ServApp; check `/readyz` and log in.
5. If the snapshot predates a schema migration, the app migrates it forward on start. Restoring a DB **newer** than the binary is refused (see §8.5).

### 8.5 Upgrade procedure

- Images are tagged `X.Y.Z` (immutable) plus `X.Y` and `latest` moving tags; Cosmos uses the **exact** tag.
- Steps: (1) read release notes; (2) `docker exec recipes /app/recipes backup`; (3) Cosmos *ServApps → recipes → Docker/Settings*: change image tag → *Update/Recreate* (Cosmos pulls and recreates the container, keeping volume, env, route); (4) watch logs for migration success and healthy status; (5) smoke test login + a recipe page.
- App behaviour: on `serve`, apply pending migrations in a transaction (forward-only, recorded in a `schema_migrations` table); if the DB's schema version is higher than the binary knows, **refuse to start** with a clear error (prevents downgrade corruption).
- Rollback: stop, restore the pre-upgrade snapshot (§8.4), set the previous tag, start.
- `cosmos-auto-update` stays `false` so migrations happen only when the admin intends; enabling it with a moving `X.Y` tag is acceptable for patch releases if the owner prefers.

---

## 9. Taskfile targets (build/deploy subset)

Taskfile at repo root; variables overridable (`task docker:release VERSION=1.2.3`). Other targets
(test, lint, generate) belong to the architecture/testing specs.

```yaml
version: "3"

vars:
  IMAGE: '{{.IMAGE | default "ghcr.io/<owner>/recipe-keeper"}}'
  VERSION: '{{.VERSION | default "dev"}}'
  COMMIT:
    sh: git rev-parse --short HEAD 2>/dev/null || echo unknown
  PLATFORMS: linux/amd64,linux/arm64
  DEV_COMPOSE: docker compose -f deploy/dev/docker-compose.yml --env-file deploy/dev/.env

tasks:
  docker:build:
    desc: Build local single-arch image {{.IMAGE}}:{{.VERSION}}
    cmds:
      - docker build --build-arg VERSION={{.VERSION}} --build-arg COMMIT={{.COMMIT}} -t {{.IMAGE}}:{{.VERSION}} -t recipe-keeper:dev .

  docker:run:
    desc: Run the image alone (needs deploy/dev/.env and a reachable IdP)
    cmds:
      - docker run --rm -p 8080:8080 --env-file deploy/dev/app.env -v recipes-local:/data {{.IMAGE}}:{{.VERSION}}

  docker:release:
    desc: Multi-arch build and push {{.IMAGE}}:{{.VERSION}} (+ X.Y and latest)
    preconditions:
      - sh: '[ "{{.VERSION}}" != "dev" ]'
        msg: "Set VERSION=X.Y.Z"
    cmds:
      - >
        docker buildx build --platform {{.PLATFORMS}} --push
        --build-arg VERSION={{.VERSION}} --build-arg COMMIT={{.COMMIT}}
        -t {{.IMAGE}}:{{.VERSION}} -t {{.IMAGE}}:{{splitList "." .VERSION | initial | join "."}} -t {{.IMAGE}}:latest .

  dev:init:
    desc: Create deploy/dev/.env from example with random secrets (idempotent)
    status: [test -f deploy/dev/.env]
    cmds:
      - cp deploy/dev/.env.example deploy/dev/.env
      - sed -i "s|^PG_PASS=.*|PG_PASS=$(openssl rand -hex 16)|" deploy/dev/.env
      - sed -i "s|^AUTHENTIK_SECRET_KEY=.*|AUTHENTIK_SECRET_KEY=$(openssl rand -base64 60 | tr -d '\n')|" deploy/dev/.env
      - sed -i "s|^RECIPES_SESSION_KEY=.*|RECIPES_SESSION_KEY=$(openssl rand -base64 48 | tr -d '\n')|" deploy/dev/.env

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
      RECIPES_BASE_URL: http://localhost:8080
      RECIPES_OIDC_ISSUER: http://auth.localhost:9000/application/o/recipes/
      RECIPES_DATA_DIR: ./dev-data
      RECIPES_LOG_FORMAT: text
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

  secrets:session-key:
    desc: Print a new RECIPES_SESSION_KEY
    cmds: ['openssl rand -base64 48']
```

---

## 10. Acceptance criteria (deployment)

1. `task dev:up` on a clean Linux machine with Docker yields a working login as `alice` at `http://localhost:8080` within 2 minutes, with no manual Authentik clicks.
2. `task dev:up:mock` works without Authentik.
3. Image runs as UID 65532, read-only rootfs, on amd64 and arm64; `docker inspect` shows a healthy status.
4. With an empty named volume the app starts without permission errors; with a root-owned bind mount it exits with a clear "data dir not writable" error.
5. Behind Cosmos with Cosmos auth OFF: browser login works over HTTPS, cookies are `Secure; HttpOnly; SameSite=Lax`, the API works with a bearer token, a 10 MB image uploads, an 11 MB image gets `413`.
6. Spoofed `X-Forwarded-For` from an untrusted peer is ignored; `X-Forwarded-Host` never changes generated URLs.
7. `docker exec recipes /app/recipes backup` produces a snapshot passing `integrity_check` while the app is serving writes; restore procedure §8.4 recovers it.
8. Upgrading the tag keeps data; a downgrade to a binary with an older schema refuses to start.
