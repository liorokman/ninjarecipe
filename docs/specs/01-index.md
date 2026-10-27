# 01 — System Specification Index & Architecture Overview

This directory contains the authoritative, implementation-ready specifications for the **Ninja Woodfire Recipe Keeper** application.
It is structured so that an AI model or engineering team can implement the system end-to-end without ambiguity.

---

## Specification Documents

| Spec # | Document | Role / Domain | Key Contents |
|---|---|---|---|
| **00** | [00-brief.md](file:///home/lior/Projects/cooking/docs/specs/00-brief.md) | Ground Truth | Original user requirements, verified hardware constraints, core principles |
| **10** | [10-user-stories.md](file:///home/lior/Projects/cooking/docs/specs/10-user-stories.md) | End-User (Non-technical) | Personas, user journeys, outdoor UX at the oven, bilingual (EN/HE) expectations, frustrations to avoid |
| **20** | [20-architecture.md](file:///home/lior/Projects/cooking/docs/specs/20-architecture.md) | System Architect | Core system design, Go + SSR + htmx, OIDC authentication code flow, security, repo structure |
| **25** | [25-oven-settings.md](file:///home/lior/Projects/cooking/docs/specs/25-oven-settings.md) | Hardware / Oven Domain | Complete specification of Ninja Woodfire OO101UK settings, modes, pizza presets, smoke pellets, ranges |
| **30** | [30-data-model.md](file:///home/lior/Projects/cooking/docs/specs/30-data-model.md) | DB & Data Architect | Pure-Go SQLite schema (WAL mode), goose migrations, FTS5 trigram search, cascade deletion rules |
| **40** | [40-api.md](file:///home/lior/Projects/cooking/docs/specs/40-api.md) | API & Protocol Lead | Full HTTP routes, JSON REST API for local AI assistant import (`/api/v1/inbox`), RFC 7807 errors |
| **50** | [50-deployment-cosmos.md](file:///home/lior/Projects/cooking/docs/specs/50-deployment-cosmos.md) | Cosmos Cloud Admin | Dockerfile (distroless), Cosmos Server servapp setup, Authentik OIDC configuration, backups |
| **60** | [60-backend-golang.md](file:///home/lior/Projects/cooking/docs/specs/60-backend-golang.md) | Go Lead Developer | Package breakdown, CGO_ENABLED=0, connection pools, image pipeline (CatmullRom), LLM extension hook, Taskfile |
| **70** | [70-ui-frontend.md](file:///home/lior/Projects/cooking/docs/specs/70-ui-frontend.md) | Lightweight UI Specialist | htmx 2.x reactive flows, bilingual Hebrew RTL styling, Screen Wake Lock API for outdoor cooking, mobile layouts |
| **--** | [open-questions.md](file:///home/lior/Projects/cooking/docs/specs/open-questions.md) | Cross-functional Team | Open architectural questions and proposed defaults for household confirmation |

---

## System Architecture Summary

```mermaid
flowchart TD
    subgraph Clients["Clients & Devices"]
        Mobile["Galaxy Phones (S26+, S25+, S23 Note)<br/>Outdoors at Oven (Touch/Wake Lock)"]
        Tablet["Galaxy Tabs (S9, S6 Lite)<br/>Kitchen Counter (Multi-column)"]
        Desktop["Linux & Windows PCs<br/>Desktop Browsers (Management)"]
        AI["Local AI Assistant<br/>(Automated Web Search & Import)"]
    end

    subgraph Cosmos["Cosmos Cloud Host"]
        RP["Cosmos Reverse Proxy & TLS"]
        subgraph AppContainer["Docker Container: cooking-recipes"]
            Mux["Go 1.25 net/http Router"]
            Auth["Auth & Session Layer (OIDC + Bearer Token)"]
            AppLogic["Service Layer (Recipes, Oven Rules, Inbox, Search)"]
            ImagePipe["Image Pipeline (CatmullRom Resize + EXIF Strip)"]
            TemplateEng["Go html/template + htmx 2.x + i18n (EN/HE)"]
        end
        Vol[("Volume: /data<br/>recipes.db (WAL) + /media")]
    end

    subgraph Identity["Identity Provider"]
        Authentik["Authentik (OIDC Provider)"]
    end

    Mobile & Tablet & Desktop -->|HTTPS / Web UI| RP
    AI -->|POST /api/v1/inbox (Bearer Token)| RP
    RP --> Mux
    Mux --> Auth
    Auth <-->|OIDC Auth Code + PKCE| Authentik
    Auth --> AppLogic
    AppLogic --> TemplateEng
    AppLogic --> ImagePipe
    AppLogic --> Vol
    ImagePipe --> Vol
```
