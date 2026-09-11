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
* `WAFFO_STORE_ID` (optional fallback when the Admin Provider configuration has no store ID)
* `WAFFO_PRODUCT_ID_ONETIME` (optional fallback when the Admin Provider configuration has no one-time product ID)
* `WAFFO_PRODUCT_ID_SUBSCRIPTION` (optional fallback for future subscription flows)

The connector exposes only authenticated internal JSON routes:

* `GET /health`
* `POST /checkout`
* `POST /refund`
* `POST /webhook/verify`

The webhook route accepts raw text and `x-waffo-signature`, then returns a
normalized event. The Go API remains the public webhook endpoint and stores
the event evidence.

## Setup

Create the one-time product in the Waffo Dashboard (or with
`client.onetimeProducts.create`) and publish it in the target environment.
Then enter the matching Store ID and one-time Product ID in Admin Finance, or
provide them as `WAFFO_STORE_ID` and `WAFFO_PRODUCT_ID_ONETIME` deployment
fallbacks. Keep the merchant private key outside the repository and inject it
only as `WAFFO_PRIVATE_KEY` or `WAFFO_PRIVATE_KEY_BASE64`.

Run the connector from this directory with Node.js 20 or newer:

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
