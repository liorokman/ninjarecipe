# 10 — User Stories (End-User View)

Written from the point of view of the people who actually cook. Plain language on purpose.
Technical words (OIDC, token, label, RTL) only appear in notes for the implementer, in *italics*.

---

## 1. Who we are

### P1 — Dana, the everyday cook (main persona, not computer savvy)
- Cooks on the Ninja Woodfire oven in the garden, 2–3 times a week.
- Holds a **Galaxy S25+** in one hand, tongs or a pizza peel in the other. Fingers are often greasy, floury or wet.
- Outside in **bright sun** (screen hard to read) or in the **evening dark** (screen too bright, eyes adjusted to dark).
- Sometimes props the **Galaxy Tab S6 Lite** against the kitchen wall while prepping.
- Reads English and Hebrew. Does not know or care what "logging in with Authentik" means. Just wants to tap the icon and see recipes.
- Typical question: *"What was that chicken thing we did last month, and what temperature was it?"*

### P2 — Lior, the owner (tech-savvy)
- Set up the server, the login system and the AI assistant.
- Adds recipes from the PC (Chrome/Firefox on Linux/Windows), tidies categories, shares good ones with the family.
- Reviews what the AI assistant found and keeps the good ones.

### P3 — Savta / a guest family member (Hebrew first, occasional user)
- Visits, wants to cook "the pizza Lior makes". Prefers **Hebrew**.
- Has her own account but uses it rarely. Forgets passwords. Will give up if anything looks complicated.
- Only needs: find a shared recipe, read the oven settings, follow the steps.

---

## 2. Where and how it is used

| Situation | Device | What matters most |
|---|---|---|
| At the oven, outside, daytime | S25+ in one hand | Huge text, high contrast, big buttons, screen stays on |
| At the oven, evening | S25+ | Dark mode that isn't blinding, still readable |
| Prepping in the kitchen | Tab S6 Lite propped up | Ingredients + steps side by side, readable from 1 m away |
| Planning / adding recipes | PC or phone on the sofa | Fast typing, paste text, add photos |
| Guest visit | Any phone | Hebrew, no confusion, no "where is it?" |

---

## 3. User stories

Format: **As a …, I want …, so that …** followed by acceptance criteria (AC). "Must" / "Nice" refers to section 5.

### A. Logging in and staying logged in

**A1 (Must)** As Dana, I want to log in once and **stay logged in for weeks**, so that I'm never stuck at the oven with a login screen.
- AC1: After logging in on a device, I am not asked to log in again for at least **30 days** of normal use (each visit should extend it).
- AC2: Closing the browser, restarting the phone, or the server restarting does **not** log me out.
- AC3: If I really must log in again, the screen shows one big button: **"Sign in"** / **"התחברות"**, not technical text.
- AC4: After signing in I land back on the page I was trying to open (e.g. the recipe link Lior sent me), not the home page.

**A2 (Must)** As Dana, I want to add the app to my **home screen** like a normal app icon, so that I open it with one tap.
- AC1: The site can be "added to home screen" on Android Chrome and opens full-screen with its own icon and name.

**A3 (Must)** As Savta, I want a clear message if I'm not allowed in, so that I know whom to ask.
- AC: If sign-in fails or my account isn't set up, I see: "You can't get in yet. Ask Lior to add you." — not an error code.

**A4 (Nice)** As Dana, I want a **"Sign out"** option tucked away in a menu, so that I can sign out on a shared tablet, but I don't hit it by accident.

### B. Finding a recipe quickly

**B1 (Must)** As Dana, I want the home screen to show **big category tiles with pictures** (Pizza, Meat, Chicken, Fish, Veg, Bread, Dessert, Smoked…), so that I can find things without typing.
- AC1: Tiles are at least thumb-sized (roughly 2 per row on a phone), with a picture and a word.
- AC2: Tapping a tile shows the recipes in it as cards with a photo, name, and the **oven mode + temperature** on the card.
- AC3: A recipe can be in more than one category (e.g. "Chicken" and "Sides"). *Implementer: "Smoked" is a tile derived from the oven settings, not a category (`30 §5`).*

**B2 (Must)** As Dana, I want a **search box** at the top where I type any word ("chicken", "עוף", "lemon"), so that I can find a recipe even if I only remember one ingredient.
- AC1: Search matches recipe names, ingredients, categories and notes.
- AC2: Results update as I type (no need to press a button), and work with partial words ("chick" finds "chicken").
- AC3: Works for Hebrew words too.
- AC4: If nothing is found: "No recipes found for 'xyz'." plus a button to clear the search.

**B3 (Must)** As Dana, I want a **"Recently cooked"** / **"Recently viewed"** row on the home screen, so that "that chicken thing from last month" is right there.
- AC: Shows the last ~10 recipes I opened in "At the oven" mode or marked as cooked, newest first, with the date ("3 weeks ago").

**B4 (Nice)** As Dana, I want to mark recipes as **favourites (♥)**, so that our go-to dishes are one tap away.
- AC: A heart on the recipe card/page; a "Favourites" tile/row on the home screen. Favourites are personal (mine don't change Lior's).

**B5 (Nice)** As Dana, I want to filter by **oven mode** (e.g. "Pizza", "Smoker", "Roast"), so that when the oven is already set up for pizza I see only pizza-mode dishes.

**B6 (Must)** As Dana, I want to see clearly **which recipes are mine and which are the family's**, without confusing words.
- AC: A small, friendly badge: **"Just me" 🔒** vs **"Family" 👨‍👩‍👧** (see section 4 for wording). By default I see both together.

### C. "At the oven" view (cooking mode)

**C1 (Must)** As Dana, I want the **oven settings at the very top of every recipe in huge text**, so that I can read them from arm's length in sunlight.
- AC1: Shows, in this order: **Mode** (e.g. Pizza / Gourmet Roast / Max Roast / Smoker / Bake / Broil / Dehydrate / Keep Warm — *implementer: modes per `25 §3`*), **Temperature in °C**, **Time** (e.g. "12 min" or "1 h 30 min"), **Woodfire/smoke: on/off** (and which pellets, if noted), plus any extra like "preheat 25 min" or "turn halfway".
- AC2: Readable without zooming: temperature/time very large (think "wall clock" size), strong contrast.
- AC3: If a recipe has **several stages** (e.g. smoke 2 h at 120 °C, then roast 10 min at 250 °C), each stage is shown as its own clearly numbered block.

**C2 (Must)** As Dana, I want a big **"Start cooking"** button that switches to a step-by-step view, so that I follow one step at a time.
- AC1: One step per screen (or the current step highlighted), very large text.
- AC2: Big **Next** / **Back** buttons at the bottom, easy to hit with a thumb; swiping also works. In Hebrew, the directions flip naturally.
- AC3: The oven settings stay visible (small strip at top) while I go through the steps.
- AC4: The **screen stays awake** while in this view. It goes back to normal when I leave.
- AC5: A large **"✕ Done"** button to leave; it asks "Did you cook it?" (see F1) — optional to answer.

**C3 (Nice)** As Dana, I want a **timer** I can start from a step ("Cook 6 min" → tap → countdown), so that I don't need a separate timer app.
- AC1: Tap the time on a step to start a countdown; it keeps going if I go to another step.
- AC2: When it ends: loud sound and vibration, and the screen flashes. Works with the screen on.
- AC3: More than one timer at a time is fine (e.g. pizza 1 and pizza 2).

**C4 (Must)** As Dana, I want the ingredients easy to see while cooking, so that I don't scroll around with greasy fingers.
- AC: In cooking mode, an "Ingredients" button opens the list over the step; on the tablet (landscape) ingredients and steps are side by side.
- Nice: tap an ingredient to tick it off.

**C5 (Must)** As Dana, I want a **dark / bright** look that suits the light, so that I can read it in sun and at night.
- AC: Follows the phone's dark-mode setting automatically; a simple toggle (☀️/🌙) on the cooking screen to override.

**C6 (Nice)** As Dana, I want to change the **number of portions/pizzas**, and ingredient amounts adjust, so that I can cook for 2 or for 8.

### D. Adding a recipe by hand (quickly)

**D1 (Must)** As Dana, I want a big **"+ Add recipe"** button, so that adding is obvious.

**D2 (Must)** As Dana, I want the add form to ask only for the essentials first, so that I can save in under a minute.
- AC1: Required: only **Name**. Everything else is optional and can be filled in later.
- AC2: The oven settings section uses **simple pickers**: Mode (list of the real oven modes), Temperature (°C, with sensible steps), Time (hours/minutes), Woodfire on/off — not free text I might mistype.
- AC3: Ingredients and steps can be typed **one per line** or **pasted as a block** of text (e.g. from WhatsApp) and are split into lines automatically.
- AC4: Categories are chosen by tapping tiles/chips; I can type a new one if missing.
- AC5: Can add more than one oven stage (C1-AC3) with a simple "+ Add another stage".

**D3 (Must)** As Dana, I want to **take a photo with the phone camera** (or pick one from the gallery), so that we remember what it should look like.
- AC1: "Add photo" offers **Camera** and **Gallery** (normal Android chooser).
- AC2: Big phone photos are accepted and shrunk automatically; I don't have to think about file size.
- AC3: Several photos allowed; I can pick which one is the main (card) photo.
- AC4: Upload shows progress; if it fails, the recipe text is still saved and I'm told "Photo didn't upload — try again".

**D4 (Must)** As Dana, I want my half-typed recipe **never to be lost**, so that a phone call, a screen lock, or a bad signal in the garden doesn't wipe it.
- AC1: The form keeps a draft on the device as I type; if I come back (even after closing the app), it asks "Continue your unsaved recipe?".
- AC2: If saving fails (no signal), I see "Not saved yet — no connection. We'll keep it here." and my text stays.
- AC3: Leaving the page with unsaved changes asks "Leave without saving?".

**D5 (Must)** As Dana, I want new recipes to be **"Just me"** by default, with an easy switch to share with the family, so that I don't accidentally share an unfinished recipe.

### E. Editing

**E1 (Must)** As Dana, I want an **Edit** button on my own recipes, so that I can fix the temperature after we learn it was too hot.
- AC1: Same form as adding, pre-filled. Same draft protection (D4).
- AC2: I can edit my own recipes and every **Family** recipe (owner decision: shared recipes are fully editable by everyone). Family recipes from others additionally offer **"Copy to my recipes"** (Nice) for making a personal variant.

**E2 (Nice)** As Dana, I want to see "Last changed: 12 May by Lior", so that I know if a recipe was updated.

### F. Sharing with the family

**F1 (Must)** As Dana, I want one clear switch **"Share with family"**, so that everyone in the house can see the recipe.
- AC1: Switch on the recipe page (and in the form when creating a recipe). Wording: "Share with family" (on) / "Just me" (off). Only the recipe's owner sees the switch; others see the badge.
- AC2: Turning it on shows a short note: "Everyone in the family will see this recipe."
- AC3: Turning it off on a shared recipe: "The family won't see this recipe any more."

**F2 (Nice)** As Lior, I want to **send a link** to a recipe over WhatsApp, so that Savta can open it directly.
- AC: A "Share link" button uses the phone's normal share sheet. The person still needs to be a family member with an account; after signing in they land on the recipe (A1-AC4).

*Decision from owner: If a recipe is shared, full control is ceded to all users — any family member can edit or delete a shared recipe. Only the owner can switch it back to "Just me". Full rules: `20 §7`.*

### G. Deleting (permanent)

**G1 (Must)** As Dana, I want deleting to be **hard to do by accident**, because it's gone forever.
- AC1: Delete is not on the main screen — it's at the bottom of the Edit page or in a "⋯" menu, in red.
- AC2: Confirmation dialog in plain words: **"Delete 'Lemon Chicken' forever? This can't be undone. Photos will be deleted too."** — buttons **"Keep it"** (prominent) and **"Delete forever"** (red).
- AC3: If the recipe is shared, the dialog adds: **"The whole family will lose this recipe too."**
- AC4: The owner can delete a private recipe; any authenticated household user can delete a shared recipe.
- AC5: After deleting, a short message "Recipe deleted" and I'm back at the list.

### H. Reviewing recipes the AI assistant found (Inbox)

**H1 (Must)** As Lior/Dana, I want an **"Inbox" with a number badge** (e.g. "Inbox 3"), so that I notice new recipes the assistant found.
- AC: Wording: **"New recipes to review"** / **"מתכונים חדשים לבדיקה"**. Badge only shows when there's something.

**H2 (Must)** As Lior/Dana, I want to see each suggested recipe like a normal recipe (photo, oven settings, ingredients, steps, where it came from), so that I can judge it.
- AC1: Shows the **source website** as a link ("Found on: example.com").
- AC2: Missing oven settings are clearly shown as "Oven settings missing — add them before cooking".

**H3 (Must)** As Lior/Dana, I want two big buttons **"✓ Keep"** and **"✕ Throw away"**, so that reviewing is quick.
- AC1: "Keep" moves it into **my recipes** as "Just me"; I can then share it.
- AC2: "Throw away" asks a light confirmation ("Throw away this suggestion?") and removes it.
- AC3: Optional "Edit, then keep" to fix things before saving.
- AC4: After each choice, the next suggestion appears automatically.

**H4 (Nice)** As Lior, I want the assistant's suggestions to arrive with categories already guessed, so that I just confirm them.

### I. Switching to Hebrew

**I1 (Must)** As Savta, I want to switch the app to **Hebrew** with one obvious button, so that I can read everything.
- AC1: A language button showing **"עברית"** when in English and **"English"** when in Hebrew (each written in its own language), reachable from the menu and from the sign-in screen.
- AC2: The choice is remembered on that device/account.
- AC3: The whole layout flips right-to-left (menus, back/next buttons, arrows). Numbers like **220 °C** and **12 min** still read correctly.
- AC4: The very first time, the app uses the phone's language.

**I2 (Must)** As Dana, I want recipes written in Hebrew to display correctly even when the app is in English (and vice versa), so that mixed family recipes look right.
- AC: Each recipe's text direction follows its own language; a Hebrew recipe shows right-to-left in either app language.

*Note: the recipe content itself is not translated — only buttons/menus. (See open-questions.)*

### J. After cooking (optional ideas)

**J1 (Nice)** As Dana, I want to tap **"Cooked it today"**, so that we have a log of when we made it and "Recently cooked" works.
- AC: Records the date; recipe page shows "Cooked 4 times — last on 3 Oct".

**J2 (Nice)** As Dana, I want to add a **quick note after cooking** ("next time less salt", "pellets: cherry was great", "turn after 45 s"), so that we improve every time.
- AC: Notes are dated, shown on the recipe page under "Our notes", newest first. Short text, optional photo.

**J3 (Nice)** As Dana, I want to give a **1–5 star rating** (or simple 👍/👎), so that the best dishes rise to the top.

**J4 (Nice)** As Lior, I want to sort/filter by "most cooked" or "best rated".

---

## 4. Frustrations to avoid (and friendly wording)

| Frustration | What we want instead |
|---|---|
| Tiny buttons / links | Every tap target at least fingertip-sized (≈ 48 px+), well spaced; main actions at the bottom within thumb reach |
| Small grey text in sunlight | Large, high-contrast text; no light-grey-on-white |
| Being logged out all the time | Stay signed in for weeks (A1) |
| Losing a half-typed recipe | Auto-saved drafts, warning before leaving (D4) |
| Jargon | No "OIDC", "token", "label", "entity", "sync", "404". Use plain words |
| Confusing "private / public / shared" | Use **"Just me 🔒"** and **"Family 👨‍👩‍👧"**; switch text: **"Share with family"** |
| "Labels / tags" | Call them **"Categories"** (קטגוריות) |
| "Inbox" (unclear) | **"New recipes to review"**, with the assistant named, e.g. "Found by your assistant" |
| "Accept / Reject" | **"Keep" / "Throw away"** |
| "Delete" with no explanation | **"Delete forever"** + what it affects (photos, family) |
| Screen going dark mid-recipe | Screen stays awake in cooking mode (C2-AC4) |
| Fahrenheit, cups, ounces | Metric only: °C, g, ml, cm |
| Error codes | Friendly messages that say what to do next ("No connection — your recipe is kept here") |
| Pop-ups and clutter | Recipe page opens straight to the oven settings, no banners |
| Pinch-zooming | Never needed; layout fits phone and tablet, portrait and landscape |
| Slow loading in the garden (weak Wi-Fi) | Fast pages, small images; recently opened recipes still readable if the signal drops (Nice) |

Sample friendly wording (EN / HE):

| Situation | English | עברית |
|---|---|---|
| Sign-in button | Sign in | התחברות |
| Private badge | Just me | רק אני |
| Shared badge | Family | משפחה |
| Share switch | Share with family | שיתוף עם המשפחה |
| Inbox | New recipes to review | מתכונים חדשים לבדיקה |
| Keep / Throw away | Keep / Throw away | לשמור / לזרוק |
| Delete confirm | Delete forever? This can't be undone. | למחוק לתמיד? אי אפשר לבטל. |
| Start cooking | Start cooking | מתחילים לבשל |
| Cooked it | Cooked it today | בישלתי היום |

*(Hebrew strings are suggestions; a native speaker should review.)*

---

## 5. Must have vs nice to have

### Must have
1. Stay signed in for weeks; one-tap "Sign in" when needed; return to the page I wanted.
2. Home-screen icon (installable).
3. Category tiles with pictures; search by any word (EN + HE); recently viewed/cooked.
4. Recipe page with **huge oven settings first** (mode, °C, time, woodfire, preheat, stages).
5. Step-by-step cooking view with big Next/Back and **screen kept awake**.
6. Readable in sun and at night (high contrast + automatic dark mode).
7. Quick add with only Name required, oven-setting pickers, paste-a-block ingredients/steps.
8. Photos from camera or gallery, auto-shrunk.
9. Never lose a half-typed recipe.
10. Edit own recipes and family recipes.
11. "Share with family" switch, "Just me" by default.
12. Delete with clear "forever" warning; private recipes only by their owner, family recipes by anyone in the family.
13. Review assistant suggestions with Keep / Throw away.
14. Full Hebrew with right-to-left layout; per-recipe text direction.
15. Big buttons everywhere; no jargon; friendly error messages.

### Nice to have
1. Favourites ♥.
2. Built-in timers from steps (with sound + vibration), multiple at once.
3. "Cooked it" log with dates; post-cook notes; ratings.
4. Portion scaling.
5. Filter by oven mode; sort by most cooked / best rated.
6. Share link via WhatsApp.
7. "Copy to my recipes" for family recipes.
8. Tick off ingredients while cooking.
9. Recently opened recipes readable offline.
10. Assistant suggestions arrive with categories guessed.
11. Sign out from menu.

---

## 6. A day in the life (sanity check scenario)

1. Saturday, 18:00, sunny. Dana opens the app from the home screen — already signed in.
2. Types "chick" — sees "Lemon Chicken (Family)" with photo and "Max Roast · 250 °C". Or taps "Recently cooked" and it's there.
3. Taps it. Top of screen: **SMOKER · 120 °C · 2 h · Woodfire ON (hickory)**, then **Stage 2: MAX ROAST · 250 °C · 10 min**.
4. Taps **Start cooking**. Screen stays on. Taps the "10 min" on step 5 → timer runs → buzzes.
5. Taps **Done** → "Cooked it today?" → Yes → adds note "a bit dry, 1 h 45 next time".
6. Evening: Lior's assistant found 3 recipes. "New recipes to review (3)". Keep, Throw away, Keep. Shares one with the family.
7. Savta, visiting next week, switches to **עברית**, taps the "Pizza" tile and follows the steps on the tablet.
