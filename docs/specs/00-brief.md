# Project Brief — Ninja Woodfire Recipe Keeper

Source of truth for the specification team. Do not edit; propose changes in `open-questions.md`.

## Problem
The owner has a **Ninja Woodfire Outdoor Oven (OO101 UK, "Artisan Pizza Maker and BBQ Smoker")**
(https://www.sharkninja.co.uk/ninja-woodfire-outdoor-oven-artisan-pizza-maker-and-bbq-smoker-with-stand-cover/OO101BNDUK.html).
The household cooks recipes on it and keeps forgetting the exact oven settings and the recipe for each dish.

## Requirements (from the owner)
1. Store recipes; find them via **label/category-based search**.
2. For each recipe show the **oven settings** required (mode, temperature, time, woodfire/smoke, etc.) and the recipe itself.
3. Runs in a **Docker container**, hosted on **Cosmos Cloud / Cosmos Server** (azukaar, self-hosted). For development: localhost + `docker compose` file.
4. Implementation: **Go** backend + **HTML browser UI**. Use **Taskfile** (taskfile.dev) where useful.
5. Authentication: **OIDC against self-hosted Authentik**.
6. Every authenticated user has **private recipes**, plus **shared recipes** visible to all users.
7. UI must work well on: Galaxy S26+, S25+, Galaxy Note/S23, Galaxy Tab S9, Galaxy Tab S6 Lite, Linux & Windows PCs on Chrome and Firefox.
8. Recipes may include **images of the end product**.
9. **Future**: an LLM will perform categorization (design the hook now, do not implement).
10. A personal **local AI assistant** searches the Internet for recipes and submits them. Integration: **REST API with per-user API tokens**; submitted recipes land in that user's **Inbox** to accept/reject. Once accepted, the recipe is **owned by the accepting user**, who can make it shared.
11. Deletion: deleting a shared recipe removes it from the server entirely; deleting a private recipe removes it entirely (incl. images). Hard delete.

## Decisions already made by the owner
- IDP: Authentik.
- Hosting: Cosmos Cloud (azukaar).
- Database: **SQLite** in a mounted volume, single container.
- UI languages: **English + Hebrew (full RTL support)**.
- Units: **Metric only** (°C, g, ml, cm).
- AI assistant: REST API + per-user API tokens + Inbox.

## Deliverable
A complete, actionable specification in `docs/specs/` that can be handed to an implementation model with no further context.
