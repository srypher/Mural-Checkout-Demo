CREATE EXTENSION IF NOT EXISTS "pgcrypto";

CREATE TABLE IF NOT EXISTS orders (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    customer_name TEXT NOT NULL,
    customer_email TEXT,
    items JSONB NOT NULL,
    amount_usdc NUMERIC(18,6) NOT NULL,
    amount_cop NUMERIC(18,2),
    status TEXT NOT NULL,
    mural_payout_request_id UUID,
    mural_payout_status TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_orders_status ON orders(status);


