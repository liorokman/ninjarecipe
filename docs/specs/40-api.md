# 40 — API Specification (HTTP & REST)

Owner: Architect / Backend Lead. Status: authoritative for implementation.
Companion docs: `20-architecture.md`, `25-oven-settings.md`, `30-data-model.md`.

## 1. Overview & Protocol Conventions

- **Base URL:** `/`
- **JSON REST API Base:** `/api/v1`
- **Error Format:** RFC 7807 `application/problem+json` for JSON API. HTML error pages for browser routes.
- **CSRF Protection:** Browser routes enforce standard SameSite cookies and Go 1.25 `http.CrossOriginProtection`. API endpoints (`/api/v1/*`) rely on Bearer token authentication in `Authorization: Bearer <token>`.
- **Response Headers:** `Content-Type: application/json; charset=utf-8` on JSON routes.

---

## 2. Authentication & Authorization Matrix

| Route | Method | Auth Scheme | Authz Rule |
|---|---|---|---|
| `/auth/login` | GET | None | Public redirect to Authentik |
| `/auth/callback` | GET | None | Public OIDC callback |
| `/auth/logout` | POST | Session | Invalidate local session & Authentik end-session |
| `/` | GET | Session | Redirect to `/recipes` if logged in, else login |
| `/recipes` | GET | Session | List shared + user's private active recipes |
| `/recipes/new` | GET | Session | Recipe creation form |
| `/recipes` | POST | Session | Create recipe (active, private by default) |
| `/recipes/{id}` | GET | Session | View recipe (must be shared or owned) |
| `/recipes/{id}/edit` | GET | Session | Edit recipe (owner if private; any authenticated user if shared) |
| `/recipes/{id}` | POST/PUT | Session | Update recipe (owner if private; any authenticated user if shared) |
| `/recipes/{id}` | DELETE | Session | Hard delete recipe & media (owner if private; any authenticated user if shared) |
| `/recipes/{id}/cook` | GET | Session | High-visibility cooking step mode (at-oven) |
| `/recipes/{id}/cook-log` | POST | Session | Add entry to "Cooked it" log |
| `/inbox` | GET | Session | View current user's inbox items |
| `/inbox/{id}/accept` | POST | Session | Move inbox recipe to active (owned by user) |
| `/inbox/{id}/reject` | POST | Session | Hard delete inbox recipe & media |
| `/settings/tokens` | GET/POST/DEL | Session | Manage user's personal API tokens |
| `/media/{year}/{month}/{filename}` | GET | Session / Bearer | Check authorization to view recipe image |
| `/api/v1/inbox` | POST | Bearer Token | Assistant imports recipe directly to user's inbox |
| `/api/v1/recipes` | GET | Bearer Token | Assistant reads user's recipes |
| `/api/v1/categories` | GET | Bearer Token / Session | Retrieve category & tag list |
| `/healthz` | GET | None | Return `{ "status": "ok" }` |

---

## 3. Local AI Assistant Import API

The local AI assistant runs autonomously, finds recipes online, and imports them on behalf of a specific user using a personal API token generated in the Web UI (`nwf_live_...`).

### `POST /api/v1/inbox`
Imports a recipe as an unverified draft directly into the token owner's **Inbox**.

#### Request Headers
```http
POST /api/v1/inbox HTTP/1.1
Host: cooking.internal
Authorization: Bearer nwf_live_839f28a7c2938471...
Content-Type: application/json
```

#### Request Body Schema (JSON)
```json
{
  "$schema": "http://json-schema.org/draft-07/schema#",
  "title": "AssistantRecipeImport",
  "type": "object",
  "required": ["title"],
  "properties": {
    "title": { "type": "string", "minLength": 1, "maxLength": 200 },
    "description": { "type": "string", "maxLength": 2000 },
    "source_url": { "type": "string", "format": "uri" },
    "source_name": { "type": "string", "maxLength": 100 },
    "language": { "type": "string", "enum": ["en", "he"], "default": "en" },
    "prep_time_minutes": { "type": "integer", "minimum": 0 },
    "cook_time_minutes": { "type": "integer", "minimum": 0 },
    "servings": { "type": "integer", "minimum": 1 },
    "difficulty": { "type": "string", "enum": ["easy", "medium", "hard"] },
    "categories": {
      "type": "array",
      "items": { "type": "string" },
      "description": "Slug or name of category/tags (e.g. ['pizza', 'weekend'])"
    },
    "oven_stages": {
      "type": "array",
      "description": "Suggested Ninja Woodfire OO101 oven settings",
      "items": {
        "type": "object",
        "required": ["mode"],
        "properties": {
          "mode": {
            "type": "string",
            "enum": [
              "pizza", "max_roast", "specialty_roast", "top_heat", 
              "bake", "smoker", "dehydrate", "keep_warm"
            ]
          },
          "pizza_preset": {
            "type": "string",
            "enum": ["artisan", "thin", "pan", "new_york", "frozen", "custom"]
          },
          "temperature_c": { "type": "integer", "minimum": 40, "maximum": 370 },
          "top_heat_level": { "type": "string", "enum": ["hi", "lo"] },
          "duration_seconds": { "type": "integer", "minimum": 0 },
          "woodfire": { "type": "boolean", "default": false },
          "pellet_blend": { "type": "string", "enum": ["all_purpose", "robust", "other"] },
          "pellet_scoops": { "type": "integer", "minimum": 1, "maximum": 3 },
          "accessory": {
            "type": "string",
            "enum": ["pizza_stone", "pro_heat_pan", "roast_rack", "accessory_frame"]
          },
          "frame_level": { "type": "string", "enum": ["low", "high"] },
          "target_internal_temp_c": { "type": "integer" }
        }
      }
    },
    "ingredients": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["item"],
        "properties": {
          "item": { "type": "string" },
          "amount": { "type": "number" },
          "unit": { "type": "string", "enum": ["g", "kg", "ml", "l", "tbsp", "tsp", "unit", "pinch"] },
          "note": { "type": "string" }
        }
      }
    },
    "steps": {
      "type": "array",
      "items": {
        "type": "object",
        "required": ["instruction"],
        "properties": {
          "title": { "type": "string" },
          "instruction": { "type": "string" }
        }
      }
    },
    "image_url": {
      "type": "string",
      "format": "uri",
      "description": "Public or local image URL. Backend will fetch, sanitize, and resize asynchronously or synchronously."
    },
    "image_base64": {
      "type": "string",
      "description": "Optional base64-encoded image payload if assistant fetched raw bytes."
    }
  }
}
```

#### Example Response (`201 Created`)
```json
{
  "id": "7b8e192a-3c48-4384-a140-5bbf2f0739c9",
  "status": "inbox",
  "owner_id": "usr_auth_28491024",
  "title": "Smoked Hickory Beef Short Ribs",
  "review_url": "https://cooking.internal/inbox/7b8e192a-3c48-4384-a140-5bbf2f0739c9",
  "created_at": "2026-10-27T13:30:00Z"
}
```

---

## 4. Error Responses (RFC 7807)

```json
{
  "type": "https://cooking.internal/errors/validation-failed",
  "title": "Validation Error",
  "status": 422,
  "detail": "Invalid oven settings for mode 'smoker': Woodfire flavor must be enabled.",
  "instance": "/api/v1/inbox",
  "invalid_params": [
    {
      "name": "oven_stages[0].woodfire",
      "reason": "Woodfire smoke must be true when mode is 'smoker'"
    }
  ]
}
```

---

## 5. Standard Status Codes

- `200 OK`: Request succeeded.
- `201 Created`: Resource created successfully (e.g. Inbox import).
- `400 Bad Request`: Malformed JSON or syntax errors.
- `401 Unauthorized`: Missing or invalid Bearer token / unauthenticated session.
- `403 Forbidden`: Authenticated user does not have permission (e.g., trying to edit someone else's private recipe).
- `404 Not Found`: Recipe or route does not exist.
- `422 Unprocessable Entity`: Semantic validation failures (invalid temperature for selected mode, invalid units).
- `500 Internal Server Error`: Server failure with logged error correlation ID.
