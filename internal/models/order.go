package models

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type OrderStatus string

const (
	StatusPendingPayment OrderStatus = "pending_payment"
	StatusPaid           OrderStatus = "paid"
	StatusWithdrawn      OrderStatus = "withdrawn"
	StatusPayoutError    OrderStatus = "payout_error"
)

type OrderItem struct {
	ProductID string  `json:"productId"`
	Name      string  `json:"name"`
	PriceUSDC float64 `json:"priceUsdc"`
	Quantity  int     `json:"quantity"`
}

type Order struct {
	ID                   uuid.UUID   `json:"id"`
	CustomerName         string      `json:"customerName"`
	CustomerEmail        string      `json:"customerEmail,omitempty"`
	Items                []OrderItem `json:"items"`
	AmountUSDC           float64     `json:"amountUsdc"`
	AmountCOP            float64     `json:"amountCop"`
	Status               OrderStatus `json:"status"`
	MuralPayoutRequestID uuid.UUID   `json:"muralPayoutRequestId,omitempty"`
	MuralPayoutStatus    string      `json:"muralPayoutStatus,omitempty"`
	CreatedAt            time.Time   `json:"createdAt"`
	UpdatedAt            time.Time   `json:"updatedAt"`
}

type OrderStore struct {
	pool *pgxpool.Pool
}

func NewOrderStore(pool *pgxpool.Pool) *OrderStore {
	return &OrderStore{pool: pool}
}

func (s *OrderStore) Create(ctx context.Context, o *Order) error {
	if o.ID == uuid.Nil {
		o.ID = uuid.New()
	}
	itemsJSON, err := json.Marshal(o.Items)
	if err != nil {
		return err
	}

	return s.pool.QueryRow(ctx, `
		INSERT INTO orders (id, customer_name, customer_email, items, amount_usdc, amount_cop, status)
		VALUES ($1,$2,$3,$4,$5,$6,$7)
		RETURNING created_at, updated_at
	`, o.ID, o.CustomerName, o.CustomerEmail, itemsJSON, o.AmountUSDC, o.AmountCOP, string(o.Status)).
		Scan(&o.CreatedAt, &o.UpdatedAt)
}

func (s *OrderStore) GetByID(ctx context.Context, id uuid.UUID) (*Order, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, customer_name, customer_email, items, amount_usdc, amount_cop, status,
		       mural_payout_request_id, mural_payout_status,
		       created_at, updated_at
		FROM orders WHERE id=$1
	`, id)
	return scanOrder(row)
}

func (s *OrderStore) ListAll(ctx context.Context) ([]*Order, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, customer_name, customer_email, items, amount_usdc, amount_cop, status,
		       mural_payout_request_id, mural_payout_status,
		       created_at, updated_at
		FROM orders ORDER BY created_at DESC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*Order
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *OrderStore) UpdateStatus(ctx context.Context, id uuid.UUID, status OrderStatus, amountCOP float64) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE orders
		SET status=$2,
		    amount_cop=COALESCE($3, amount_cop),
		    updated_at=NOW()
		WHERE id=$1
	`, id, string(status), amountCOP)
	return err
}

func scanOrder(row pgx.Row) (*Order, error) {
	var (
		o            Order
		itemsRaw     []byte
		status       string
		payoutID     *uuid.UUID
		payoutStatus *string
	)
	if err := row.Scan(
		&o.ID,
		&o.CustomerName,
		&o.CustomerEmail,
		&itemsRaw,
		&o.AmountUSDC,
		&o.AmountCOP,
		&status,
		&payoutID,
		&payoutStatus,
		&o.CreatedAt,
		&o.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(itemsRaw, &o.Items); err != nil {
		return nil, err
	}
	o.Status = OrderStatus(status)
	if payoutID != nil {
		o.MuralPayoutRequestID = *payoutID
	}
	if payoutStatus != nil {
		o.MuralPayoutStatus = *payoutStatus
	}
	return &o, nil
}

// UpdatePayoutMetadata stores the Mural payout request ID and status for an order.
func (s *OrderStore) UpdatePayoutMetadata(ctx context.Context, id uuid.UUID, payoutRequestID uuid.UUID, payoutStatus string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE orders
		SET mural_payout_request_id=$2,
		    mural_payout_status=$3,
		    updated_at=NOW()
		WHERE id=$1
	`, id, payoutRequestID, payoutStatus)
	return err
}
