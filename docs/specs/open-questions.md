# Open Questions & Decisions Log

All architectural and user questions have been resolved and agreed upon by the team with the project owner.

| # | Topic | Owner Decision | Technical Impact & Consensus |
|---|---|---|---|
| **1.1** | **Shared Recipe Permissions** | **Full control ceded to all users.** Any authenticated household user can edit, update stages/notes, and delete shared recipes. | Handlers allow any authenticated user to edit or delete recipes where `visibility = 'shared'`. For `visibility = 'private'`, only the owner can edit or delete. |
| **1.2** | **Session Duration** | **Accepted: 30 days sliding.** | `sessions` table tracks `expires_at`. Session middleware extends expiration by 30 days on activity if less than 15 days remain. |
| **1.3** | **Cook Log, Notes & Ratings** | **Accepted: Personal favourites; Shared cook logs/notes.** | Favourites are user-specific. For shared recipes, all cook logs (`recipe_cook_logs`), post-cook notes, and ratings are displayed to all household users. |
| **1.4** | **Inbox Scope** | **Accepted: Per-user private inbox.** | AI assistant API tokens route imports to the specific token owner's inbox. Upon accepting ("Keep"), user can mark as shared with family. |
| **2.5** | **Translation Scope** | **Accepted: Interface translation only.** | App chrome is translated to English and Hebrew. Recipe content displays in original language with native bi-directional rendering (`dir="auto"`). |
| **2.6** | **Category Management** | **Accepted: Shared starter set + custom tags.** | Starter categories seeded via migration. Users can select categories and freely add custom tags on any recipe. |
| **3.7** | **In-App Cooking Timer** | **Included: Active countdown with a 5-minute pre-alert.** | Cook Mode UI includes an active countdown timer for stages with a duration. When the app is open, triggers an audio chime / vibration and visual banner **5 minutes before completion** (or when done for short stages < 5m). Uses browser Web Audio / Notification API. |
| **3.8** | **Deletion Confirmation** | **Accepted: Immediate hard delete with strong confirmation.** | Two-step modal warning: "This will permanently delete this recipe and its images from the server for all family members." No trash bin. Cascades to DB and disk. |
| **3.9** | **Image Formats & HEIC Handling** | **Server converts all formats including HEIC.** | Server accepts JPEG, PNG, WebP, and HEIC/HEIF (e.g. Samsung camera format). Uses Go HEIC decoding (`github.com/jdeng/goheif`) in the backend image pipeline to convert to normalized WebP/JPEG, ensuring instant display on any browser without client errors. |
