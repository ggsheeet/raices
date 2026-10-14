# Plan: Ticket Purchase Flow (SQLite + Mercado Pago + Resend + QR check-in)

Plan name: `ticket-purchase-flow`

This document is self-sufficient. A fresh overseer who reads ONLY this doc can run the effort. It already includes the 2026-10-13 red-team locks from `RED_TEAM.md` (3 block, 12 fix-in-plan, 7 note). Repo line refs are a snapshot; re-verify them with an explore subagent before a builder edits.

---

## 1. Objective

Turn `raices` from a UI-only registration stub into a working ticket sale:

1. A buyer fills the register form. Card data stays inside Mercado Pago Secure Fields or Card Form iframes. Our server receives a card token plus the payer fields, reserves seats, and charges through the Orders API. The synchronous create response and the webhook share one idempotent transition. On success the buyer gets one confirmation email with one inline QR image per ticket.
2. An admin (one env-configured credential) logs in. Opening a ticket QR link on their phone marks that ticket used. A second scan shows the original time and does not change the row.
3. Held seats never exceed 300. A scanned ticket still counts. Released seats are deleted and stop counting.
4. New checkouts stop at 2026-10-31T06:00:00Z (end of 30 Oct 2026 in Mexico City, which is UTC−6 year-round). The event runs 30 Oct – 1 Nov 2026. Scanning keeps working after sales close.
5. Orders that need a person show on one admin list: paid but unfulfilled, paid with a failed email, and paid-fulfilled orders still `not_sent` after about 10 minutes. Email retry is an admin action. There is no background resend loop.

## 2. Success metrics

1. A sandbox purchase produces one `orders` row `paid` + `fulfilled`, N `tickets` rows `issued` with unique plaintext tokens, and one Resend email whose HTML references those QR PNGs by `Content-ID`. Held count increases by exactly N.
2. An admin claim of an issued ticket sets `used` once. A second claim of the same link renders the original `used_at` and does not write.
3. A checkout that would push held tickets (`reserved` + `issued` + `used`) past 300 is rejected before Mercado Pago is called, and the page shows how many seats remain. Issuing tickets, then scanning one, does not increase seats available.
4. A reservation that never reaches a terminal charge result releases its seats within 15 minutes, verified by test with a moved clock. A synchronous approval with no webhook ever arriving does not expire.
5. `go build ./...`, `go vet ./...`, and `templ generate` (no drift) pass.
6. Persistence, split in two:
   - Compose: with `SQLITE_PATH` on a named volume, a row written in the container is still there after a restart.
   - Cloud Run shape: the entrypoint runs `litestream restore` then `litestream replicate -exec` when `LITESTREAM_REPLICA_URL` is set, and execs the binary directly when it is empty. Exercising a real GCS bucket is out of scope.
7. The admin list shows exactly three kinds of paid orders: `unfulfilled`, `email_status=failed`, and `fulfilled` + `not_sent` older than about 10 minutes. A successful admin resend drops the email row off the list.

## 3. Scope

### In scope

- SQLite mirrored from zh-nb: `modernc.org/sqlite`, sqlc, embedded numbered migrations applied on boot, `SetMaxOpenConns(1)`, pragmas set in the DSN (`busy_timeout`, `foreign_keys`, `journal_mode=WAL`, `synchronous=NORMAL`) so every connection gets them. `SQLITE_PATH` required. `.gitignore` for `*.db*` and generated sqlc output. Temp-file tests, not `:memory:`.
- Graceful shutdown: `SIGTERM`/`SIGINT` call `http.Server.Shutdown` with a bounded drain, from `main`.
- Schema:
  - `orders`: `id`, `given_name`, `family_name`, `email`, `age`, `church` (nullable), `quantity`, `unit_price_cents`, `total_cents`, `status` (`pending_payment` | `paid` | `rejected` | `expired`), `fulfillment_status` (`pending` | `fulfilled` | `unfulfilled`), `email_status` (`not_sent` | `sent` | `failed`), `email_last_error` (nullable), `mp_order_id` (nullable), `reserved_until`, timestamps.
  - `tickets`: `id`, `order_id` FK, `token` nullable unique, `status` (`reserved` | `issued` | `used`), `used_at` nullable, timestamps. Reserved rows have `token` NULL. The token is generated when the row moves to `issued` and stored in plaintext so a later resend can rebuild the same QR. Lookup is an indexed equality match on `token`.
  - `sessions`: `token_hash` primary key, `expires_at`. Admin session cookies stay hash-only. Ticket tokens do not.
  - Held capacity is `COUNT(*)` of ticket rows. Release deletes the order's `reserved` rows. There is no `released` status.
- Checkout `POST /register/pay`:
  - Payer fields only: given name, family name, email (must match email confirm), age, church, quantity, and the Mercado Pago card token. Reuse `validateTicketQty` (1–10).
  - Card number, expiry, and CSC never appear in our DOM, form body, or request. Use Mercado Pago Secure Fields or Card Form (iframes). Remove the `name` attributes on the current plain card inputs.
  - Inside one transaction, before any network call: expire stale reservations (delete their `reserved` tickets, set the order `expired`), reject the purchase when `now >= 2026-10-31T06:00:00Z`, reject when `300 - held < qty`, insert the order `pending_payment` with `reserved_until = now+15m` and `fulfillment_status=pending`, insert `qty` ticket rows `reserved` with `token` NULL, commit.
  - After commit, call the Orders API. Set `external_reference` to the local order id. Set the idempotency key from that same id (`X-Idempotency-Key` via the SDK's `WithIdempotencyKey`). Persist `mp_order_id` when the response includes it.
  - Feed the synchronous create response into the same transition function the webhook uses. Approved/processed issues tickets in that request. A terminal failure releases seats immediately and the page shows an error. Still-pending (`action_required` and any non-terminal status) leaves the reservation up and the page tells the buyer the charge is still confirming. The webhook is confirmation and recovery, not the only issuer.
- Webhook `POST /webhooks/mercadopago`:
  - Verify the signature with the webhook secret. Do not trust the body for status or amount.
  - Re-fetch the order from Mercado Pago by id. Resolve the local row by `external_reference` first, then by `mp_order_id`.
  - Map real Orders API `status` / `status_detail` values onto three outcomes before writing the handler: success, terminal failure, still pending. The Payments API words `approved` / `rejected` are not the contract. Confirm the mapping against current Orders API docs or the v1.14 SDK types and write it into the tier kickoff.
  - Idempotent. A terminal local status never transitions again.
- Transition function, one transaction, no network inside it:
  - Success, and this order's tickets are still `reserved`: set those rows to `issued`, fill each `token` with a fresh 32-byte URL-safe secret, set the order `paid` + `fulfilled`. No second capacity check. This order's seats are already in the count.
  - Success, and the order is `expired` or `rejected` (its reserved rows are gone): insert `qty` new `issued` tickets with tokens only when `300 - held >= qty`, in the same transaction, and set `paid` + `fulfilled`. Otherwise set `paid` + `unfulfilled` and send no email. Refund stays a manual action in the Mercado Pago dashboard.
  - Terminal failure: delete remaining `reserved` rows, set the order `rejected`.
  - Commit the paid/fulfilled transition before any email attempt. Sending runs after the commit, outside every transaction.
- Email: one message per paid fulfilled order. Spanish. Order summary (name, 250 MXN × qty, total, event 30 Oct–1 Nov 2026) and one QR PNG per ticket, embedded with `Content-ID` so the image renders in the message. Copy says the QR is for door staff to scan. `RESEND_FROM` is required. Pin `github.com/resend/resend-go/v2` at v2.28.0 or newer so `Attachment.ContentId` exists. Fidelio's v2.19.0 cannot do CID images and is not the version to copy. Each trigger (the transition's send, and each admin click) makes up to three immediate attempts with the short backoff, then stops. No startup sweep and no interval loop. A failed send sets `email_status=failed` and stores `email_last_error`. Admin resend rebuilds QR images from the stored plaintext tokens.
- QR payload: `{APP_BASE_URL}/tickets/claim/{token}`. Pure-Go QR library, no CGO, compatible with `CGO_ENABLED=0`.
- Admin login: `ADMIN_EMAIL` and `ADMIN_PASSWORD` required and non-empty, compared in constant time. Match creates a `sessions` row (random token, store only the hash, expiry long enough to cover the event weekend — 7 days). HttpOnly, Secure, SameSite=Lax cookie. Empty credentials never authenticate.
- `GET /tickets/claim/{token}`: valid admin session required. Missing session redirects to login with `next` set to that path. `next` accepts only a same-origin relative path (leading `/`, not `//`, no scheme). Unknown token renders not-found. `issued` flips to `used` and shows buyer name, ticket index, and time. Already `used` shows the stored time and does not write. The request logger redacts the token segment on `/tickets/claim/` so Cloud Logging never stores it.
- `GET /admin` (name flexible): the landing page after login when there is no `next`. Lists paid orders that are `unfulfilled`, or `email_status=failed`, or `fulfilled` + `not_sent` with `updated_at` older than about 10 minutes. Columns: buyer name, email, quantity, amount, Mercado Pago order id, and for email rows the last error. Email rows have a resend button. Unfulfilled rows have no issue button.
- Register copy: event dates 30 Oct – 1 Nov 2026 on both the header and the banner. No clock times. Remove the 20 Oct sales-end line. The page states that new purchases stop at the end of 30 Oct. Displayed unit price and line total come from the server. Remove every hardcoded money figure in `register.templ` and `register.js`, including 900, 250, and 972. There is no fee; 972 is a stale placeholder and is deleted. The Alpine component reads the unit price from a data attribute. The server computes `qty * unit_price_cents` for the charge.
- CSP, widened only for the Mercado Pago origins the chosen Secure Fields / Card Form docs require, across `script-src`, `frame-src`, `connect-src`, and `img-src`. The `new MercadoPago(...)` constructor moves into a `'self'` script (or a nonce). Acceptance: a sandbox tokenization produces no CSP violations in DevTools, and the `/register/pay` body contains a token and no card fields.
- Deploy hooks copied from zh-nb's shape: `scripts/entrypoint.sh` restore-if-replica-exists then replicate `-exec` when `LITESTREAM_REPLICA_URL` is set; otherwise exec the binary. Prod image `ENV SQLITE_PATH=/data/raices.db`, `mkdir /data`. README states the Cloud Run deploy constraint as non-negotiable: `--max-instances 1`, `--execution-environment gen2`, `--no-cpu-throttling`. Do not FUSE-mount a GCS bucket as the live database. Creating the bucket is a deploy-time human step, not part of the build.
- Boot fails closed. A required-var helper rejects empty values for: `SQLITE_PATH`, `APP_BASE_URL`, `TICKET_PRICE`, `MP_ACCESS_TOKEN`, `MP_PUBLIC_KEY`, `MP_WEBHOOK_SECRET`, `RESEND_API_KEY`, `RESEND_FROM`, `ADMIN_EMAIL`, `ADMIN_PASSWORD`. `TICKET_PRICE` is whole pesos (existing example value `250`); parse once at boot to integer cents (`250` → `25000`) and refuse non-positive or non-integer input. `LITESTREAM_REPLICA_URL` is optional. `PORT` and `ENVIRONMENT` keep their current defaults.

### Out of scope

- More than one admin, a users table, or roles.
- A button that issues tickets for an unfulfilled order. Refund is manual in the Mercado Pago dashboard.
- A background job that resends email.
- A general order browser, search, or export.
- More than one event or ticket type. Currency is MXN.
- In-app refunds.
- A public "my tickets" page. The email is the ticket.
- Running Litestream against a real bucket in CI.
- Installing a tunnel so Mercado Pago can reach a laptop. The synchronous result completes a normal sandbox purchase without a public webhook. A tunnel, if someone wants one later, needs explicit install approval.

## 4. Phased breakdown

### Tier 0 — Database, env, shutdown

- `database.Open`, DSN pragmas, pool of one, ping, migrate-on-boot, sqlc, `InTx`.
- Schema and migrations as in Scope. Held count is all ticket rows.
- Required env vars and `TICKET_PRICE` cents parsing, fail at boot.
- `main` opens the DB, and shuts the HTTP server down on signal.
- Dockerfile, entrypoint, compose volume, gitignore, README deploy constraint (max-instances 1, gen2, no CPU throttling).

Verification: migration test on a temp file; bad `TICKET_PRICE` and empty `ADMIN_PASSWORD` refuse to boot; compose restart keeps a row; entrypoint script branches on whether `LITESTREAM_REPLICA_URL` is set (inspect the script; do not call GCS).

### Tier 1 — Checkout, reserve, charge

- CSP and Secure Fields / Card Form. Delete plain card inputs. Move the Mercado Pago constructor into a self-hosted script.
- Price rendering from the server. Delete 900, 250, and 972 hardcodes.
- `POST /register/pay` as specified: lazy expiry, cutoff instant, capacity, reserve, commit, then the Orders API call with `external_reference` and idempotency key, then the shared transition.
- Cutoff test at `2026-10-31T05:59:59Z` (allowed) and `2026-10-31T06:00:00Z` (refused). The cutoff function is not called from the webhook or from the transition of an order created earlier.
- Tests: happy reserve + synchronous success with no webhook and the order still `paid` after the 15-minute mark; capacity refusal; scan-does-not-free-a-seat can land in Tier 3 if the claim route does not exist yet, but the count query itself must be tested here with a `used` row included; post-cutoff refusal; double submit uses the same idempotency key.

### Tier 2 — Transition, webhook, email

- One transition function used by the synchronous response and the webhook. Document the Orders API status map in the kickoff after checking docs or SDK types.
- Webhook verifies, re-fetches, resolves by `external_reference`.
- Both late-approval cases tested: live reservation issues with no extra capacity check; expired reservation re-inserts only when seats remain, otherwise `paid` + `unfulfilled`.
- Email after commit, CID QR images, three immediate attempts, then `sent` or `failed`.
- Tests: duplicate success notification does not mint a second set of tickets or send a second email; terminal failure deletes `reserved` rows; a crash-shaped `paid` + `fulfilled` + `not_sent` row is not emailed again by the webhook.

### Tier 3 — Admin session, claim, attention list

- Login, session cookie, `next` allowlist.
- Claim route, first scan wins, log redaction test (the log line contains the path prefix and no token).
- Attention list and admin resend of the same tokens.
- Test: issue, scan, assert held count unchanged; second scan unchanged; list contains only the three stuck shapes; resend of a failed row sets `sent`.

## 5. Risks

- Orders API status strings must be read from current docs or SDK types. Do not ship a webhook that switches on `approved`.
- `MaxOpenConns(1)` serializes writes in one process only. A second Cloud Run instance would be a second 300-cap and a second session store. The README constraint is part of the effort's definition of done.
- A pool query or an HTTP call inside `InTx` deadlocks the single connection. Mercado Pago and Resend stay outside the transaction.
- Webhook signature details (header, secret, payload) are not in this repo. The Tier 2 builder takes them from Mercado Pago's current docs.
- QR library must be pure Go. Confirm before adding the dependency.
- During a Cloud Run revision rollout, old and new instances overlap briefly even at max-instances 1. Litestream restore-if-missing does not see writes the old instance accepts after the new instance has restored, and the default replica interval leaves a short crash window. Before the first production sale the overseer records one of these, in the overseer state, with the user's acknowledgment: a deploy freeze from first public sale through 1 Nov 2026, or an explicit acceptance of that loss window. Builders do not invent a third option.
- Sandbox end-to-end in dev does not require a public webhook URL once the synchronous result issues tickets. Webhook tests construct the notification and mock the re-fetch.

## 6. Decisions log

| # | Decision | Why |
|---|---|---|
| 1 | SQLite via `modernc.org/sqlite`, sqlc, embedded migrations, DSN pragmas, pool of one, Litestream entrypoint | Matches zh-nb, which is what was asked for GCP. Pragmas live in the DSN so recycled connections keep `foreign_keys`. |
| 2 | Resend `v2.28.0` or newer, QR PNGs inline via `Content-ID`, `RESEND_FROM` required | The email is the ticket the buyer shows at the door. Fidelio's v2.19.0 has no `ContentId`, and Gmail strips data-URI images. Attachments would not render in the message body. |
| 3 | One order, one buyer, quantity 1–10. No per-ticket names | The form is one payer. Email confirm is checked and not stored. |
| 4 | Card data stays in Mercado Pago iframes. Our request carries a token only | Plain named inputs would still POST the PAN even if the handler ignored it. CSP must allow the SDK script, frames, connect, and images. |
| 5 | `TICKET_PRICE` is whole pesos, parsed once to cents. Charge is `qty * unit_price_cents` on the server. No fees. 900, 250, and 972 hardcodes are removed | 250 MXN is the price. The template's other figures are stale. |
| 6 | One admin. Credentials are env vars, constant-time compare, reject empty. Session stores a hash. Cookie lasts 7 days | No users table. The door phone must stay signed in across the weekend. |
| 7 | Held seats = every ticket row (`reserved`, `issued`, and `used`). Cap 300. Release deletes `reserved` rows | A scan on 30 Oct, while sales are still open, must not put that seat back on sale. |
| 8 | Reservation hold is 15 minutes, swept lazily inside the checkout transaction before the capacity check | Abandoned checkouts return seats. No cron. A synchronous terminal result does not wait for this timer. |
| 9 | The confirmation email is the ticket. One message, one CID QR per ticket. Door-staff wording in the copy | No buyer portal. A buyer who opens the link hits admin login, which is intended. |
| 10 | Late success after the reservation was released: re-insert tickets only if seats remain; otherwise `paid` + `unfulfilled`, no email, refund by hand | Avoids overselling and avoids losing a paid order silently. A still-live reservation just issues its existing rows. |
| 11 | No background email retry. Each trigger gets three immediate attempts. Admin resend uses the stored tokens | Chosen after the automatic sweeper was rejected. |
| 12 | The attention list is only stuck paid orders, including `not_sent` older than about 10 minutes | A crash between commit and send must be visible. The 10-minute lag avoids listing an order whose send is still in flight. |
| 13 | Sales close at the fixed instant `2026-10-31T06:00:00Z`. It gates new checkouts only | Alpine has no tzdata, and `CGO_ENABLED=0` does not load the zone database. Mexico City has been UTC−6 since 2022. Scanning and webhook confirmation ignore the cutoff. |
| 14 | Claim URL is `{APP_BASE_URL}/tickets/claim/{token}`. Admin session required. First scan wins. Token redacted in logs | The phone camera opens the link. The secret must not land in Cloud Logging. |
| 15 | Ticket `token` is generated at issue time and stored in plaintext. Reserved rows have none. Admin sessions stay hash-only | The webhook and a later resend both need the same secret. A hash cannot be reversed. HMAC-with-`TICKET_SECRET` was the alternative; plaintext is simpler and redemption still requires the admin session. |
| 16 | Every Orders API create sets `external_reference` and an idempotency key from the local order id. The webhook resolves that field first, then re-fetches the order from Mercado Pago | A lost HTTP response after a successful charge must still match. The body of the webhook is not the source of truth. |
| 17 | Synchronous result and webhook call one transition. Terminal states stick. Network stays outside the transaction. Shutdown drains on SIGTERM | Otherwise a missing webhook expires a paid order at 15 minutes, and a deploy kills in-flight charges. |
| 18 | Cloud Run: max-instances 1, gen2, no CPU throttling. Documented in the README and entrypoint comments | The cap and the sessions are process-local. This is a deploy constraint, not a suggestion. |
| 19 | Deploy-loss window is an overseer gate before the first production sale: record a freeze through 1 Nov 2026, or record explicit acceptance | Overlapping revisions and the replica interval can drop a paid order or a `used` stamp. Builders do not decide this. |

## 7. Verification

- Tier 0: temp-file migration; boot rejects empty required env and a bad price; compose volume restart; entrypoint branch reviewed, GCS not called.
- Tier 1: checkout tests listed above. Mercado Pago may be stubbed in CI. Sandbox credentials are optional and not a merge gate.
- Tier 2: transition and webhook tests listed above, with a mocked re-fetch. Status map written down before the handler is implemented.
- Tier 3: claim twice, redaction, attention list, admin resend.
- Effort gate: `go build ./...`, `go vet ./...`, `templ generate` clean, image builds, README contains the three Cloud Run flags.

## 8. Locks

These are non-negotiable. They restate `RED_TEAM.md` so a reader of this file alone still has them.

1. Held capacity includes `used`. Released tickets are deleted and do not count.
2. Ticket tokens are plaintext, created at issue time, reproducible for resend. Do not copy zh-nb's hash-only session pattern onto tickets.
3. `external_reference` and the idempotency key are the local order id. Webhook lookup uses `external_reference`.
4. One idempotent transition for the synchronous result and the webhook. Terminal states do not move again.
5. Commit paid/fulfilled before any email. No Mercado Pago call, Resend call, or pool query inside a transaction.
6. Card data never enters our DOM or request body. CSP allows only the Mercado Pago origins that Secure Fields or Card Form need.
7. Price comes from `TICKET_PRICE`, parsed once to cents. The server computes the charge. Templates and JS have no hardcoded prices.
8. Cutoff is `2026-10-31T06:00:00Z`. It gates new checkouts only.
9. Production deploy is one Cloud Run instance: max-instances 1, gen2, no CPU throttling.
10. Required env vars fail boot. Admin compare is constant time and rejects empty values.
11. Claim tokens never appear in logs.
12. The admin list includes `paid` + `fulfilled` + `not_sent` older than about 10 minutes.
13. Before the first production sale, the overseer records the deploy-freeze decision from Risk / Decision 19.
