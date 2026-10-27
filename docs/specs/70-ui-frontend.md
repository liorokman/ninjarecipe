# 70 — UI & Frontend Specification

Owner: Lightweight Reactive UI Developer. Status: authoritative for implementation.
Companion docs: `10-user-stories.md`, `20-architecture.md`, `25-oven-settings.md`.

## 1. Principles & Technology Stack

- **Paradigm:** Server-Side Rendered (SSR) HTML via Go templates + **htmx 2.x** for reactive swaps + **Hyperscript/Vanilla JS (under 5KB)** for DOM-only interactions (e.g. adding dynamic rows in editor, wake lock).
- **No Node.js / NPM toolchain:** Assets are plain, modern CSS and vanilla JS embedded directly in the Go binary using `//go:embed`.
- **Zero Heavy Frameworks:** Ultra-fast load times on low-power devices (Galaxy Tab S6 Lite) and flawless rendering across Galaxy phones and desktop browsers.
- **Bi-Directional Support (LTR / RTL):** Full Hebrew localization using CSS Logical Properties (`margin-inline-start`, `padding-inline`, `inset-inline-end`).

---

## 2. Target Device Compatibility Matrix

| Device | Screen Size / Res | Context & Interaction | Key UI Adaptations |
|---|---|---|---|
| **Galaxy S26+ / S25+** | ~6.7" OLED (1080x2340) | One-handed, outdoors at oven, bright sun, greasy hands | Bottom navigation bar, minimum 48px touch targets, high-contrast dark mode support |
| **Galaxy Note / S23** | ~6.8" OLED | Outdoors / Kitchen counter | S-Pen compatible tap targets, large typography |
| **Galaxy Tab S9** | 11.0" AMOLED | Propped on kitchen counter | 2-column or 3-column responsive layout, multi-pane recipe views |
| **Galaxy Tab S6 Lite** | 10.4" LCD (Lower CPU) | Older kitchen tablet | Zero JS bundle overhead, instantaneous HTML paint, smooth scrolling |
| **Linux & Windows PCs** | 1080p - 4K (Chrome/Firefox) | Planning, keyboard input, multi-tab browsing | Keyboard shortcuts (`/` for search, `Cmd/Ctrl+S` to save), spacious widescreen grid |

---

## 3. Screen Wake Lock & Cooking Mode ("At the Oven" View)

When cooking outdoors at the Ninja Woodfire oven, cooks cannot touch their phone with greasy hands every 30 seconds to prevent the screen from sleeping.

### Screen Wake Lock Implementation (`web/static/js/wakelock.js`)
```javascript
// Native Screen Wake Lock API
let wakeLock = null;

async function requestWakeLock() {
  if ('wakeLock' in navigator) {
    try {
      wakeLock = await navigator.wakeLock.request('screen');
      document.body.classList.add('wakelock-active');
      wakeLock.addEventListener('release', () => {
        document.body.classList.remove('wakelock-active');
      });
    } catch (err) {
      console.warn(`Wake Lock error: ${err.name}, ${err.message}`);
    }
  }
}

function releaseWakeLock() {
  if (wakeLock !== null) {
    wakeLock.release();
    wakeLock = null;
  }
}

// Auto-request on entering Cooking Mode page, auto-release on leave
document.addEventListener('DOMContentLoaded', () => {
  if (document.body.dataset.page === 'cook-mode') {
    requestWakeLock();
    document.addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'visible') requestWakeLock();
    });
  }
});
```

### Cooking Mode Visual Layout
1. **Oven Setting Hero Card:**
   - Massive 36px font for Dial Mode (e.g. **SMOKER** or **PIZZA: ARTISAN**).
   - High-contrast Temperature badge: **120 °C** (bold yellow on dark background).
   - Prominent Woodfire Pellets indicator: **🔥 WOODFIRE ON (1 Scoop Robust)**.
   - Stage indicators (e.g. "Stage 1 of 2: Smoke" ➔ "Stage 2 of 2: Max Roast").
2. **Step Carousel / Stepper:**
   - Giant "Next Step" / "Previous Step" buttons (min 60px height) at screen bottom.
   - Tap-to-check ingredients checklist.

3. **Active Stage Countdown Timer & 5-Minute Alert:**
   - Prominent countdown display for stages with a defined duration.
   - **5-Minute Warning:** If the app is open, when 5 minutes remain on the current stage (or at 0s for shorter stages), triggers an audio chime (synthesized via Web Audio API, no external audio files required) and triggers the browser's vibration API (`navigator.vibrate([200, 100, 200])`) with an on-screen toast/banner warning.

---

## 4. Hebrew Localization & RTL Design Strategy

Hebrew is an official requirement alongside English.

### Layout Flipping
The HTML root tag sets the direction dynamically:
```html
<!DOCTYPE html>
<html lang="{{ .Lang }}" dir="{{ if eq .Lang "he" }}rtl{{ else }}ltr{{ end }}">
```

### CSS Logical Properties
All spacing, margins, borders, and flex layouts avoid `left` and `right`:
```css
/* Good: Works seamlessly in both LTR and RTL */
.recipe-card {
  padding-inline: 1.25rem;
  padding-block: 1rem;
  margin-inline-end: 1rem;
  text-align: start;
}

.badge-icon {
  margin-inline-end: 0.5rem;
}

/* Modal close button: floats to inline-end */
.modal-close {
  inset-inline-end: 1rem;
}
```

### Bi-Directional Content Handling (Recipe Text)
Even in Hebrew UI mode, some recipes imported from English websites remain in English.
Each recipe card or instruction block includes `dir="auto"`:
```html
<h1 class="recipe-title" dir="auto">{{ .Recipe.Title }}</h1>
<div class="recipe-instructions" dir="auto">{{ .Step.Instruction }}</div>
```
The browser automatically detects character scripts: Hebrew text aligns right, Latin text aligns left, while the surrounding app chrome respects the selected UI language.

---

## 5. Reactive UI Patterns with htmx

### Instant Search & Filter Bar
```html
<div class="search-bar">
  <input type="search" 
         name="q" 
         placeholder="Search recipes, ingredients..."
         hx-get="/recipes" 
         hx-trigger="keyup changed delay:250ms, search" 
         hx-target="#recipe-grid" 
         hx-select="#recipe-grid"
         hx-indicator="#search-spinner"
         autocomplete="off">
  
  <!-- Category Filter Pills -->
  <div class="category-pills">
    {{ range .Categories }}
      <button hx-get="/recipes?category={{ .Slug }}"
              hx-target="#recipe-grid"
              hx-select="#recipe-grid"
              class="pill-btn">
        {{ .Icon }} {{ .Name }}
      </button>
    {{ end }}
  </div>
</div>

<div id="recipe-grid" class="grid-container">
  <!-- Recipe cards rendered here -->
</div>
```

### Interactive Inbox "Review" Actions
Inside the Inbox (`/inbox`):
```html
<div id="inbox-card-{{ .ID }}" class="inbox-card">
  <h3>{{ .Title }}</h3>
  <p>{{ .Description }}</p>
  
  <div class="actions">
    <!-- Accept: Moves to Active Recipes and removes card from Inbox -->
    <button hx-post="/inbox/{{ .ID }}/accept"
            hx-target="#inbox-card-{{ .ID }}"
            hx-swap="outerHTML swap:0.3s"
            class="btn-accept">
      Keep Recipe
    </button>
    
    <!-- Reject: Deletes permanently with confirm -->
    <button hx-post="/inbox/{{ .ID }}/reject"
            hx-confirm="Are you sure you want to permanently discard this recipe?"
            hx-target="#inbox-card-{{ .ID }}"
            hx-swap="outerHTML swap:0.3s"
            class="btn-reject">
      Discard
    </button>
  </div>
</div>
```

---

## 6. Dynamic Form Rows for Recipe Editor (Vanilla JS)

In the recipe editor, cooks need to dynamically add or delete:
- Ingredients (item, amount, unit)
- Instructions / steps
- Oven stages (multi-stage cooking, e.g. smoke then crisp)

We use HTML5 `<template>` cloning without any heavy JS dependencies:

```html
<template id="ingredient-row-template">
  <div class="row ingredient-row">
    <input type="text" name="ingredient_item[]" placeholder="Ingredient" required>
    <input type="number" step="any" name="ingredient_amount[]" placeholder="Amount">
    <select name="ingredient_unit[]">
      <option value="g">g</option>
      <option value="kg">kg</option>
      <option value="ml">ml</option>
      <option value="tbsp">tbsp</option>
      <option value="tsp">tsp</option>
      <option value="unit">unit</option>
    </select>
    <button type="button" class="btn-remove" onclick="this.closest('.row').remove()">×</button>
  </div>
</template>
```
Client code attaches an event to the "Add Ingredient" button to clone `#ingredient-row-template` and append it into the container.
Draft forms save their state to `localStorage` periodically to prevent loss of half-typed recipes if a phone browser tab reloads.
