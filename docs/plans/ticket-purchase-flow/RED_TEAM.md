# Red Team: ticket-purchase-flow (revision 2)

Audit of the 2026-10-14 revision of `docs/plans/ticket-purchase-flow/PLAN.md` (webhook-only issuance, Decision 20). This replaces the 2026-10-13 audit. Those findings are already folded into `PLAN.md` §8 Locks, and some of them (old F1 and old lock 4, "synchronous approval issues tickets") are superseded by Decision 20, so the overseer must not read them as live. The prior file is in git history at `ca298f4`.

Repo facts were grounded with narrow searches of `raices` and the module cache copy of `github.com/mercadopago/sdk-go@v1.14.0` (abbreviated `sdk/` below). Line refs are a snapshot; re-verify them before a builder edits.

Severity key: **block** means the overseer must not start until `PLAN.md` changes. **fix-in-plan** means change `PLAN.md` before the affected tier's kickoff. **note** means the overseer may proceed if the locks section records it.

Summary: 2 block, 6 fix-in-plan, 5 note.

---

## Block

### B1. The poll token has no column, and the plan says the schema needs none

- **Plan claim broken:** Scope says "Schema (unchanged from the prior revision — no new columns needed for the webhook-only rule)". The same Scope section and Decision 22 require "a random, unguessable per-order poll token minted at checkout time".
- **Evidence:** The committed `orders` table has no poll-token column (`database/migrations/000001_init.sql:11-22`). Migration 000001 is already applied to the local dev database (`data/raices.db`). `database/migrate.go:19-42` records applied migrations by version and skips any version it has already seen, so if a builder edits 000001 to add the column, the existing database silently never gets it.
- **Required plan change:**
  - Replace "unchanged … no new columns needed" with: "Add migration `000002`: `orders.poll_token` (text, not null, unique). Never edit `000001`." Add the checkout-key column from F4 to the same migration if F4 is accepted.
  - Add `poll_token` to the `orders` column list in Scope.
  - Add a Tier 1 verification step: start against an existing `000001` database, and assert after boot that the new column exists.

### B2. The SDK returns declines as errors, not as an order, so the synchronous outcome mapping cannot run as written

- **Plan claim broken:**
  - Scope: "Persist `mp_order_id` whenever the response includes one, independent of status."
  - Scope: "Map the response to exactly three synchronous outcomes."
  - Scope: "If the call never reaches Mercado Pago (network error, no response), `mp_order_id` stays null and that order's eventual expiry needs no reconciliation — nothing was charged."
  - Lock 3.
- **Evidence:**
  - For any HTTP status of 400 or higher, `sdk/pkg/internal/httpclient/gateway.go:34-48` returns `nil` plus a typed error. The raw response body is kept only as the string `ResponseError.Message` (`sdk/pkg/mperror/mp_error.go:19-29`).
  - `order.Client.Create` then returns a nil `*Response` (`sdk/pkg/order/client.go:100-112`).
  - The SDK maps 402 to `PaymentError`, 409 to `IdempotencyError`, and 422 to `ValidationError` (`sdk/pkg/mperror/typed_errors.go:64-73`). That strongly suggests a declined card arrives on the error channel with no parsed `Status` or `ID`. This is unverified until it is checked against the Orders API docs.
  - Transport errors are wrapped as "transport level error" (`gateway.go:19-21`). A timeout after the request reached Mercado Pago looks identical to one that never left. So "no response" does not mean "nothing was charged". This is the same lost-response case old B3 was about.
  - Read literally, the plan sends every decline to "everything else". That holds the seats for 15 minutes and breaks success metric 2.
- **Required plan change:** Replace the three-outcome sentence in Scope and Tier 1 with a classification of the call, not just of the response:
  1. **2xx with an order body:** map `status` / `status_detail` as planned.
  2. **4xx with an order JSON body** (expected for declines, likely 402): parse `ResponseError.Message` into the order response type, persist its `id` as `mp_order_id`, then map its status. A documented decline is a terminal failure.
  3. **4xx that Mercado Pago documents as "no order created"** (for example an invalid or consumed card token, or a validation error): terminal failure. Nothing was charged.
  4. **Transport error, context timeout, 5xx, 429, or 409:** unknown outcome. This is not a terminal failure and not "nothing charged". The buyer goes to the pending view, and the order expires normally if no webhook arrives.

  Also:
  - Delete the "nothing was charged" sentence.
  - Rewrite Lock 3 to say "persist `mp_order_id` from the success body or from a parsed error body".
  - Add to Tier 1 verification: the CI stubs must include an error-shaped decline (a typed error whose message contains an order body) and a transport timeout, alongside the four response stubs. `order.Client` is an interface (`sdk/pkg/order/client.go:36`), and `MPClient` embeds it (`payment/config.go:10-12`), so a fake client is the stubbing seam.
  - Record the actual HTTP status Mercado Pago uses for a sandbox decline in the Tier 1 kickoff.

---

## Fix-in-plan

### F1. The Attention filter drops charged orders whose synchronous response was lost

- **Plan claim broken:** Success metric 9, the Attention filter, and Decision 23 all limit expired orders to "`expired` with a non-null `mp_order_id`".
- **Evidence:** B2 shows a null `mp_order_id` can belong to a charged order. Every local order is created by `POST /register/pay` and immediately attempts a charge, so there is no expired order that never tried to charge. An expired order with a null id is exactly the unknown-outcome case: the one where the webhook is the only remaining signal. When that webhook is also lost (tunnel down, wrong URL, signature bug), the charged buyer disappears from every admin view except All orders.
- **Required plan change:**
  - Change the Attention predicate to every `expired` order, regardless of `mp_order_id`. Apply this in Scope (Objective item 5 and the `GET /admin` section), success metric 4, success metric 9, Decision 23, and Lock 13.
  - Add the local order id as a column labelled as the Mercado Pago `external_reference`, so the admin can search the Mercado Pago dashboard when the Mercado Pago id is blank.
  - Add a Tier 2/3 test: a transport-timeout order expires and appears on Attention.

### F2. Lazy expiry only runs on checkout, so after sales close nothing ever expires

- **Plan claim broken:**
  - Decision 8: "swept lazily inside the checkout transaction".
  - Success metric 4: "expires at 15 minutes".
  - The poll view's "expired" state.
  - Lock 5.
- **Evidence:** The sweep only runs inside `POST /register/pay`. After `2026-10-31T06:00:00Z` the cutoff rejects every checkout, so no sweep ever runs again. Before the cutoff, a quiet stretch has the same effect. Unswept orders stay `pending_payment`, so:
  - the poll page says "still confirming" forever;
  - the Attention filter, which selects on `expired`, never shows them.

  The moved-clock test in success metric 4 passes only because the test itself triggers a checkout.
- **Required plan change:**
  - Make the sweep a single function. Call it at the start of the checkout transaction, the poll handler, the admin view handler, and the webhook transaction.
  - Say explicitly that when the webhook sweeps first, a late success for a stale order takes the "expired, re-insert only if seats remain" branch.
  - Add a test: after the cutoff, a stale `pending_payment` order becomes `expired` through a poll request and through the admin view, with no checkout involved.

### F3. "Terminal never transitions again" contradicts the late-success branch, and the writes have no guards

- **Plan claim broken:**
  - Webhook section: "A terminal local status never transitions again."
  - The transition's second success case moves `expired` / `rejected` to `paid`.
  - The terminal-failure branch, which "delete[s] remaining `reserved` rows, set[s] the order `rejected`", has no precondition.
- **Evidence:** The plan never defines which statuses are terminal. If a builder treats `paid`, `rejected`, and `expired` all as terminal, every late success is dropped. The Orders API often sends the webhook before our synchronous `Create` call has returned, so the webhook can mark an order `paid` while the synchronous handler is still waiting. Two writes can then clobber that result:
  - a synchronous `mp_order_id` write that rewrites the whole row;
  - an unguarded failure branch.
- **Required plan change:** Add a transition table to Scope:
  - `paid` is absorbing for both branches. A failure event for a `paid` order is logged and ignored, since refunds and chargebacks are out of scope.
  - Success may move `pending_payment`, `expired`, or `rejected` to `paid`. Only the webhook may do this.
  - Failure moves `pending_payment` to `rejected`. Failure on `expired` also moves it to `rejected`, which clears the order off Attention once Mercado Pago confirms there was no charge. Failure on `rejected` is a no-op.
  - The synchronous `mp_order_id` write updates that one column only.

  Add a Tier 2 test: the webhook success lands before the synchronous handler finishes, and the order stays `paid` with one set of tickets.

### F4. Double submit is not deduplicated, and a declined card token is reused on retry

- **Plan claim broken:** The Tier 1 test "double submit uses the same idempotency key". Lock 3 says "the idempotency key [is] the local order id".
- **Evidence:**
  - Every `POST /register/pay` inserts a new order before calling Mercado Pago, so a resubmit gets a new order id and therefore a new idempotency key. The test cannot pass as specified.
  - The pay form uses `hx-swap="none"` (`templates/view/register/register.templ:206`), so the form, including the hidden `token` input (`:211`), is never re-rendered.
  - `public/js/mp-card.js:69-71` resubmits whatever token is already in that input without re-tokenizing.
  - Secure Fields card tokens are single-use. After a decline or a timeout, the buyer's retry sends a consumed token. That gives a guaranteed second failure, or, after a lost-response charge, an error page in front of a charge that will still be fulfilled by the webhook.
- **Required plan change:**
  - Add a `checkout_key` that the server mints when it renders the pay form. Store it on `orders`, unique, in migration `000002` (see B1).
  - A resubmit with an existing key returns that order's pending view, with no insert and no Mercado Pago call. A decline response mints a fresh key.
  - The idempotency key stays the local order id, which is now stable across resubmits.
  - Any Mercado Pago response or error clears the hidden token, and the next submit re-tokenizes.
  - Reword the Tier 1 test to: "a resubmit with the same checkout key creates no second order and no second Mercado Pago call; a retry after a decline uses a new card token".

### F5. The poll view has no state for two real outcomes, and its token can reach the logs

- **Plan claim broken:** The five poll states in Scope, Decision 22, and Lock 14.
- **Evidence:**
  - **Paid but unfulfilled:** a late success after the event sold out leaves the order `paid` + `unfulfilled`. The buyer was charged, but the poll page has no state for it.
  - **Stuck email:** a crash between commit and send leaves `paid` + `fulfilled` + `not_sent`, which the poll page shows as "still confirming" forever. Nothing retries the send automatically (Decision 11).
  - **Logging:** the request logger writes `r.URL.Path` (`middleware.go:55`). A poll token placed in the path lands in Cloud Logging. Lock 12 only redacts claim tokens.
- **Required plan change:**
  - Add a poll state for `paid` + `unfulfilled`: "payment received, no seats left, the organizer will refund".
  - Map `not_sent` older than about 10 minutes to the same message as email-failed.
  - Cap polling at a fixed duration after the hold expires.
  - Put the poll token in the query string, which the logger does not write, or redact it like the claim path. Add that to Lock 12.
  - State that the poll response carries no buyer name or email.

### F6. The bank-authentication step has no rendering path and may need origins Lock 7 forbids

- **Plan claim broken:** The needs-authentication bullet in Scope, Decision 21, Risk 2, and Lock 7 ("CSP allows only the Mercado Pago origins").
- **Evidence:**
  - The SDK exposes the challenge as `transaction_security.url` on the payment method in the response (`sdk/pkg/order/response.go:95-105`).
  - On the request side it is opt-in, via `TransactionSecurityRequest{Validation, LiabilityShift}` (`sdk/pkg/order/request.go:359-365`). If Tier 1 never sets it, no challenge may ever come back, and the needs-authentication branch is untestable in the sandbox.
  - The live CSP is `frame-src https://*.mercadopago.com; form-action 'self'` (`middleware.go:85`). If the challenge URL is a bank-hosted page, an iframe is blocked, and widening the CSP breaks Lock 7.
  - With `hx-swap="none"` (`register.templ:206`), the pay response currently has nowhere to render a challenge or a pending view.
- **Required plan change:** Tier 1 kickoff must record:
  1. the `transaction_security.validation` value that is sent;
  2. the origin of the challenge URL in the sandbox;
  3. how the pay response moves the buyer, either an htmx redirect to the pending view or a swap target.

  Prefer rendering the challenge inside the pending view while polling runs. Then no completion callback is needed, because the webhook resolves the order either way. If the challenge origin is not a Mercado Pago origin, the overseer needs the user's sign-off before widening `frame-src`. The alternative is to open the challenge as a top-level navigation. Builders do not choose.

---

## Note

### N1. The plan reads as greenfield, but Tier 0 and part of Tier 1 have already landed

The following already exist:

- **Tier 0:** the database package (`database/database.go`, `migrate.go`, `tx.go`, sqlc), the required-var boot checks (`env/env.go:37-64`), and graceful shutdown (`main.go:60-78`).
- **Tier 1, in part:** Secure Fields with the `MercadoPago` constructor in a self-hosted script (`public/js/mp-card.js:22-43`), a widened CSP (`middleware.go:85`), and a server-provided price attribute (`register.templ:47`).

`POST /register/pay` is not yet routed (`app/router.go:31-36`). The overseer should verify each landed item against its acceptance check rather than re-kick it. The CSP DevTools check and the "no card fields in the request body" check are still owed.

### N2. A per-order notification URL could remove the manual dashboard sync

`order.Request.Config.NotificationURL` exists (`sdk/pkg/order/request.go:26`, `:299`). Setting it to `APP_BASE_URL` + `/webhooks/mercadopago` would mean the tester only sets `APP_BASE_URL` to the tunnel URL, instead of re-registering the URL in the dashboard for each session (Risk 8, Decision 24). Before adopting this, confirm that Mercado Pago signs per-order notifications with the same webhook secret. Either way, the webhook should answer 200 for topics other than `order` and for unknown references (and log them), so Mercado Pago does not retry them for hours.

### N3. The webhook never compares the amount

The plan says not to trust the notification body for the amount, but it never says to compare the re-fetched order's `total_amount` (a decimal string, `sdk/pkg/order/request.go:13`) and currency against the local `total_cents`. The fix: on a mismatch, set the order `paid` + `unfulfilled` (which puts it on Attention) instead of issuing tickets. Also test the cents-to-decimal-string conversion (`25000` must become `250.00`).

### N4. The order of email sending and the webhook's 200 response is unspecified

The plan does not say whether the three send attempts happen before or after the webhook returns 200.

- **Before:** the backoff runs inside Mercado Pago's webhook timeout. A retried notification then hits the crash-shaped rule and correctly does not resend.
- **After:** the send goroutine must be drained by `http.Server.Shutdown`, or SIGTERM drops it.

The Tier 2 kickoff should pick one.

### N5. Uncharged expired orders never leave Attention

With F1, Attention shows every expired order. Only a later failure or success webhook moves one off (F3). Abandoned challenges and lost-response orders with no charge stay listed, because there is no dismiss action. That is acceptable at a 300-seat scale. The overseer records it rather than adding a dismiss button.

---

## Locks (non-negotiable for the overseer)

1. `PLAN.md` §8 Locks 1–16 still hold, as amended by the edits above. The 2026-10-13 red-team findings are not live instructions.
2. Schema changes go in a new numbered migration (`000002`+). Never edit `000001_init.sql`.
3. The synchronous Mercado Pago call is classified four ways (B2). An unknown outcome (transport error, timeout, 5xx, 429, 409) is never treated as a terminal failure and never as "nothing was charged".
4. `paid` is absorbing. Only the webhook moves an order to `paid`. The failure branch only acts on `pending_payment`, `expired`, or `rejected`. The synchronous `mp_order_id` write touches only that column.
5. One expiry sweep function runs in checkout, poll, admin view, and webhook.
6. Attention includes every `expired` order and shows the local order id as the `external_reference`.
7. A Mercado Pago card token is never resubmitted after any Mercado Pago response or error. A resubmit with the same checkout key never creates a second order or a second charge.
8. The poll token never appears in a logged path, and the poll response carries no buyer PII.
9. Any CSP widening beyond Mercado Pago origins (for example for a bank 3DS challenge) needs user sign-off recorded in overseer state.
