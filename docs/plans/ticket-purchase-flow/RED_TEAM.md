# Red Team: ticket-purchase-flow

Audit of `docs/plans/ticket-purchase-flow/PLAN.md`, 2026-10-13. Repo facts were grounded by read-only explore subagents against `raices`, `zh-nb` (`/Users/mac/Desktop/zh-nb`), and `fidelio` (`/Users/mac/Desktop/fidelio`). Line refs are a snapshot; re-verify before a builder edits.

Severity key: **block** = overseer must not start until `PLAN.md` changes. **fix-in-plan** = change `PLAN.md` before the affected tier's kickoff. **note** = overseer may proceed if the locks section records it.

Summary: 3 block, 12 fix-in-plan, 7 note.

---

## Block

### B1. The capacity count stops counting a ticket once it is scanned

- **Plan claim broken:** Tier 0 capacity option "`COUNT(*) FROM tickets WHERE status IN ('reserved','issued')` against 300"; Decision 7 "300 total tickets (reserved + issued)"; Success metric "total issued+reserved tickets".
- **Evidence:** The plan's own ticket state machine is `reserved → issued → used` (Tier 0 schema, Tier 3 claim flow). A ticket flipped to `used` drops out of `reserved`+`issued`. Opening night (30 Oct) is also the sales-cutoff day, so door scans run while sales are still open. Every scan frees a seat to sell again, so the venue can be oversold.
- **Required plan change:** Redefine held capacity as every ticket row that is not released: `reserved` + `issued` + `used`. Do this in Decision 7, the Tier 0 capacity bullet, Tier 1 step (a), and Success metric 3. Add a Tier 1/Tier 3 test: issue tickets, scan one, and assert that available capacity does not change.

### B2. Tier 1 creates hash-only ticket tokens, but Tier 2 needs the plaintext later

- **Plan claim broken:** Tier 1 step (c) "inserts `qty` `tickets` rows `reserved` with generated+hashed per-ticket tokens". Tier 2 then builds QR images "using the ticket's plaintext token — generated once at reservation time and not persisted in plaintext... decide storage approach during implementation". Risk 6 defers the choice to Tier 2.
- **Evidence:** The reservation runs in the `POST /register/pay` request. The QR images are built in a different request (the webhook), and again later on admin manual resend (Tier 3). zh-nb's pattern stores only `token_hash` (SHA-256 hex, `zh-nb/app/auth.go:98-100`; `sessions` schema `zh-nb/database/migrations/000010_create_sessions.sql:1-6`), and a hash can't be turned back into the plaintext. Tier 0 fixes the `tickets` schema before Tier 2 can decide anything, so deferring this decision means a migration rewrite or an email step that can't work.
- **Required plan change:** Settle the decision now in the Decisions log, and update Tier 0's schema to match.
  - **Recommended option:** reserved tickets carry no token. Generate the token when the ticket moves `reserved → issued` (Tier 2). Store it as a plaintext unique column, which makes it reproducible for resend. Look it up by an indexed equality match.
  - **Acceptable alternative:** derive the token as an HMAC of `ticket.id` under a new required `TICKET_SECRET` env var, with no stored token.
  - Either way, rewrite Risk 6, the Tier 1 step (c) wording, the Tier 2 QR bullet, and the Scope QR bullet ("stored hashed, mirroring `sessions.token_hash`") to match.

### B3. A webhook can't be matched to its order if the synchronous Mercado Pago response is lost

- **Plan claim broken:** Tier 1 says "Then calls Mercado Pago to charge... Store `mp_order_id` on the order." Scope says the webhook "loads the referenced order".
- **Evidence:** The order is committed before the Mercado Pago call (Tier 1). If that call times out, the process restarts, or Cloud Run kills the request after Mercado Pago charged the card, `mp_order_id` is never stored. The webhook then carries an ID that no local row has. That is a charged buyer with no order and no ticket, and it won't appear on the admin list. The SDK in use already supports both fixes: `github.com/mercadopago/sdk-go v1.14.0` (`go.mod:8`) has `order.Request.ExternalReference` (`pkg/order/request.go:14`) and `requestoptions.WithIdempotencyKey`, which sets `X-Idempotency-Key` (`pkg/order/client.go:38-41`). `MPClient` already wraps `order.Client` (`payment/config.go:10-21`). The server has no graceful shutdown (`main.go:39-48`, plain `ListenAndServe`), so SIGTERM during a deploy cuts off in-flight charges.
- **Required plan change:** Add to Tier 1:
  - Every Mercado Pago order create sets `external_reference` to our local order ID, and sets the idempotency key to a value derived from our local order ID.
  - The webhook resolves the local order by `external_reference` first, and uses `mp_order_id` only as a fallback.
  - Add graceful shutdown (handle SIGTERM and drain with `http.Server.Shutdown`) to Tier 0 `main.go` wiring.

---

## Fix-in-plan

### F1. The plan ignores the synchronous charge result, so paid orders can be expired

- **Plan claim broken:** Tier 1 "final truth comes from the webhook"; the lazy sweep expires any `pending_payment` order past `reserved_until` "with no terminal webhook yet". The plan says nothing about a synchronous rejection.
- **Evidence:** `MPClient` uses the Orders API (`payment/config.go:10-21`). With a card token, that API normally returns a final result in the create response. If the webhook is misconfigured, delayed (Mercado Pago retries for hours), or the endpoint is down, every approved order expires at 15 minutes and its seats are resold. When the webhook finally arrives it falls into the late-approval path. That path either issues tickets the buyer should already have had, or marks a correctly paid buyer `unfulfilled` (Decision 10). A synchronous rejection (declined card, or a single-use card token reused by a double-submit) leaves the seats held for 15 minutes for no reason.
- **Required plan change:** Tier 1: send the create response through the same idempotent state-transition function the webhook uses. Approved means issue the tickets immediately. Failed means release the seats immediately and show the buyer an error on the page. Webhook processing then becomes confirmation or recovery. Add a test where the response is approved and no webhook ever arrives, and assert the order is not expired.

### F2. The status names are Payments API vocabulary, not Orders API vocabulary

- **Plan claim broken:** Tier 2 "On `approved`" and "On `rejected`/`cancelled`".
- **Evidence:** The integration is the Orders API (`payment/config.go:10-21`, `pkg/order`), not the Payments API. It is an unverified assumption that the Orders API order status values are `approved`/`rejected`. The current Orders API docs describe statuses such as `processed`, `action_required`, `failed`, `canceled` with a separate `status_detail`, and a webhook topic of `order`. Verify this against the docs.
- **Required plan change:** Tier 2: the builder must map the actual Orders API order status and status_detail values (taken from the docs or SDK response types) onto the plan's three outcomes: success, terminal failure, and still pending. Write that mapping into the plan or the handoff before writing the webhook. The webhook must also re-fetch the order from Mercado Pago by ID instead of trusting the notification body.

### F3. "Re-check capacity" on late approval is ambiguous, and released tickets have no state

- **Plan claim broken:** Tier 2 "On `approved`: re-check capacity; if room remains... flip its tickets `reserved`→`issued`". Tier 1/2 "release its tickets".
- **Evidence:** The ticket statuses are only `reserved|issued|used` (Tier 0), so "release" is undefined: delete the rows, or add a new status? If the reservation is still live, the order's own reserved tickets are already in the count, so a naive re-check counts them twice. That can falsely mark a paid order `unfulfilled` when the event is close to full. If the reservation already expired, its tickets were released, so "flip its tickets" has no rows to act on.
- **Required plan change:** Define release as deleting the order's `reserved` ticket rows (simplest with B1's counting rule), or add a `released` status and exclude it from the count. Define the approval rule in two cases:
  - Reservation still live and the order's tickets still `reserved`: issue them with no capacity check.
  - Order `expired` or `rejected`: insert fresh tickets only if `300 − held ≥ qty`, all in one transaction; otherwise mark the order `paid` + `unfulfilled`.

  Add both cases to the Tier 2 tests (Risk 3 already asks for this race to be tested).

### F4. A paid order whose email never went out is invisible to the admin list

- **Plan claim broken:** Scope and Tier 3 list `email_status=failed`; Success metric 6 says "exactly" `unfulfilled` OR `email_failed`.
- **Evidence:** The email is sent after the paid transition. A crash, SIGTERM (no graceful shutdown, `main.go:39-48`), or a webhook request timeout between that commit and the send leaves `paid`+`fulfilled`+`not_sent`, and that state never appears on the list. fidelio's retry backs off 2s/4s/8s on every error (`fidelio/api/mailing.go:45-54`, `helpers/helpers.go:194`). That is up to 14 seconds of sleeping inside the webhook request, which invites a Mercado Pago retry and a Cloud Run cancellation.
- **Required plan change:** Also include `paid`+`fulfilled`+`not_sent` orders older than about 10 minutes on the list, and update Success metric 6 to match. Lock that the paid transition commits before any email attempt and that sending happens outside any transaction (see the locks section).

### F5. Gmail-safe inline QR images aren't possible with fidelio's `resend-go` version

- **Plan claim broken:** Scope "inline/attached QR PNGs"; Decision 2 "mirroring fidelio".
- **Evidence:** fidelio pins `resend-go/v2 v2.19.0` (`fidelio/go.mod:18`). In that version `Attachment` has only `Content`, `Filename`, `Path`, `ContentType`, with no `ContentId` (`emails.go:81-98` in the v2.19.0 module source). fidelio never sends attachments, so there is nothing of its to mirror. Data-URI `<img>` tags are stripped by Gmail. CID inline images need v2.28.0 or later (present in the local module cache, but adopting it is still a dependency change).
- **Required plan change:** Choose in the Decisions log: either (a) send the QR PNGs as regular attachments on v2.19.0 (works everywhere, no inline display), or (b) pin a `resend-go` version with `ContentId` and embed the images by CID. Either way, add a `RESEND_FROM` sender address env var.

### F6. The CSP fix covers more than `script-src`, and the card fields are plain inputs

- **Plan claim broken:** Out-of-scope note and Tier 1 "Fix CSP to allow the Mercado Pago SDK script"; Decision 4 "raw card data must never touch our server".
- **Evidence:** The CSP is `default-src 'self'; script-src 'self'; ... connect-src 'self'; ... frame-ancestors 'none'` with no `frame-src` (`middleware.go:85`). The SDK loads from `https://sdk.mercadopago.com/js/v2` (`register.templ:97`), and the `new MercadoPago(...)` constructor is an inline script (`register.templ:99`), which `script-src 'self'` also blocks. The card fields are plain `<input>` elements with `name` attributes `card_number`, `card_exp`, `card_csc`, `card_name` (`register.templ:222-236`) inside a form that posts to `/register/pay` (`:167-168`). Removing "server-side handling" alone still sends the raw card number in the request body.
- **Required plan change:** Tier 1:
  - Use Mercado Pago Secure Fields or Card Form (iframes), so card data never sits in our DOM or form body, and remove those `name` attributes.
  - Move the inline constructor into a `'self'` JS file (or use a nonce).
  - Widen the CSP for exactly the Mercado Pago origins the SDK needs across `script-src`, `frame-src`, `connect-src`, and `img-src`, taken from Mercado Pago's docs.
  - Acceptance: with DevTools open, no CSP violations appear during a sandbox tokenization, and the `/register/pay` request body contains no card fields.

### F7. The price is hardcoded in more places than the plan lists

- **Plan claim broken:** Decision 5 and Tier 1 "remove hardcoded 900".
- **Evidence:** 900 is at `register.templ:131`, but the summary placeholders also hardcode 250 (`:62`, `:68`) and 972 (`:72`), and the Alpine component hardcodes `price: 250` (`public/js/register.js:12`). `env.TicketPrice` is a `string` that nothing parses (`env/env.go:22`), yet the schema uses `unit_price_cents`.
- **Required plan change:** Tier 1:
  - Parse `TICKET_PRICE` once at boot into integer cents and fail fast on bad input.
  - Have the template render that value to the JS component (for example as a data attribute) and remove every hardcoded price in both `register.templ` and `register.js`.
  - Explain or remove the 972 figure: is it a fee-inclusive total? The plan has no fees.
  - Compute the charge amount on the server only.

### F8. The `env` package has no fail-fast mechanism, and several env vars the plan needs aren't listed

- **Plan claim broken:** Tier 0 "`env.SqlitePath` (required, no default — fail fast like zh-nb)".
- **Evidence:** Every variable in `env/env.go` is a `string` falling back to a default through `getenv` (`:27-31`). Nothing is required; `MP_ACCESS_TOKEN` defaults to `""` (`:24`). The plan also introduces `ADMIN_EMAIL`, `ADMIN_PASSWORD`, `RESEND_API_KEY`, a Mercado Pago webhook signing secret, a sender address, and a Litestream replica URL without listing them. If both admin credentials are empty and the comparison is careless, the login lets anyone in.
- **Required plan change:** Tier 0: add a required-var helper and list every new env var with whether it's required in prod. `SQLITE_PATH`, `MP_ACCESS_TOKEN`, `MP_PUBLIC_KEY`, the webhook secret, `RESEND_API_KEY`, `ADMIN_EMAIL`, and `ADMIN_PASSWORD` must be non-empty at boot. The admin comparison must reject empty values and use a constant-time compare.

### F9. The sales cutoff breaks inside the container because Alpine has no timezone data

- **Plan claim broken:** Decision 13 cutoff "end of day 30 Oct 2026 (America/Mexico_City)" enforced server-side.
- **Evidence:** The final image is `alpine:3.22` with `ca-certificates` and no `tzdata` (`Dockerfile:19-20`). The build uses `CGO_ENABLED=0` (`Dockerfile:6-7`), and the repo never imports `time/tzdata` or calls `time.LoadLocation`. `LoadLocation("America/Mexico_City")` would fail in the container, so the cutoff check either errors or gets skipped.
- **Required plan change:** Tier 1: define the cutoff as the fixed UTC instant 2026-10-31T06:00:00Z (Mexico City has had no daylight saving time since 2022, so it is UTC−6). Alternatively, import `time/tzdata`. Add a test at the cutoff instant plus and minus one second. The cutoff applies only to new checkouts, never to the webhook or the synchronous result for orders created before it.

### F10. Plaintext ticket tokens would land in the request logs

- **Plan claim broken:** Scope "token is a per-ticket random secret".
- **Evidence:** The request logger writes `r.Method, r.URL.Path, status, duration` (`middleware.go:55`). Every `GET /tickets/claim/{token}` would put the bearer secret into stdout, which becomes Cloud Logging in prod.
- **Required plan change:** Tier 3: redact the token segment for `/tickets/claim/` paths in the logging middleware. Add a test or an acceptance check that the log line contains no token.

### F11. The plan doesn't pin Cloud Run to one instance, which a SQLite cap requires

- **Plan claim broken:** Risk 2 "`MaxOpenConns(1)`... simplest way to make the capacity check+reserve atomic"; Scope "matching the zh-nb deploy shape".
- **Evidence:** `MaxOpenConns(1)` only serializes writes inside one process. zh-nb works because its README deploys with `--max-instances 1 --execution-environment gen2 --no-cpu-throttling`. Its entrypoint runs `litestream restore -if-replica-exists` and then `litestream replicate -exec` (`zh-nb/scripts/entrypoint.sh:13-20`), and it has no Cloud Run volume. raices has no deploy config and no max-instances setting anywhere. A second instance would have its own SQLite file: a second 300 cap, sessions that only exist on one instance, and webhooks landing on the wrong database.
- **Required plan change:** Add max-instances 1, gen2, and no CPU throttling to Decisions as a non-negotiable deploy constraint. Document them in the raices README and the entrypoint deliverable, the way zh-nb does. No CPU throttling also matters if any email work runs after the response is written.

### F12. The persistence success metric tests a volume that Cloud Run doesn't have

- **Plan claim broken:** Success metric 5 "DB file survives container restart (volume-mounted)"; Tier 0 "writable volume".
- **Evidence:** zh-nb's prod compose uses a named volume (`zh-nb/compose.prod.yml:13-17`), but on Cloud Run data survives only through a Litestream restore from GCS (`entrypoint.sh:13-20`). raices has `compose.dev.yml`/`compose.prod.yml` and no `VOLUME` (Dockerfile).
- **Required plan change:** Split the metric in two: (a) a compose volume restart test, which is runnable now, and (b) the entrypoint running restore-then-replicate when `LITESTREAM_REPLICA_URL` is set, and passing straight to the binary when it isn't. Running (b) against a real bucket stays out of scope, as the plan already says.

---

## Note

### N1. A deploy during sales or the event can silently lose writes

During a revision rollout, Cloud Run briefly runs the old and new revision at the same time even with max-instances 1. The new instance only restores from the replica if its local file is missing (`entrypoint.sh:13-20`). Writes the old instance accepts after that restore are lost, which could mean paid orders or `used` stamps. Litestream's default sync interval also leaves a short loss window on a crash. Overseer: record a deploy freeze from the first public sale through 1 Nov 2026, or accept the risk explicitly.

### N2. A single database connection plus network calls inside a transaction would deadlock

zh-nb's `InTx` uses plain `BeginTx` with sqlc `WithTx` (`zh-nb/database/tx.go:9-19`), and its pool is capped at one connection (`database.go:46-47`). A pool query issued while a transaction is open blocks forever. A Mercado Pago or Resend call inside a transaction stalls every other request for its whole duration. This is covered in the locks section.

### N3. Pragmas: copy zh-nb's DSN approach exactly

zh-nb sets its pragmas through DSN `_pragma=` parameters (`zh-nb/database/database.go:35-37`), so they apply to every connection. Don't "simplify" this into `db.Exec` calls after opening, because recycled connections would silently lose `foreign_keys`.

### N4. The Mercado Pago webhook can't reach a local dev machine

Success metric 1 requires a real end-to-end sandbox purchase in dev. That needs a public URL (a tunnel or a deployed preview). Installing a tunnel tool requires user approval under the workspace's ask-before-installing rule. F1 reduces how much this matters, because the synchronous result completes most purchases without the webhook.

### N5. The login `next` parameter could be an open redirect, and the login handler ignores it

The current `handleLoginForm` (`app/auth.go:11-33`) always redirects to `/register`. Tier 3 adds a return-to `next` parameter. It must accept only same-origin relative paths: a leading `/` but not `//`, and no scheme.

### N6. Client and server disagree on the minimum quantity

The JS stepper allows `min: 0` (`register.js:13-14`); the server requires `ticketQtyMin=1` (`app/register.go:13-16`). This is harmless while the server validates `/register/pay` with the same `validateTicketQty`, which the builder must reuse.

### N7. A buyer who opens their own QR link lands on the admin login page

This follows from Decision 14 and isn't a bug, but the email copy should say the QR is for door staff to scan.

---

## Locks (non-negotiable for the overseer)

1. Held capacity = all ticket rows that are not released, `used` included. Released tickets are deleted or marked `released`, and never count.
2. The ticket token decision is made before the Tier 0 schema is written. It must be reproducible for resend; zh-nb's write-once hash pattern is not used for tickets.
3. Every Mercado Pago order create sets `external_reference` to the local order ID and an idempotency key derived from it. The webhook looks up orders by `external_reference`.
4. The synchronous Orders API result and the webhook go through one idempotent transition function. A terminal state never transitions again.
5. The paid/fulfilled transition commits before any email attempt. No network call (Mercado Pago, Resend) and no pool query happens inside a database transaction.
6. Raw card data never enters our DOM form body or request: Mercado Pago iframes or Card Form only, and the CSP is widened only for Mercado Pago origins.
7. The price comes from `TICKET_PRICE`, parsed once to integer cents at boot. The charge amount is computed on the server only, and there are no hardcoded prices in templates or JS.
8. The sales cutoff is a fixed instant, 2026-10-31T06:00:00Z (or the timezone database is embedded). It gates only new checkouts, never confirmations or scanning.
9. Deploy runs on one Cloud Run instance (max-instances 1, gen2, no CPU throttling), mirroring zh-nb.
10. Required env vars fail fast at boot. Admin credentials can't be empty and are compared in constant time.
11. Ticket tokens never appear in logs.
12. The admin list also shows `paid` + `fulfilled` + `not_sent` orders older than about 10 minutes.
13. The deploy-freeze decision (N1) is recorded by the overseer before the first production sale.
