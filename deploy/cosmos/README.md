# Cosmos Cloud deployment

Import `cosmos-compose.json` in Cosmos (URLs -> Add -> Import compose), set the placeholders (`APP_BASE_URL`,
`OIDC_ISSUER_URL`, `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`), and turn Cosmos "Authentication required" OFF for the route:
the app does its own OIDC login. Full step-by-step recipe, Authentik setup, backups and upgrade procedure:
`docs/specs/50-deployment-cosmos.md` (sections 5, 6, 8).
