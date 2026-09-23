# Waffo Pancake connector

This is a private, server-side boundary for `@waffo/pancake-ts`. It must only
be reachable from the Go API and worker network. The Waffo private key is read
from the process environment and is never returned by this service.

Required environment:

* `WAFFO_MERCHANT_ID`
* `WAFFO_PRIVATE_KEY` or `WAFFO_PRIVATE_KEY_BASE64`
* `WAFFO_ENVIRONMENT` (`test` or `prod`)
* `WAFFO_CONNECTOR_TOKEN`
* `WAFFO_CONNECTOR_ADDR` (default `127.0.0.1:8091`)
* `WAFFO_STORE_ID` (required for product checkout identity; must match Admin Finance and the Go deployment)
* `WAFFO_PRODUCT_ID_ONETIME` (optional fallback when the Admin Provider configuration has no one-time product ID)
* `WAFFO_PRODUCT_ID_SUBSCRIPTION` (optional fallback for future subscription flows)
* `WAFFO_CHECKOUT_LOOKUP_QUERY` (optional, schema-reviewed read-only GraphQL query for product Checkout recovery)

The connector exposes only authenticated internal JSON routes:

* `GET /health`
* `POST /checkout/identity`
* `POST /checkout`
* `POST /checkout/lookup`
* `POST /refund`
* `POST /webhook/verify`

The webhook route accepts raw text and `x-waffo-signature`, then returns a
versioned verification proof for the exact raw-body SHA-256 and configured
environment. The Go API parses those original bytes and remains the public
webhook endpoint; it never substitutes a returned event object for the body.

Product checkout uses migration 0085 and the matching Go API release. Before
creating a product checkout, the API persists the complete semantic request and
the connector's authenticated merchant/store/environment assertion. `/checkout`
requires that original identity and rejects a changed merchant, store,
environment, SDK contract or request version before calling Waffo. This assertion
describes the connector's configured SDK identity; it is not a remote Waffo
merchant-health or settlement verification. Responses contain no credentials.

Deploy `checkout-contract.mjs`, `checkout-lookup-contract.mjs`,
`refund-contract.mjs`, `transport.mjs` and `webhook-contract.mjs` with
`server.mjs`. Changes to SDK serialization or checkout defaults require a new
request version in both the connector and Go runtime. Existing uncertain
requests must be reconciled or handled by their original serializer; do not
silently send them through a new contract.

`POST /checkout/lookup` is a read-only recovery boundary. It executes only the
deployment-supplied `WAFFO_CHECKOUT_LOOKUP_QUERY`; the query must be reviewed
against the real merchant GraphQL schema, must not contain a mutation, and must
return normalized `data.payments` rows with the aliases documented in
`checkout-lookup-contract.mjs`. The contract returns `found` only for one
complete, paid row matching the original order, buyer, store, amount and
currency, with live mode derived from the configured identity. It returns `not_found`, `ambiguous` or
`incomplete` otherwise, and never creates a new Checkout or grants rights.
An empty or invalid query remains fail-closed with
`checkout_lookup_not_configured`.

This boundary remains disabled until its contract is reviewed against the real
merchant. The connector now passes the bounded `createdAfter`/`createdBefore`
window, cursor and exact `lookupContractVersion` on every page, requires a
boolean `pageInfo.hasNextPage` with a cursor when more pages exist, and caps
the scan at 10 pages, 1,000 rows and 100 rows per page. Returned rows must
provide explicit integer `amountCents`, `createdAt` and `expiresAt`; decimal
snapshot totals are not inferred. The exact contract also binds the buyer,
store, order, currency, amount and paid status before returning `found`.
The query still needs real merchant schema, authorization, pagination,
amount/time semantics and end-to-end validation. The downstream `found` path
can enqueue payment checking and fulfillment, so an empty or unreviewed query
must remain disabled. This endpoint does not implement authenticated refund
lookup or pre-snapshot identity recovery.

Product checkout also requires the Go one-shot dispatch guard and migration
0120. The pinned SDK uses 60-second idempotency buckets for checkout creation;
repeating the same body across a minute boundary changes its remote key. The
connector does not provide persistent deduplication. After durable reservation,
an uncertain Go call must retain its original order for reconciliation instead
of calling `/checkout` again. This includes crashes before the remote request
actually started. Reuse a saved valid URL and keep processing verified late
results. Stop old API writers on cutover; do not replay queued HTTP requests at
an intermediary. See [deployment requirements](../../deploy/README.md#waffo-checkout-dispatch-0120).

Product refunds use `waffo-product-refund-v1` together with the original
checkout's authenticated `paymentIdentity`, store and buyer identity. Before
issuing a customer session or ticket, `/refund` rejects missing/old contracts,
changed identities and malformed requests. The response must independently bind
the upstream ticket's payment subject, operation ID, local payment metadata,
amount, currency, type and known status. It returns only these normalized fields;
reviewer notes and private upstream fields are not exposed. Both HTTP boundaries
reject redirects (including 307/308); SDK requests have a 20-second timeout
covering headers and body consumption. The shared transport rejects non-success HTTP responses and cancels their bodies
before SDK parsing, even if they contain success-shaped data. Outward errors
preserve 401, 403 and 429; other 4xx map to 422 and 5xx to 502 without exposing
private response bodies. Successful decoded responses are bounded to 1 MiB,
including chunked and compressed bodies; oversized declared bodies are rejected
immediately.
The incoming HTTP request scopes cancellation across all merchant and customer
SDK steps. Disconnecting stops an in-flight read and prevents a later step from
starting. Handler completion also cancels unfinished parallel SDK branches,
without cancelling other requests sharing the client. Cancellation, rate limiting,
a failed HTTP response or an oversized response does not prove a previously
dispatched financial request failed; preserve its reservation and reconcile its
outcome before further action. Ship matching `server.mjs` and `transport.mjs`;
this change introduces neither a new request contract nor a database migration.
A ticket is not proof
that funds were returned, even when its status is `succeeded`.

Migration **0107** and the matching API/worker release create a one-shot permit
only in the transaction that creates a new refund operation. The worker commits
its reservation before calling the connector, then locks and rechecks the current
operation, payment version, merchant identity and financial review gates. A lost
response, malformed result, process loss or failed local commit cannot recreate
the permit. Subsequent jobs and operator retries require reconciliation; they do
not blindly create another ticket. Old unresolved operations without a permit
also require reconciliation. A trusted financial result can still complete the
original operation. Failure confirmed by that result can permit a distinct new
refund operation under the existing refund rules.

**Cutover:** drain/stop all old API and worker processes, apply 0107, and deploy the
matching API, worker and connector files together before resuming requests. Old
workers do not honor this guard and must not run alongside the new release. Do
not backfill permits from the absence of a ticket ID, remove reservations or
reset jobs to bypass review. Rollback refuses to discard any permit evidence or remove the guard on unresolved
legacy dispatches.
The running development database is not upgraded by the offline tests.

The SDK's external refund-ticket ID is a correlation key, not a verified remote
idempotency guarantee. This protocol limits dispatch by the coordinated local
release; it cannot prevent an external proxy or provider from duplicating a
request, and it is not complete financial reconciliation. The authenticated
query boundary is implemented, but real merchant schema capture, query
authorization, pagination/completeness and end-to-end acceptance are still
required, including recovery for a reservation lost before sending. Authenticated
refund lookup and pre-snapshot merchant-identity recovery remain unimplemented. The query
must not be enabled merely because the SDK examples compile.

Migration **0108** binds product webhook deliveries to their immutable checkout
request. The API requires `waffo-webhook-v1` proof, checks the deployment
environment/digest and parses the original raw body. Signed store, customer
identity, merchant order reference, product, purpose, amount and currency must
match the original order. The merchant ID comes from the frozen original
request, not from a new remote merchant lookup. Verification credentials are not
forwarded through HTTP redirects. Signature headers are capped at 4096 bytes.

Disabling new Waffo sales, selecting another sales provider or changing the
current store/profile no longer rejects a matched historical product result.
Keep the old environment's signature verifier available; a different deployment
environment/key is not interchangeable. The global processing switch still
applies. Top-up/subscription callbacks retain their existing active-provider
routing pending a separate original-request lifecycle implementation.

Bindings commit with the minimized inbox event and job; workers recheck them
before money/rights mutations. Old unprocessed events without this evidence
require reconciliation. A freshly authenticated replay of the *identical raw
body* can add missing evidence only when its hash and minimized financial fields
match the retained event and original checkout. This does not reset a failed
job or rewrite its evidence; use existing operator recovery afterwards. Do not
manufacture receipts from today's store settings. Processed history is retained
without fabricating a past verification. Buyer exports include their own
non-secret binding fields.

**0108 cutover:** drain old API/worker processes; apply the migration and deploy
matching API, worker and all connector modules together. Old/new verifier
contracts deliberately fail closed when mixed. Rollback refuses to discard
bindings or remove the guard while legacy product events remain unresolved.
No running development database is migrated by tests. Real webhook acceptance,
Waffo query/recovery and historical transactions without original checkout
identity remain incomplete.

Run the offline guard and pinned-SDK serialization checks with
`npm test`. Serialization checks inject fetch; transport tests use a loopback
server; signature tests use ephemeral RSA keys with the pinned SDK. No remote
provider calls or production credentials are used.

## Setup

### Capture the authenticated query schema

Before implementing checkout/refund reconciliation, capture the schema available
to the actual merchant. The SDK examples are not sufficient evidence of filter
fields, nested lists or complete financial history. With `WAFFO_ENVIRONMENT`
explicitly set to `test` or `prod`, and the merchant ID/private key injected from
the deployment secret store, run from this directory:

```bash
npm run schema:inspect -- --output /private/output/waffo-schema.json
```

The parent directory must exist and the output file must not already exist.
This runs one authenticated introspection query; it does not query transactions,
customers, email addresses or execute financial actions. It retains only type,
field, argument and enum definitions, environment, SDK version and capture time.
Descriptions, default values, arbitrary extra response fields and credentials
are excluded; the file is created exclusively with mode 0600. GraphQL errors,
incomplete type references, malformed definitions or oversized responses fail
without publishing an artifact. CLI errors do not print SDK response bodies or
credentials. Keep the schema in a private working directory outside the repo.

The capture is preparatory evidence, not a payment reader or proof of refund
completion. Query authorization, pagination/completeness, original merchant/order
binding and financial semantics must still be verified before enabling recovery.

### Start the connector

Create the one-time product in the Waffo Dashboard (or with
`client.onetimeProducts.create`) and publish it in the target environment.
Set `WAFFO_STORE_ID` for the connector and Go deployment, and use the same Store
ID in Admin Finance. Set the one-time Product ID in Admin Finance or provide
`WAFFO_PRODUCT_ID_ONETIME` as its deployment fallback. Keep the merchant private key outside the repository and inject it
only as `WAFFO_PRIVATE_KEY` or `WAFFO_PRIVATE_KEY_BASE64`.

Leave `WAFFO_CHECKOUT_LOOKUP_QUERY` empty until the authenticated merchant
schema has been captured and the query has been reviewed for original order,
buyer, store, amount, currency, status and pagination semantics. The local
contract is intentionally fail-closed when it is unset.

Run the connector from this directory with Node.js 20.3 or newer
(`AbortSignal.any` is required; use a supported LTS release for deployment):

```bash
npm ci
WAFFO_ENVIRONMENT=test \
WAFFO_MERCHANT_ID=MER_xxx \
WAFFO_PRIVATE_KEY='-----BEGIN PRIVATE KEY-----\n...' \
WAFFO_CONNECTOR_TOKEN='a-long-random-token' \
WAFFO_STORE_ID=STO_xxx \
WAFFO_PRODUCT_ID_ONETIME=PROD_xxx \
npm start
```

Register the public API endpoint as an HTTP Webhook for the same environment
(`order.completed`, `subscription.activated`, `subscription.payment_succeeded`, `refund.succeeded`, and
`refund.failed`):

```ts
await client.webhooks.add({
  storeId: process.env.WAFFO_STORE_ID!,
  channel: "http",
  url: "https://your-domain.example/api/v1/payments/webhooks/waffo",
  events: ["order.completed", "subscription.activated", "subscription.payment_succeeded", "refund.succeeded", "refund.failed"],
  testMode: true,
});
```

Use `testMode: false` and `WAFFO_ENVIRONMENT=prod` only after rotating any
exposed key, approving production mode, and verifying the HTTPS endpoint.
