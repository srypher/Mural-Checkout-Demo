## Mural Checkout Demo – Backend + Frontend

This repo is a small end‑to‑end demo of a merchant “checkout” experience built on top of the **Mural API**:

- A **React + Vite + Tailwind** frontend in `frontend/` that lets you:
  - Browse a small product catalog.
  - Create an order as a “customer”.
  - View a simple **admin dashboard** with live order status and payout details.
- A **Go backend** that:
  - Exposes a small REST API (`/api/products`, `/api/orders`, `/api/admin/*`).
  - Stores orders in Postgres.
  - Talks to **Mural** to:
    - Detect (or assume) incoming USDC deposits.
    - Quote USDC→COP.
    - Create and execute COP payouts to a demo Colombian bank account.

The goal is to demonstrate a realistic, but sandbox‑friendly, “USDC in → COP out” merchant flow using Mural.

---

## Running locally

Prerequisites:

- Node 18+ and npm.
- Docker + Docker Compose.
- A Mural sandbox account with:
  - An API key (`MURAL_API_KEY`).
  - A transfer API key (`MURAL_TRANSFER_KEY`).

From the repo root:

1. **Install and start the frontend**

   ```bash
   cd frontend
   npm install
   npm run dev
   ```

   By default this will serve the UI on `http://localhost:5173`.

2. **Configure Mural env vars**

   In the repo root, create a `.env` file (used by the backend) with at least:

   ```bash
   MURAL_API_KEY=your-sandbox-api-key
   MURAL_TRANSFER_KEY=your-sandbox-transfer-key
   # Optional – override if you need to hit a different Mural environment
   MURAL_BASE_URL=https://api-staging.muralpay.com
   ```

3. **Start the backend + Postgres via Docker Compose**

   From the repo root:

   ```bash
   docker compose up --build
   ```

   This will:

   - Build the Go backend binary.
   - Start Postgres and initialize the `orders` table from `db/001_init.sql`.
   - Start the backend on `http://localhost:8080`.

   The backend logs will include lines like:

   - `using Mural account ... for deposits and transactions`
   - `using Mural organization ... for on-behalf-of`

   which confirm successful auto‑configuration.

4. **Use the app**

   - Open the frontend (`http://localhost:5173`).
   - Log in as:
     - `guest/guest` for a customer flow.
     - `admin/admin` for the admin dashboard.
   - Add items to cart and start checkout.
   - The backend will:
     - Create an order in Postgres (`pending_payment`).
     - Start a background **payment lifecycle** watcher for that order.

---

## How the payment + payout flow works

At a high level:

1. **Order creation**

   - Endpoint: `POST /api/orders`.
   - The backend:
     - Persists an order with:
       - `status = pending_payment`
       - `amountUsdc` based on cart.
     - Returns:
       - `orderId`
       - `amountUsdc`
       - `depositAddress` (Mural Account wallet address)
       - `network` (e.g. POLYGON).
     - Starts `simulatePaymentLifecycle(orderID, amountUSDC)` in the background.

2. **(Intended) payment**

   - The UI tells the user to **send USDC** to the provided wallet address on the given network.
   - In a production integration, this would be the actual customer transfer; in the demo, it may or may not occur.

3. **Payment detection (demo)**

   - The backend uses the **Transactions API**:
     - `POST /api/transactions/search/account/{accountId}`  
       to look for USDC **DEPOSIT** transactions into the configured Account.
   - For each order, `simulatePaymentLifecycle`:
     - Loads the order (for `createdAt` and amount).
     - Polls `SearchTransactionsForAccount` for up to **2 minutes**.
     - Logs all returned transactions for observability.
     - Marks the order as **`paid`** as soon as it sees a transaction where:
       - `tokenSymbol == "USDC"` and
       - `executedAt >= order.CreatedAt` and
       - `amount ≈ order.amountUsdc`.
     - **If no matching transaction is seen before the timeout**, for demo purposes it:
       - Logs the timeout, and
       - Still marks the order `paid` so the rest of the flow can be exercised.

4. **Quote USDC→COP**

   - Endpoint: `POST /api/payouts/fees/token-to-fiat`.
   - Method used: `QuoteTokenToFiat` in `internal/mural/client.go`.
   - The backend:
     - Requests a quote for converting the order’s USDC amount to **COP**.
     - Updates the order’s `amountCop` with the estimated COP amount.

5. **Create + execute COP payout**

   - Endpoints:
     - `POST /api/payouts/payout` – create payout request.
     - `POST /api/payouts/payout/{id}/execute` – execute payout.
   - The backend:
     - Builds a single payout to a **demo Colombian bank recipient** (hard‑coded bank + account info).
     - Calls `CreatePayoutRequest` and then `ExecutePayoutRequest` with mode `FLEXIBLE`.
     - If execution returns `EXECUTED`, it:
       - Reloads the order to get the COP estimate.
       - Marks the order `withdrawn` with that COP amount.

6. **Admin view**

   - `GET /api/admin/orders` lists all orders with their current status and COP amounts.
   - `GET /api/admin/orders/{id}/payout` fetches:
     - Stored payout metadata on the order.
     - A live payout request from the Mural Payouts API, if available.

---

## Mural APIs leveraged

The backend uses the following Mural APIs (see `internal/mural/client.go` and `mural-api-documentation-complete.md`):

- **Accounts**
  - `GET /api/accounts` – list accounts for the API key.
  - `GET /api/accounts/{id}` – used during earlier iterations; now mainly `GET /api/accounts`.
- **Organizations**
  - `POST /api/organizations/search` – discover an Organization to use for `on-behalf-of`.
- **Transactions**
  - `POST /api/transactions/search/account/{accountId}` – poll account transactions for USDC deposits.
- **Payouts**
  - `POST /api/payouts/fees/token-to-fiat` – quote USDC→COP conversion.
  - `POST /api/payouts/payout` – create a payout request.
  - `POST /api/payouts/payout/{id}/execute` – execute a payout request.
  - `GET /api/payouts/payout/{id}` – fetch payout request details for the admin view.
- **Webhooks (scaffolded, not fully used)**
  - `GET /api/webhooks` / `POST /api/webhooks` / `PATCH /api/webhooks/{id}/status` – used to create and activate a webhook for account balance activity, though the running demo still mostly relies on polling.

The **Payins** APIs are not currently driving the payment detection logic; see “Future work” below.

---

## Project structure (high level)

- `cmd/api/main.go`
  - Backend entrypoint: wires DB, Mural client, and HTTP server.
  - Auto‑discovers Mural Account + Organization at startup.
- `internal/handlers/app.go`
  - All HTTP handlers and routing (`/api/*`, `/healthz`).
  - `simulatePaymentLifecycle` background routine.
- `internal/models/order.go`
  - Order model, Postgres persistence, status transitions.
- `internal/mural/client.go`
  - Minimal, typed wrapper for Mural API endpoints used in this demo.
- `internal/storage/db.go`
  - Postgres connection pool setup.
- `db/001_init.sql`
  - `orders` table schema + basic index.
- `frontend/src/App.tsx`
  - Entire frontend app (storefront + admin) in a single React tree.

---

## Future work / notes

These are items I would add or refine with more time:

- **Polling against Transactions**
  - In testing I could not get the Transactions API to return a non‑empty list, and the faucet I tried for on‑chain payments had a **2‑hour cooldown**, which made realistic end‑to‑end testing difficult.

- **More fleshed out support for Webhooks**
  - I stood up the bones for Mural webhooks (create/list, activation, signature verification), but everything deployed is currently using **polling**, not webhook‑driven state transitions.

- **Exchange fees in order pricing**
  - Mural exposes various **fees and spreads** associated with token→fiat conversions.
  - The demo **does not** add these fees into the price the customer owes; a production integration should factor them into:
    - The quoted USDC amount the customer must send.
    - Any merchant markup / developer fee.

- **Leverage Counterparties API for payouts**
  - Today the payout recipient (demo Colombian business) is hard‑coded in the payload.
  - A more complete flow would:
    - Create **Counterparty** records via the Counterparties API.
    - Attach those to payout methods/requests for better reuse and auditability.

- **Prefer Payins API over Transactions for payment detection**
  - The current demo uses the **Transactions API** to infer when a payment has arrived (and even falls back to a timeout).
  - A more robust implementation should:
    - Use the **Payins API** (or wallet‑driven Payins for token deposits) as the canonical “deposit” signal.
    - Track Payin IDs and statuses (`COMPLETED` / `FAILED`) per order.
   - Additionally, payout requests that remain in **`PENDING`** status are treated as “complete enough” for UI purposes; the demo maps both `EXECUTED` and `PENDING` payout request statuses to the internal `withdrawn` state to match what we saw in the Mural dashboard (the “three bouncing dots” state).

- **Customer order history**
  - Right now, only the admin view can see orders.
  - It would be useful to let customers:
    - View a list of their own orders.
    - Re‑open an order to see how much they still need to pay and to which address.

- **Codebase cleanup**
  - Backend:
    - Break up `app.go` into focused handler files (e.g. `orders.go`, `admin.go`, `webhooks.go`) instead of a single large file.
  - Frontend:
    - Avoid a single `App.tsx` monolith by extracting:
      - `CatalogPage`, `CheckoutModal`, `AdminDashboard`, etc. into separate components/pages.

- **Frontend UX polish / bug fixes**
  - Cart behavior:
    - Clear cart more predictably between sessions/logins.
    - Cart badge count should reflect **total items**, not just the number of distinct products.
  - General tightening around edge cases (e.g. retry flows, error surfaces) now that the backend payment lifecycle is in place.


