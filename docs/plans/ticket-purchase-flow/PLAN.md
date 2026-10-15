# Plan: Ticket Purchase Flow (SQLite + Mercado Pago + Resend + QR check-in)

Plan name: `ticket-purchase-flow`

**Revision note (2026-10-14):** This revises the prior version of this plan. The single biggest change: the synchronous Orders API response **no longer issues tickets or triggers email on success**. Only the Mercado Pago webhook does. The synchronous call is used solely to detect an immediate decline (release seats, show an error) and to detect a bank authentication step (render the challenge, then resume polling). This was a deliberate, discussed trade-off: it forces the local dev loop to exercise a real webhook round-trip (through a temporary tunnel) before anything is considered proven, rather than letting the synchronous path mask a webhook that was never built or never worked. Decisions 10 and 17 from the prior revision are superseded; see Decision 20 below. Everything else (schema, capacity rule, cutoff, admin session, claim/redemption, Cloud Run shape) is unchanged.

This document is self-sufficient. A fresh overseer who reads ONLY this doc can run the effort. The 2026-10-14 red team (`RED_TEAM.md`, 2 block, 6 fix-in-plan, 5 note) is folded in below. The 2026-10-13 findings are not live instructions; they live in git history at `ca298f4`. Repo line refs are a snapshot; re-verify them with an explore subagent before a builder edits.

---

## 1. Objective

Turn `raices` from a UI-only registration stub into a working ticket sale:

1. A buyer fills the register form. Card data stays inside Mercado Pago Secure Fields or Card Form iframes. Our server receives a card token plus the payer fields, reserves seats, and charges through the Orders API. A synchronous decline releases the seats immediately and shows an error. A synchronous bank-authentication step is rendered in-page, then the buyer returns to a pending/confirming view. Every other synchronous outcome, including an outright synchronous approval, leaves the order reserved and unconfirmed. **Only the Mercado Pago webhook issues tickets and triggers the confirmation email.** The buyer's page polls until the webhook lands or the hold expires.
2. An admin (one env-configured credential) logs in. Opening a ticket QR link on their phone marks that ticket used. A second scan shows the original time and does not change the row.
3. Held seats never exceed 300. A scanned ticket still counts. Released seats are deleted and stop counting.
4. New checkouts stop at 2026-10-31T06:00:00Z (end of 30 Oct 2026 in Mexico City, which is UTC−6 year-round). The event runs 30 Oct – 1 Nov 2026. Scanning keeps working after sales close.
5. The admin view has two filters. **Attention** (default): paid-but-unfulfilled orders, paid orders with a failed email, paid-fulfilled orders still `not_sent` after about 10 minutes, and every `expired` order. **All orders**: every order regardless of status, for lookup. Email retry is an admin action from the Attention filter. There is no background resend loop, no dismiss button, and no automatic refund. Abandoned checkouts stay on Attention until a later webhook moves them; that is acceptable at a 300-seat scale.

## 2. Success metrics

1. A sandbox purchase whose synchronous call is approved does **not** issue tickets or send email by itself. Only after the matching webhook notification is received and processed does the order become `paid` + `fulfilled`, N `tickets` rows go `issued` with unique plaintext tokens, held count increases by exactly N, and one Resend email is sent whose HTML references the QR PNGs by `Content-ID`.
2. A documented decline — a 2xx body whose status maps to terminal failure, or a 4xx whose body is an order Mercado Pago documents as declined, or a 4xx Mercado Pago documents as "no order created" — releases the reserved seats and shows the buyer an error in that same request. A transport error, timeout, 5xx, 429, or 409 does not. No webhook is required to observe a documented decline.
3. A sandbox purchase whose synchronous call returns a bank-authentication step renders that step to the buyer; completing it leads back to the same pending/poll view, which still only resolves off the webhook.
4. A reservation whose synchronous call was approved, left pending, or ended in an unknown outcome, and whose webhook never arrives within 15 minutes, expires: reserved seats are released, the order is `expired`, no tickets are issued, no email is sent. Every expired order appears on the Attention filter, including one whose `mp_order_id` is null. Verified with a moved clock, including through the poll handler and the admin view with no further checkout.
5. An admin claim of an issued ticket sets `used` once. A second claim of the same link renders the original `used_at` and does not write.
6. A checkout that would push held tickets (`reserved` + `issued` + `used`) past 300 is rejected before Mercado Pago is called, and the page shows how many seats remain. Issuing tickets, then scanning one, does not increase seats available.
7. `go build ./...`, `go vet ./...`, and `templ generate` (no drift) pass.
8. Persistence, split in two:
   - Compose: with `SQLITE_PATH` on a named volume, a row written in the container is still there after a restart.
   - Cloud Run shape: the entrypoint runs `litestream restore` then `litestream replicate -exec` when `LITESTREAM_REPLICA_URL` is set, and execs the binary directly when it is empty. Exercising a real GCS bucket is out of scope.
9. The Attention filter shows exactly four kinds of order: `unfulfilled`, `email_status=failed`, `fulfilled` + `not_sent` older than about 10 minutes, and every `expired` order. A successful admin resend drops the email row off that filter. The All-orders filter shows every row regardless of status. Both filters show the local order id, labelled as the Mercado Pago `external_reference`.
10. **A full local purchase, start to finish, through a temporary public tunnel to this machine's Docker container, produces exactly one email**: the webhook that Mercado Pago sends to the tunnel URL is what issues the tickets and triggers the send. This is the manual end-to-end proof the effort is not done without.

## 3. Scope

### In scope

- SQLite mirrored from zh-nb: `modernc.org/sqlite`, sqlc, embedded numbered migrations applied on boot, `SetMaxOpenConns(1)`, pragmas set in the DSN (`busy_timeout`, `foreign_keys`, `journal_mode=WAL`, `synchronous=NORMAL`) so every connection gets them. `SQLITE_PATH` required. `.gitignore` for `*.db*` and generated sqlc output. Temp-file tests, not `:memory:`.
- Graceful shutdown: `SIGTERM`/`SIGINT` call `http.Server.Shutdown` with a bounded drain, from `main`.
- Schema. Do not edit `000001_init.sql`. Add migration `000002` only:
  - `orders.poll_token` (text, not null, unique). A fresh unguessable value minted when the order is inserted. It is not the ticket token and not the order id.
  - `orders.checkout_key` (text, not null, unique). Minted when the pay form is rendered and stored when the order is inserted. A resubmit with the same key returns that order's pending view and does not insert a second order or call Mercado Pago. A documented decline mints a fresh key so the buyer can try again.
  - SQLite `ADD COLUMN ... NOT NULL` requires a default. Give each column a non-secret default only so a database that already applied `000001` accepts the migration. Every new order inserts a real `poll_token` and `checkout_key`.
  - Existing `orders` columns stay: `id`, `given_name`, `family_name`, `email`, `age`, `church` (nullable), `quantity`, `unit_price_cents`, `total_cents`, `status` (`pending_payment` | `paid` | `rejected` | `expired`), `fulfillment_status` (`pending` | `fulfilled` | `unfulfilled`), `email_status` (`not_sent` | `sent` | `failed`), `email_last_error` (nullable), `mp_order_id` (nullable), `reserved_until`, timestamps.
  - `tickets`: `id`, `order_id` FK, `token` nullable unique, `status` (`reserved` | `issued` | `used`), `used_at` nullable, timestamps. Reserved rows have `token` NULL. The token is generated when the row moves to `issued` and stored in plaintext so a later resend can rebuild the same QR. Lookup is an indexed equality match on `token`.
  - `sessions`: `token_hash` primary key, `expires_at`. Admin session cookies stay hash-only. Ticket tokens do not.
  - Held capacity is `COUNT(*)` of ticket rows. Release deletes the order's `reserved` rows. There is no `released` status.
- `mp_order_id` is written by an update of that one column only, from a 2xx order body or from a parsed 4xx order body. A null `mp_order_id` does not mean nothing was charged: a transport error, timeout, 5xx, 429, or 409 can follow a charge Mercado Pago already accepted.
- Expiry is one function. Call it at the start of the checkout transaction, the poll handler, the admin view handler, and the webhook transaction. It deletes `reserved` tickets and sets the order `expired` when `reserved_until` has passed and the order is still `pending_payment`. When the webhook runs the sweep first, a late success for that order takes the expired re-insert branch.
- Checkout `POST /register/pay`:
  - Payer fields only: given name, family name, email (must match email confirm), age, church, quantity, and the Mercado Pago card token. Reuse `validateTicketQty` (1–10).
  - Card number, expiry, and CSC never appear in our DOM, form body, or request. Use Mercado Pago Secure Fields or Card Form (iframes). Remove the `name` attributes on the current plain card inputs.
  - Inside one transaction, before any network call: expire stale reservations (delete their `reserved` tickets, set the order `expired`), reject the purchase when `now >= 2026-10-31T06:00:00Z`, reject when `300 - held < qty`, insert the order `pending_payment` with `reserved_until = now+15m` and `fulfillment_status=pending`, insert `qty` ticket rows `reserved` with `token` NULL, commit.
  - The pay form carries the `checkout_key` the server minted for that render. A resubmit with an existing key returns that order's pending view: no insert, no Mercado Pago call. The idempotency key on a real create stays the local order id, which is stable across those resubmits.
  - After commit, call the Orders API. Set `external_reference` to the local order id. Set the idempotency key from that same id (`X-Idempotency-Key` via the SDK's `WithIdempotencyKey`). Any Mercado Pago response or error clears the hidden card token in the form; the next submit re-tokenizes. A card token is single-use and is never sent twice.
  - Classify the call, not only a success body:
    1. **2xx with an order body.** Map `status` / `status_detail`. Persist `id` as `mp_order_id`.
    2. **4xx whose body is order JSON** (expected for declines; the SDK returns this as a typed error whose message is the raw body, with a nil order). Parse that body into the order response type, persist its `id` as `mp_order_id`, then map its status. A documented decline is a terminal failure.
    3. **4xx that Mercado Pago documents as no order created** (invalid or consumed card token, or a validation error). Terminal failure. Nothing was charged.
    4. **Transport error, context timeout, 5xx, 429, or 409.** Unknown outcome. Not a terminal failure. The buyer goes to the pending view. The order expires normally if no webhook arrives.
  - Only a documented terminal failure acts synchronously: the failure branch deletes this order's `reserved` tickets, sets the order `rejected`, and the page shows an error. A needs-bank-authentication result renders the challenge inside the pending view while polling runs, so no completion callback is required; the webhook still resolves the order. The Tier 1 kickoff records the `transaction_security.validation` value that is sent, the origin of the sandbox challenge URL, and whether the pay response reaches the pending view by an htmx redirect or a swap target. If that origin is not a Mercado Pago origin, widening `frame-src` needs the user's sign-off in overseer state before it happens. The alternative is a top-level navigation to the challenge. Builders do not choose. Every other outcome, including a synchronous approval and an unknown outcome, changes nothing beyond the single-column `mp_order_id` write and hands the buyer to the pending view. The webhook is the only path that issues tickets and triggers email.
  - The pending/poll view polls a status endpoint with `poll_token` in the query string, never in the path and never as the bare order id. The request logger writes the path and does not write the query, so the token stays out of logs. The response carries no buyer name and no email. States:
    - still confirming (`pending_payment` inside the hold);
    - paid, email sent (only after `email_status=sent`);
    - paid, email failed, or `paid` + `fulfilled` + `not_sent` older than about 10 minutes: payment received, the email did not go out, contact the organizer;
    - `paid` + `unfulfilled`: payment received, no seats left, the organizer will refund;
    - declined (`rejected`);
    - expired: unconfirmed; if you were charged, contact the organizer.
    Polling stops on a terminal state, and in any case stops a fixed duration after the hold expires. The poll token resolves this one order's status only.
- Webhook `POST /webhooks/mercadopago`:
  - Verify the signature with the webhook secret. Do not trust the body for status or amount.
  - Re-fetch the order from Mercado Pago by id. Resolve the local row by `external_reference` first, then by `mp_order_id`.
  - Map real Orders API `status` / `status_detail` values onto the three outcomes above — success, terminal failure, still pending (the bank-authentication case collapses into "still pending" from the webhook's point of view; it only matters synchronously) — before writing the handler. The Payments API words `approved` / `rejected` are not the contract. Confirm the mapping against current Orders API docs or the v1.14 SDK types and write it into the tier kickoff.
  - This is the **only** caller allowed to drive the success branch of the transition function below. The synchronous handler never calls the success branch.
  - On a success mapping, compare the re-fetched `total_amount` and currency to the local `total_cents` (`25000` must match `250.00` MXN). On a mismatch, set `paid` + `unfulfilled` and send no email. That row stays on Attention.
  - Answer 200 for topics other than `order` and for unknown references, and log them, so Mercado Pago does not retry them for hours.
  - The Tier 2 kickoff picks whether the three email attempts run before the webhook returns 200 or after. Before: the backoff sits inside Mercado Pago's timeout, and a retry then hits the crash-shaped rule and does not send again. After: `http.Server.Shutdown` must drain the send. Builders do not leave this unspecified.
- Transition function, one transaction, no network inside it. The webhook alone calls the success branch. The webhook and the synchronous handler both call the failure branch. `paid` is absorbing.

  | Local status | Success (webhook only) | Failure |
  |---|---|---|
  | `pending_payment`, tickets still `reserved` | Set those rows `issued`, fill each `token` with a fresh 32-byte URL-safe secret, set `paid` + `fulfilled`. No second capacity check. | Delete remaining `reserved` rows, set `rejected`. |
  | `expired` or `rejected` (reserved rows gone) | Insert `qty` new `issued` tickets with tokens only when `300 - held >= qty`, and set `paid` + `fulfilled`. Otherwise set `paid` + `unfulfilled` and send no email. | On `expired`, set `rejected` (this clears Attention once Mercado Pago confirms there was no charge). On `rejected`, no-op. |
  | `paid` | No-op. | Log and ignore. Refunds and chargebacks are out of scope. |

  Commit a paid/fulfilled transition before any email attempt. Sending runs after the commit, outside every transaction. Only a fulfilled success sends. A failure never sends. An unfulfilled success never sends.
- Email: one message per paid fulfilled order. Spanish. Order summary (name, 250 MXN × qty, total, event 30 Oct–1 Nov 2026) and one QR PNG per ticket, embedded with `Content-ID` so the image renders in the message. Copy says the QR is for door staff to scan. `RESEND_FROM` is required. Pin `github.com/resend/resend-go/v2` at v2.28.0 or newer so `Attachment.ContentId` exists. Fidelio's v2.19.0 cannot do CID images and is not the version to copy. Each trigger (the webhook's send, and each admin click) makes up to three immediate attempts with the short backoff, then stops. No startup sweep and no interval loop. A failed send sets `email_status=failed` and stores `email_last_error`. Admin resend rebuilds QR images from the stored plaintext tokens.
- QR payload: `{APP_BASE_URL}/tickets/claim/{token}`. Pure-Go QR library, no CGO, compatible with `CGO_ENABLED=0`.
- Admin login: `ADMIN_EMAIL` and `ADMIN_PASSWORD` required and non-empty, compared in constant time. Match creates a `sessions` row (random token, store only the hash, expiry long enough to cover the event weekend — 7 days). HttpOnly, Secure, SameSite=Lax cookie. Empty credentials never authenticate.
- `GET /tickets/claim/{token}`: valid admin session required. Missing session redirects to login with `next` set to that path. `next` accepts only a same-origin relative path (leading `/`, not `//`, no scheme). Unknown token renders not-found. `issued` flips to `used` and shows buyer name, ticket index, and time. Already `used` shows the stored time and does not write. The request logger redacts the token segment on `/tickets/claim/` so Cloud Logging never stores it.
- `GET /admin` (name flexible): the landing page after login when there is no `next`. Two filters:
  - **Attention** (default): paid orders that are `unfulfilled`, or `email_status=failed`, or `fulfilled` + `not_sent` with `updated_at` older than about 10 minutes, or any `expired` order. Columns: buyer name, email, quantity, amount, status, Mercado Pago order id, the local order id labelled as `external_reference`, and for email rows the last error. Email rows have a resend button. Unfulfilled and expired rows have no issue button and no dismiss button.
  - **All orders**: every order regardless of status, same columns, no resend button on rows that aren't email-failed. For lookup only.
- Register copy: event dates 30 Oct – 1 Nov 2026 on both the header and the banner. No clock times. Remove the 20 Oct sales-end line. The page states that new purchases stop at the end of 30 Oct. Displayed unit price and line total come from the server. Remove every hardcoded money figure in `register.templ` and `register.js`, including 900, 250, and 972. There is no fee; 972 is a stale placeholder and is deleted. The Alpine component reads the unit price from a data attribute. The server computes `qty * unit_price_cents` for the charge.
- CSP, widened only for the Mercado Pago origins the chosen Secure Fields / Card Form docs require, across `script-src`, `frame-src`, `connect-src`, and `img-src`. The `new MercadoPago(...)` constructor moves into a `'self'` script (or a nonce). Acceptance: a sandbox tokenization produces no CSP violations in DevTools, and the `/register/pay` body contains a token and no card fields.
- Deploy hooks copied from zh-nb's shape: `scripts/entrypoint.sh` restore-if-replica-exists then replicate `-exec` when `LITESTREAM_REPLICA_URL` is set; otherwise exec the binary. Prod image `ENV SQLITE_PATH=/data/raices.db`, `mkdir /data`. README states the Cloud Run deploy constraint as non-negotiable: `--max-instances 1`, `--execution-environment gen2`, `--no-cpu-throttling`. Do not FUSE-mount a GCS bucket as the live database. Creating the bucket is a deploy-time human step, not part of the build.
- Boot fails closed. A required-var helper rejects empty values for: `SQLITE_PATH`, `APP_BASE_URL`, `TICKET_PRICE`, `MP_ACCESS_TOKEN`, `MP_PUBLIC_KEY`, `MP_WEBHOOK_SECRET`, `RESEND_API_KEY`, `RESEND_FROM`, `ADMIN_EMAIL`, `ADMIN_PASSWORD`. `TICKET_PRICE` is whole pesos (existing example value `250`); parse once at boot to integer cents (`250` → `25000`) and refuse non-positive or non-integer input. `LITESTREAM_REPLICA_URL` is optional. `PORT` and `ENVIRONMENT` keep their current defaults.

### Out of scope

- More than one admin, a users table, or roles.
- A button that issues tickets for an unfulfilled or expired order. Refund is manual in the Mercado Pago dashboard.
- A background job that resends email.
- A general order browser beyond the two filters, search, or export.
- More than one event or ticket type. Currency is MXN.
- In-app refunds.
- A public "my tickets" page. The email is the ticket.
- Running Litestream against a real bucket in CI.
- **Automating or scripting a tunnel inside the build or Docker image.** A temporary public tunnel (e.g. `cloudflared tunnel --url` or `ngrok http`) is a manual step the person testing runs on their own machine, pointed at the already-published container port, and is required to actually prove success metric 10. The build does not install, configure, or launch a tunnel tool itself.

## 4. Phased breakdown

### Tier 0 — Database, env, shutdown

- `database.Open`, DSN pragmas, pool of one, ping, migrate-on-boot, sqlc, `InTx`.
- Schema and migrations as in Scope. Held count is all ticket rows.
- Required env vars and `TICKET_PRICE` cents parsing, fail at boot.
- `main` opens the DB, and shuts the HTTP server down on signal.
- Dockerfile, entrypoint, compose volume, gitignore, README deploy constraint (max-instances 1, gen2, no CPU throttling).

Verification: migration test on a temp file; boot against a database that already has `000001` applied and assert `poll_token` and `checkout_key` exist afterward; bad `TICKET_PRICE` and empty `ADMIN_PASSWORD` refuse to boot; compose restart keeps a row; entrypoint script branches on whether `LITESTREAM_REPLICA_URL` is set (inspect the script; do not call GCS). Tier 0 and the Secure Fields, CSP, and server price attribute have already landed. Verify those against their checks. Do not re-kick them. The CSP DevTools check and the "no card fields in the request body" check are still owed.

### Tier 1 — Checkout, reserve, synchronous decline/challenge only

- CSP and Secure Fields / Card Form. Delete plain card inputs. Move the Mercado Pago constructor into a self-hosted script.
- Price rendering from the server. Delete 900, 250, and 972 hardcodes.
- `POST /register/pay` as specified: expiry sweep, cutoff instant, capacity, reserve, commit, then the Orders API call with `external_reference` and idempotency key. Classify the call four ways (2xx body, 4xx order body, 4xx with no order, unknown). A documented terminal failure runs the failure branch synchronously and shows an error. Needs-authentication renders the challenge inside the pending view. Everything else, including a synchronous approval and an unknown outcome, goes to the pending view. The only order write on those paths is the single-column `mp_order_id` update.
- The Tier 1 kickoff records the sandbox decline's HTTP status, the `transaction_security.validation` value sent, the challenge URL's origin, and how the pay response reaches the pending view.
- The pending/poll view and its status endpoint. `poll_token` is a query parameter.
- Cutoff test at `2026-10-31T05:59:59Z` (allowed) and `2026-10-31T06:00:00Z` (refused). The cutoff function is not called from the webhook or from the transition of an order created earlier.
- Tests: an error-shaped decline (typed error whose message contains an order body) releases seats and shows an error, with no tickets issued; a transport timeout leaves the order `pending_payment` and does not release seats; synchronous approval alone does not issue tickets, send email, or change ticket rows; capacity refusal; the held-count query includes a `used` row; post-cutoff refusal; a resubmit with the same checkout key creates no second order and no second Mercado Pago call; a retry after a decline uses a new card token; the poll endpoint rejects a wrong poll token. The fake client is the stubbing seam: `order.Client` is an interface and `MPClient` embeds it.

### Tier 2 — Transition, webhook, email

- One transition function, with the status table in Scope. Success is webhook-only. Failure is webhook or synchronous, and only on `pending_payment`, `expired`, or `rejected`. Document the Orders API status map in the kickoff after checking docs or SDK types. Record whether email runs before or after the webhook's 200.
- Webhook verifies, re-fetches, resolves by `external_reference`, compares amount and currency, and answers 200 for other topics and unknown references.
- Both late-success cases tested: live reservation issues with no extra capacity check; expired reservation re-inserts only when seats remain, otherwise `paid` + `unfulfilled`. Amount mismatch sets `paid` + `unfulfilled` and sends no email. `25000` cents is sent as `250.00`.
- Email after commit, CID QR images, three immediate attempts, then `sent` or `failed`.
- Expiry: an approved, pending, or unknown-outcome order with no webhook expires at 15 minutes. A transport-timeout order expires and appears on Attention with a null `mp_order_id`. After the cutoff, a stale `pending_payment` order becomes `expired` through a poll request and through the admin view, with no checkout involved.
- Tests: duplicate success notification does not mint a second set of tickets or send a second email; webhook success that lands before the synchronous handler finishes leaves the order `paid` with one set of tickets; a failure event for a `paid` order does not change it; a crash-shaped `paid` + `fulfilled` + `not_sent` row is not emailed again by the webhook.

### Tier 3 — Admin session, claim, two-filter admin view

- Login, session cookie, `next` allowlist.
- Claim route, first scan wins, log redaction test (the log line contains the path prefix and no token).
- Attention filter (four shapes, including every `expired` order) and All-orders filter. Both show the local order id as `external_reference`. Admin resend of the same tokens, available only on email-failed rows. No dismiss button.
- Test: issue, scan, assert held count unchanged; second scan unchanged; Attention filter contains only the four stuck shapes; All-orders filter contains every status; resend of a failed row sets `sent`.

## 5. Risks

- Orders API status strings must be read from current docs or SDK types. Do not ship a webhook (or the synchronous mapping) that switches on `approved`.
- The exact shape of a "needs bank authentication" response from the Orders API (what the SDK returns, what has to be rendered to the buyer — a redirect, a widget, an iframe) is not yet confirmed against Mercado Pago's current docs. The Tier 1 builder verifies this before writing the challenge-rendering code and records the finding in the tier kickoff.
- `MaxOpenConns(1)` serializes writes in one process only. A second Cloud Run instance would be a second 300-cap and a second session store. The README constraint is part of the effort's definition of done.
- A pool query or an HTTP call inside `InTx` deadlocks the single connection. Mercado Pago and Resend stay outside the transaction.
- Webhook signature details (header, secret, payload) are not in this repo. The Tier 2 builder takes them from Mercado Pago's current docs.
- QR library must be pure Go. Confirm before adding the dependency.
- During a Cloud Run revision rollout, old and new instances overlap briefly even at max-instances 1. Litestream restore-if-missing does not see writes the old instance accepts after the new instance has restored, and the default replica interval leaves a short crash window. Before the first production sale the overseer records one of these, in the overseer state, with the user's acknowledgment: a deploy freeze from first public sale through 1 Nov 2026, or an explicit acceptance of that loss window. Builders do not invent a third option.
- **Local end-to-end verification requires a real webhook round trip**, not just a synchronous success. The person testing runs a temporary tunnel pointed at the published container port and points Mercado Pago at `https://<tunnel-host>/webhooks/mercadopago`. The tunnel URL changes most times it restarts. The Orders API accepts a per-order `notification_url`. The overseer may set that from `APP_BASE_URL` plus the webhook path, after confirming Mercado Pago signs those notifications with the same webhook secret. Until that is confirmed, the tester registers the URL in the Mercado Pago dashboard by hand.
- The per-order poll token is a fresh unguessable value, not derived from the order id or the ticket tokens. Reusing a weaker scheme for convenience would let one buyer poll another's order.

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
| 8 | Reservation hold is 15 minutes. One sweep function runs at the start of checkout, poll, admin view, and webhook. No cron. | A sweep that lives only inside checkout never runs after sales close, and a quiet stretch leaves the poll saying "still confirming" forever. |
| 9 | The confirmation email is the ticket. One message, one CID QR per ticket. Door-staff wording in the copy | No buyer portal. A buyer who opens the link hits admin login, which is intended. |
| 10 | **Superseded by Decision 20.** Originally: a synchronous success issued tickets immediately via the shared transition, with the webhook as confirmation/recovery. | Reversed because the user wants the full flow, including the webhook, exercised locally before trusting it — a synchronous-success shortcut would let local testing pass without the webhook ever having worked. |
| 11 | No background email retry. Each trigger gets three immediate attempts. Admin resend uses the stored tokens | Chosen after the automatic sweeper was rejected. |
| 12 | The attention list includes `not_sent` older than about 10 minutes | A crash between commit and send must be visible. The 10-minute lag avoids listing an order whose send is still in flight. |
| 13 | Sales close at the fixed instant `2026-10-31T06:00:00Z`. It gates new checkouts only | Alpine has no tzdata, and `CGO_ENABLED=0` does not load the zone database. Mexico City has been UTC−6 since 2022. Scanning and webhook confirmation ignore the cutoff. |
| 14 | Claim URL is `{APP_BASE_URL}/tickets/claim/{token}`. Admin session required. First scan wins. Token redacted in logs | The phone camera opens the link. The secret must not land in Cloud Logging. |
| 15 | Ticket `token` is generated at issue time and stored in plaintext. Reserved rows have none. Admin sessions stay hash-only | The webhook and a later resend both need the same secret. A hash cannot be reversed. HMAC-with-`TICKET_SECRET` was the alternative; plaintext is simpler and redemption still requires the admin session. |
| 16 | Every Orders API create sets `external_reference` and an idempotency key from the local order id. The webhook resolves that field first, then re-fetches the order from Mercado Pago | A lost HTTP response after a successful charge must still match. The body of the webhook is not the source of truth. |
| 17 | **Superseded by Decision 20.** Originally: "synchronous result and webhook call one transition; terminal states stick." | Still true for terminal failure. No longer true for success — see Decision 20. |
| 18 | Cloud Run: max-instances 1, gen2, no CPU throttling. Documented in the README and entrypoint comments | The cap and the sessions are process-local. This is a deploy constraint, not a suggestion. |
| 19 | Deploy-loss window is an overseer gate before the first production sale: record a freeze through 1 Nov 2026, or record explicit acceptance | Overlapping revisions and the replica interval can drop a paid order or a `used` stamp. Builders do not decide this. |
| 20 | **The webhook is the sole trigger for ticket issuance and email.** A documented terminal failure still releases seats and shows an error in that same request. A bank-authentication step renders inside the pending view while polling runs. A synchronous approval and an unknown outcome (transport error, timeout, 5xx, 429, 409) change nothing but the single-column `mp_order_id` write, when an id was actually parsed, and hand the buyer to the pending view. An order with no webhook within 15 minutes expires like any other abandoned reservation. | Local dev must see a real webhook before a purchase counts as done. A decline is safe to release immediately only when Mercado Pago's response says no charge exists or the decline is documented. A lost response can still be a charge. |
| 21 | A bank-authentication ("still needs 3-D Secure" / `action_required`-shaped) response is rendered to the buyer as its own step, then the buyer returns to the same pending/poll view that everything else uses. | A real card can require this step. Treating it as a terminal failure would wrongly show declines for cards that just need one more step; treating it as silent success would skip a step Mercado Pago requires. |
| 22 | The buyer's pending view is addressed by `poll_token` in the query string. The page says the tickets were emailed only after `email_status=sent`. Email failure and a `not_sent` row older than about 10 minutes share one message. `paid` + `unfulfilled` says the organizer will refund. The response has no buyer name or email. | The request logger writes the path. A token in the path would land in Cloud Logging. A sequential order id would let one buyer poll another's order. The page must not claim a send that has not happened, and it must have a state for a charge that could not be seated. |
| 23 | Attention (default) is the three stuck-paid shapes plus every `expired` order. Both filters show the local order id as `external_reference`. Neither filter mints tickets or dismisses a row. | A null `mp_order_id` can still be a charged order when the synchronous response was lost. That buyer has to be visible. The local id is what the admin searches for in Mercado Pago when the Mercado Pago id is blank. |
| 24 | Installing or scripting a tunnel is explicitly out of scope for the build; running one is a manual step by whoever is testing, and the Mercado Pago sandbox webhook URL is updated by hand to match it each session. | Covered under the workspace's ask-before-installing rule, and a tunnel's lifetime is tied to a person's terminal session, not to the container. |

## 7. Verification

- Tier 0: temp-file migration; boot rejects empty required env and a bad price; compose volume restart; entrypoint branch reviewed, GCS not called.
- Tier 1: checkout tests listed above. CI stubs cover an error-shaped decline, a transport timeout, a 2xx approval, a needs-authentication body, and a still-pending body. Only a documented terminal failure acts synchronously. The Tier 1 kickoff records the real sandbox decline status before the handler is written. Sandbox credentials are optional and not a merge gate.
- Tier 2: transition and webhook tests listed above, with a mocked re-fetch. Status map, amount check, and the email-versus-200 choice are written down before the handler is implemented. Expiry through poll and admin, including a null `mp_order_id`, is a merge gate.
- Tier 3: claim twice, redaction, both admin filters, admin resend.
- Effort gate: `go build ./...`, `go vet ./...`, `templ generate` clean, image builds, README contains the three Cloud Run flags.
- **Manual, human-run gate (not CI):** with the dev container running and a temporary tunnel pointed at it, register that tunnel's URL as the sandbox webhook endpoint, complete one sandbox purchase with an approving test card, and confirm exactly one email arrives and it arrived only after the webhook request hit this machine's logs — not before. This is success metric 10 and it is the actual proof the effort works, independent of the unit/integration tests above.

## 8. Locks

These are non-negotiable. They restate `RED_TEAM.md` and this revision's grill session so a reader of this file alone still has them.

1. Held capacity includes `used`. Released tickets are deleted and do not count.
2. Ticket tokens are plaintext, created at issue time, reproducible for resend. Do not copy zh-nb's hash-only session pattern onto tickets.
3. `external_reference` and the idempotency key are the local order id. Webhook lookup uses `external_reference`. Persist `mp_order_id` from a 2xx body or from a parsed 4xx order body, by updating that column only. Schema changes go in `000002` or later. Never edit `000001_init.sql`.
4. **The webhook is the only caller of the success branch.** `paid` is absorbing. The failure branch acts only on `pending_payment`, `expired`, or `rejected`. The synchronous handler calls it only for a documented terminal decline, never for an approval, a pending result, or an unknown outcome.
5. One expiry function runs in checkout, poll, admin view, and webhook. An order with no webhook within 15 minutes expires. It is not kept alive because the synchronous call looked good.
6. Commit paid/fulfilled before any email. No Mercado Pago call, Resend call, or pool query inside a transaction.
7. Card data never enters our DOM or request body. CSP allows only the Mercado Pago origins that Secure Fields or Card Form need. Any wider `frame-src` needs the user's sign-off recorded in overseer state.
8. Price comes from `TICKET_PRICE`, parsed once to cents. The server computes the charge. Templates and JS have no hardcoded prices.
9. Cutoff is `2026-10-31T06:00:00Z`. It gates new checkouts only.
10. Production deploy is one Cloud Run instance: max-instances 1, gen2, no CPU throttling.
11. Required env vars fail boot. Admin compare is constant time and rejects empty values.
12. Claim tokens never appear in logs. `poll_token` is a query parameter, never a path segment, and the poll response carries no buyer name or email.
13. Attention includes every `expired` order and shows the local order id as `external_reference`. Neither filter mints tickets or dismisses a row.
14. The pending page says the tickets were emailed only after the send has completed. It has a state for `paid` + `unfulfilled` and for an email that never went out.
15. Before the first production sale, the overseer records the deploy-freeze decision from Risk / Decision 19.
16. Building, installing, or launching a tunnel is not part of this effort's code. It is a manual step taken by whoever is testing locally.
17. A card token is never resubmitted after any Mercado Pago response or error. A resubmit with the same `checkout_key` creates no second order and no second charge.
