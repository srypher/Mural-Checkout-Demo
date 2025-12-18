package handlers

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/asn1"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"io"
	"log"
	"math"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/srypher/mural-challenge-backend/internal/models"
	"github.com/srypher/mural-challenge-backend/internal/mural"
)

type App struct {
	orders         *models.OrderStore
	mural          *mural.Client
	depositAddress string
	network        string
	useWebhooks    bool
	webhookID      string
	webhookKeyPEM  string
}

func NewApp(orders *models.OrderStore, muralClient *mural.Client, backendBaseURL string, useWebhooks bool) *App {
	app := &App{
		orders:      orders,
		mural:       muralClient,
		useWebhooks: useWebhooks,
	}

	// Prefer the real Mural account wallet address (and derive org/account IDs) when available.
	if muralClient != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		accts, err := muralClient.GetAccounts(ctx)
		if err != nil || len(accts) == 0 {
			if err != nil {
				log.Printf("failed to list mural accounts: %v", err)
			} else {
				log.Printf("no mural accounts returned for current API key")
			}
		} else {
			// Prefer an account explicitly named "Main Account", otherwise fall back
			// to the first ACTIVE, API-enabled account.
			var chosen *mural.Account
			for i := range accts {
				if accts[i].Name == "Main Account" {
					chosen = &accts[i]
					break
				}
			}
			if chosen == nil {
				for i := range accts {
					if accts[i].IsAPIEnabled && strings.EqualFold(accts[i].Status, "ACTIVE") {
						chosen = &accts[i]
						break
					}
				}
			}
			if chosen == nil {
				chosen = &accts[0]
			}

			muralClient.SetAccountID(chosen.ID)
			log.Printf("using Mural account %s (%s) for deposits and transactions", chosen.ID, chosen.Name)

			// Derive organization ID via SearchOrganizations instead of relying on
			// account fields, since Accounts response may not include it.
			if orgs, err := muralClient.SearchOrganizations(ctx, ""); err != nil {
				log.Printf("failed to search mural organizations for on-behalf-of: %v", err)
			} else if len(orgs.Organizations) > 0 {
				org := orgs.Organizations[0]
				muralClient.SetOrganizationID(org.ID)
				log.Printf("using Mural organization %s (%s) for on-behalf-of", org.ID, org.Name)
			} else {
				log.Printf("no organizations returned from search; proceeding without on-behalf-of")
			}

			if chosen.AccountDetails != nil && chosen.AccountDetails.WalletDetails != nil {
				app.depositAddress = chosen.AccountDetails.WalletDetails.WalletAddress
				app.network = chosen.AccountDetails.WalletDetails.Blockchain
			}
		}
	}

	// Fallback to mock address / network if we couldn't resolve from Mural.
	if app.depositAddress == "" {
		addr := os.Getenv("MOCK_USDC_ADDRESS")
		if addr == "" {
			addr = "0xDEMOUSDCADDRESSONPOLYGON000000000"
		}
		app.depositAddress = addr
	}
	if app.network == "" {
		app.network = "POLYGON"
	}

	// Configure webhook if enabled and backend base URL is known.
	if app.useWebhooks && muralClient != nil && backendBaseURL != "" {
		callbackURL := strings.TrimRight(backendBaseURL, "/") + "/api/webhooks/mural"
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		webhooks, err := muralClient.ListWebhooks(ctx)
		if err != nil {
			log.Printf("failed to list Mural webhooks: %v", err)
		} else {
			var match *mural.Webhook
			for i := range webhooks {
				w := &webhooks[i]
				if w.URL == callbackURL {
					match = w
					break
				}
			}
			if match == nil {
				if len(webhooks) >= 5 {
					log.Printf("cannot create Mural webhook: already at max count")
				} else {
					created, err := muralClient.CreateWebhook(ctx, callbackURL, []string{"MURAL_ACCOUNT_BALANCE_ACTIVITY"})
					if err != nil {
						log.Printf("failed to create Mural webhook: %v", err)
					} else {
						match = created
					}
				}
			}
			if match != nil {
				if match.Status != "ACTIVE" {
					if updated, err := muralClient.UpdateWebhookStatus(ctx, match.ID, "ACTIVE"); err != nil {
						log.Printf("failed to activate Mural webhook: %v", err)
					} else {
						match = updated
					}
				}
				app.webhookID = match.ID
				// Public key is configured via env for now
				app.webhookKeyPEM = os.Getenv("MURAL_WEBHOOK_PUBLIC_KEY")
			}
		}
	}

	return app
}

func (a *App) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", a.handleHealth)
	mux.HandleFunc("POST /api/login", a.handleLogin)
	mux.HandleFunc("GET /api/products", a.handleProducts)
	mux.HandleFunc("POST /api/orders", a.handleCreateOrder)
	mux.HandleFunc("GET /api/orders/{id}", a.handleGetOrder)
	mux.HandleFunc("GET /api/admin/orders", a.requireAdmin(a.handleListOrders))
	mux.HandleFunc("GET /api/admin/mural/account", a.requireAdmin(a.handleAdminMuralAccount))
	mux.HandleFunc("GET /api/admin/orders/{id}/payout", a.requireAdmin(a.handleAdminOrderPayout))
	mux.HandleFunc("POST /api/webhooks/mural", a.handleMuralWebhook)

	return a.cors(mux)
}

func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type loginResponse struct {
	Token string `json:"token"`
	Role  string `json:"role"`
}

// simple hardcoded auth: guest/guest and admin/admin, returns a bearer token
func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req loginRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	var role, token string
	switch {
	case req.Username == "guest" && req.Password == "guest":
		role = "guest"
		token = "guest-token"
	case req.Username == "admin" && req.Password == "admin":
		role = "admin"
		token = "admin-token"
	default:
		http.Error(w, "invalid credentials", http.StatusUnauthorized)
		return
	}

	writeJSON(w, http.StatusOK, loginResponse{
		Token: token,
		Role:  role,
	})
}

type product struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	PriceUSDC   float64 `json:"priceUsdc"`
	ImageURL    string  `json:"imageUrl"`
}

func (a *App) handleProducts(w http.ResponseWriter, r *http.Request) {
	products := []product{
		{
			ID:          "starter-kit",
			Name:        "Starter Kit",
			Description: "Lightweight entry plan for small experiments.",
			PriceUSDC:   1,
			ImageURL:    "https://images.pexels.com/photos/546819/pexels-photo-546819.jpeg",
		},
		{
			ID:          "growth-bundle",
			Name:        "Growth Bundle",
			Description: "Everything you need to scale your next launch.",
			PriceUSDC:   12,
			ImageURL:    "https://images.pexels.com/photos/546819/pexels-photo-546819.jpeg",
		},
		{
			ID:          "pro-suite",
			Name:        "Pro Suite",
			Description: "Advanced toolkit for high‑volume merchants.",
			PriceUSDC:   20,
			ImageURL:    "https://images.pexels.com/photos/546819/pexels-photo-546819.jpeg",
		},
		{
			ID:          "studio-templates",
			Name:        "Studio Templates",
			Description: "Pre‑built canvases for rapid ideation.",
			PriceUSDC:   7,
			ImageURL:    "https://images.pexels.com/photos/546819/pexels-photo-546819.jpeg",
		},
		{
			ID:          "team-collab",
			Name:        "Team Collaboration",
			Description: "Unlocks real‑time team sessions.",
			PriceUSDC:   9,
			ImageURL:    "https://images.pexels.com/photos/546819/pexels-photo-546819.jpeg",
		},
		{
			ID:          "insights-pack",
			Name:        "Insights Pack",
			Description: "Analytics overlay for every mural session.",
			PriceUSDC:   11,
			ImageURL:    "https://images.pexels.com/photos/546819/pexels-photo-546819.jpeg",
		},
		{
			ID:          "webinar-pass",
			Name:        "Webinar Pass",
			Description: "Access to a live workshop series.",
			PriceUSDC:   4,
			ImageURL:    "https://images.pexels.com/photos/546819/pexels-photo-546819.jpeg",
		},
		{
			ID:          "design-library",
			Name:        "Design Library",
			Description: "Hand‑crafted components and stickers.",
			PriceUSDC:   6.5,
			ImageURL:    "https://images.pexels.com/photos/546819/pexels-photo-546819.jpeg",
		},
		{
			ID:          "ops-playbook",
			Name:        "Ops Playbook",
			Description: "Operational templates for recurring rituals.",
			PriceUSDC:   8,
			ImageURL:    "https://images.pexels.com/photos/546819/pexels-photo-546819.jpeg",
		},
		{
			ID:          "research-deck",
			Name:        "Research Deck",
			Description: "User interview and discovery toolkit.",
			PriceUSDC:   10,
			ImageURL:    "https://images.pexels.com/photos/546819/pexels-photo-546819.jpeg",
		},
		{
			ID:          "retro-kit",
			Name:        "Retro Kit",
			Description: "Facilitation assets for sprint retros.",
			PriceUSDC:   3.5,
			ImageURL:    "https://images.pexels.com/photos/546819/pexels-photo-546819.jpeg",
		},
		{
			ID:          "strategy-board",
			Name:        "Strategy Board",
			Description: "Long‑range planning frameworks bundle.",
			PriceUSDC:   14,
			ImageURL:    "https://images.pexels.com/photos/546819/pexels-photo-546819.jpeg",
		},
	}
	writeJSON(w, http.StatusOK, products)
}

type createOrderRequest struct {
	CustomerName  string             `json:"customerName"`
	CustomerEmail string             `json:"customerEmail"`
	Items         []models.OrderItem `json:"items"`
}

type createOrderResponse struct {
	OrderID        string  `json:"orderId"`
	AmountUSDC     float64 `json:"amountUsdc"`
	DepositAddress string  `json:"depositAddress"`
	Network        string  `json:"network"`
}

func (a *App) handleCreateOrder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req createOrderRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	if len(req.Items) == 0 {
		http.Error(w, "no items", http.StatusBadRequest)
		return
	}

	var total float64
	for _, it := range req.Items {
		total += it.PriceUSDC * float64(it.Quantity)
	}

	order := &models.Order{
		CustomerName:  req.CustomerName,
		CustomerEmail: req.CustomerEmail,
		Items:         req.Items,
		AmountUSDC:    total,
		Status:        models.StatusPendingPayment,
	}

	if err := a.orders.Create(r.Context(), order); err != nil {
		http.Error(w, "could not create order", http.StatusInternalServerError)
		return
	}

	// start fake payment pipeline in background
	go a.simulatePaymentLifecycle(order.ID, total)

	writeJSON(w, http.StatusCreated, createOrderResponse{
		OrderID:        order.ID.String(),
		AmountUSDC:     total,
		DepositAddress: a.depositAddress,
		Network:        a.network,
	})
}

func (a *App) handleGetOrder(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	order, err := a.orders.GetByID(r.Context(), id)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, order)
}

func (a *App) handleListOrders(w http.ResponseWriter, r *http.Request) {
	list, err := a.orders.ListAll(r.Context())
	if err != nil {
		http.Error(w, "failed to list", http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleAdminMuralAccount returns basic information about the configured Mural Account.
// This is primarily for verifying connectivity to the Mural sandbox.
func (a *App) handleAdminMuralAccount(w http.ResponseWriter, r *http.Request) {
	if a.mural == nil {
		http.Error(w, "mural client not configured", http.StatusServiceUnavailable)
		return
	}
	acct, err := a.mural.GetAccount(r.Context())
	if err != nil {
		http.Error(w, "failed to fetch mural account: "+err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, http.StatusOK, acct)
}

// handleAdminOrderPayout returns the stored payout metadata for an order plus
// a live lookup from Mural using the payout request ID, if present.
func (a *App) handleAdminOrderPayout(w http.ResponseWriter, r *http.Request) {
	if a.mural == nil {
		http.Error(w, "mural client not configured", http.StatusServiceUnavailable)
		return
	}

	idStr := r.PathValue("id")
	orderID, err := uuid.Parse(idStr)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}

	order, err := a.orders.GetByID(r.Context(), orderID)
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	var payout *mural.PayoutRequest
	if order.MuralPayoutRequestID != uuid.Nil {
		payout, err = a.mural.GetPayoutRequest(r.Context(), order.MuralPayoutRequestID.String())
		if err != nil {
			// Don't fail the whole request; just omit the live payload.
			log.Printf("failed to fetch mural payout %s for order %s: %v", order.MuralPayoutRequestID.String(), order.ID.String(), err)
		}
	}

	// If we successfully fetched a live payout, refresh stored metadata and, if needed,
	// map Mural status -> internal order status.
	if payout != nil {
		if payoutUUID, err := uuid.Parse(payout.ID); err == nil {
			if err := a.orders.UpdatePayoutMetadata(r.Context(), order.ID, payoutUUID, payout.Status); err != nil {
				log.Printf("failed to refresh payout metadata for order %s: %v", order.ID.String(), err)
			}
		}
		switch payout.Status {
		case "EXECUTED":
			// Ensure order is marked withdrawn if payout executed.
			_ = a.orders.UpdateStatus(r.Context(), order.ID, models.StatusWithdrawn, order.AmountCOP)
		case "FAILED", "CANCELED":
			// Mark order as payout_error so UI/admin can see something went wrong.
			_ = a.orders.UpdateStatus(r.Context(), order.ID, models.StatusPayoutError, order.AmountCOP)
		}
		// Reload order so response reflects refreshed fields.
		order, _ = a.orders.GetByID(r.Context(), order.ID)
	}

	resp := struct {
		Order       *models.Order        `json:"order"`
		MuralPayout *mural.PayoutRequest `json:"muralPayout,omitempty"`
	}{
		Order:       order,
		MuralPayout: payout,
	}

	writeJSON(w, http.StatusOK, resp)
}

// muralWebhookEnvelope captures just the parts of the WebhookEventRequestBody / payload
// that we care about for account balance activity.
type muralWebhookEnvelope struct {
	EventCategory string `json:"eventCategory"`
	Payload       struct {
		Type        string            `json:"type"`
		AccountID   string            `json:"accountId"`
		TokenAmount mural.TokenAmount `json:"tokenAmount"`
	} `json:"payload"`
}

// handleMuralWebhook processes Mural webhook callbacks. For this demo we only handle
// mural_account_balance_activity.account_credited to mark orders as paid when USDC
// is credited to the configured Account.
func (a *App) handleMuralWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if a.mural == nil {
		http.Error(w, "mural client not configured", http.StatusServiceUnavailable)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	// Verify webhook signature when a public key is configured.
	if a.webhookKeyPEM != "" {
		sigB64 := r.Header.Get("x-mural-webhook-signature")
		sigVersion := r.Header.Get("x-mural-webhook-signature-version")
		ts := r.Header.Get("x-mural-webhook-timestamp")

		if sigB64 == "" || sigVersion == "" || ts == "" {
			http.Error(w, "missing webhook signature headers", http.StatusUnauthorized)
			return
		}

		sigBytes, err := base64.StdEncoding.DecodeString(sigB64)
		if err != nil {
			http.Error(w, "invalid signature encoding", http.StatusUnauthorized)
			return
		}

		block, _ := pem.Decode([]byte(a.webhookKeyPEM))
		if block == nil {
			http.Error(w, "invalid webhook public key", http.StatusInternalServerError)
			return
		}
		pubAny, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			http.Error(w, "failed to parse webhook public key", http.StatusInternalServerError)
			return
		}
		pub, ok := pubAny.(*ecdsa.PublicKey)
		if !ok {
			http.Error(w, "unexpected public key type", http.StatusInternalServerError)
			return
		}

		var rs struct {
			R, S *big.Int
		}
		if _, err := asn1.Unmarshal(sigBytes, &rs); err != nil {
			http.Error(w, "invalid signature format", http.StatusUnauthorized)
			return
		}

		msg := []byte(ts + "." + string(body))
		hash := sha256.Sum256(msg)
		if !ecdsa.Verify(pub, hash[:], rs.R, rs.S) {
			http.Error(w, "invalid webhook signature", http.StatusUnauthorized)
			return
		}
	}

	var env muralWebhookEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		http.Error(w, "invalid body", http.StatusBadRequest)
		return
	}

	if env.EventCategory != "MURAL_ACCOUNT_BALANCE_ACTIVITY" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if env.Payload.Type != "account_credited" {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Only react to credits for the configured Account and USDC token.
	if env.Payload.AccountID == "" || !strings.EqualFold(env.Payload.TokenAmount.TokenSymbol, "USDC") {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Best-effort matching: mark the oldest pending_payment order whose amount is
	// less than or equal to the credited amount as paid.
	list, err := a.orders.ListAll(r.Context())
	if err != nil {
		log.Printf("failed to list orders for webhook: %v", err)
		w.WriteHeader(http.StatusNoContent)
		return
	}

	var target *models.Order
	for i := len(list) - 1; i >= 0; i-- { // newest first
		o := list[i]
		if o.Status == models.StatusPendingPayment && o.AmountUSDC <= env.Payload.TokenAmount.TokenAmount {
			target = o
			break
		}
	}

	if target == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	if err := a.orders.UpdateStatus(r.Context(), target.ID, models.StatusPaid, 0); err != nil {
		log.Printf("failed to update order %s to paid from webhook: %v", target.ID.String(), err)
	}

	w.WriteHeader(http.StatusNoContent)
}

// simulatePaymentLifecycle mocks on-chain payment detection, FX conversion and withdrawal
// for demo purposes only. We now poll the Mural Account for USDC deposits instead
// of using a fixed sleep, and use real Mural payouts for the COP conversion and withdrawal.
func (a *App) simulatePaymentLifecycle(id uuid.UUID, amountUSDC float64) {
	ctx := context.Background()

	// If Mural client is not configured (e.g. local unit tests), fall back to mock behavior.
	if a.mural == nil {
		log.Println("mural client nil, falling back to mock payout lifecycle")
		// wait for "on-chain" confirmation
		time.Sleep(8 * time.Second)
		// mark as paid (no COP yet)
		_ = a.orders.UpdateStatus(ctx, id, models.StatusPaid, 0)

		// convert to COP
		time.Sleep(5 * time.Second)
		rate := 4000.0
		cop := amountUSDC * rate
		// keep status as paid but update COP estimate
		_ = a.orders.UpdateStatus(ctx, id, models.StatusPaid, cop)
		time.Sleep(5 * time.Second)
		_ = a.orders.UpdateStatus(ctx, id, models.StatusWithdrawn, cop)
		return
	}

	// Load the order so we can filter out transactions that occurred before it was created.
	order, err := a.orders.GetByID(ctx, id)
	if err != nil {
		log.Printf("failed to load order %s before waiting for payment: %v", id.String(), err)
		return
	}

	// Poll Transactions for the configured Account and look for an incoming USDC
	// transaction whose amount matches this order's USDC total and which was
	// executed after the order was created. This is still a heuristic, but much
	// more transparent to debug and test than Payins in this sandbox.
	paymentDeadline := time.Now().Add(2 * time.Minute)
	const amountTolerance = 0.000001 // allow minor rounding differences
	log.Printf("waiting for USDC transaction for order %s amount %.6f created_at=%s", id.String(), amountUSDC, order.CreatedAt.Format(time.RFC3339))
	paymentMarked := false
	for {
		resp, err := a.mural.SearchTransactionsForAccount(ctx, 50)
		if err != nil {
			log.Printf("mural search transactions error while waiting for payment for order %s: %v", id.String(), err)
		} else {
			log.Printf("search transactions for order %s returned count=%d nextId=%v", id.String(), resp.Count, resp.NextID)
			var matched bool
			for _, tx := range resp.Transactions {
				log.Printf("considering tx id=%s direction=%s symbol=%s amount=%.6f executedAt=%s for order=%s",
					tx.ID, tx.Direction, tx.TokenAmount.TokenSymbol, tx.TokenAmount.TokenAmount, tx.ExecutedAt.Format(time.RFC3339), id.String())
				if !tx.ExecutedAt.IsZero() && tx.ExecutedAt.Before(order.CreatedAt) {
					// ignore historical transactions that predate the order
					continue
				}
				if !strings.EqualFold(tx.TokenAmount.TokenSymbol, "USDC") {
					continue
				}
				if math.Abs(tx.TokenAmount.TokenAmount-amountUSDC) <= amountTolerance {
					matched = true
					break
				}
			}
			if matched {
				log.Printf("matched incoming USDC transaction for order %s; marking as paid", id.String())
				if err := a.orders.UpdateStatus(ctx, id, models.StatusPaid, 0); err != nil {
					log.Printf("failed to update order %s to paid: %v", id.String(), err)
				}
				paymentMarked = true
				break
			}
		}
		if time.Now().After(paymentDeadline) {
			// For demo purposes, assume payment was received even if we didn't see a
			// matching on-chain transaction, so the rest of the lifecycle (quote +
			// payout) can still be exercised.
			log.Printf("timed out waiting for USDC payment for order %s; proceeding as paid for demo", id.String())
			if !paymentMarked {
				if err := a.orders.UpdateStatus(ctx, id, models.StatusPaid, 0); err != nil {
					log.Printf("failed to update order %s to paid after timeout: %v", id.String(), err)
				} else {
					paymentMarked = true
				}
			}
			break
		}
		time.Sleep(5 * time.Second)
	}

	// quote token-to-fiat to estimate COP amount via Mural.
	quoteResults, err := a.mural.QuoteTokenToFiat(ctx, amountUSDC, "USDC", "cop")
	if err != nil {
		log.Printf("mural quote error for order %s: %v", id.String(), err)
		// keep simple fallback in case of quote failure.
		rate := 4000.0
		cop := amountUSDC * rate
		// update paid status with COP estimate
		_ = a.orders.UpdateStatus(ctx, id, models.StatusPaid, cop)
	} else {
		var estimatedCOP float64
		if len(quoteResults) > 0 {
			estimatedCOP = quoteResults[0].EstimatedFiatAmount.Amount
		}
		// update paid status with COP estimate
		_ = a.orders.UpdateStatus(ctx, id, models.StatusPaid, estimatedCOP)
	}

	// build a single stubbed COP payout to a demo Colombian bank recipient.
	payoutReq := mural.CreatePayoutRequestRequest{
		SourceAccountID: a.muralAccountID(),
		Memo:            "Order " + id.String(),
		Payouts: []mural.PayoutInfoInput{
			{
				Amount: mural.TokenAmount{
					TokenAmount: amountUSDC,
					TokenSymbol: "USDC",
				},
				PayoutDetails: mural.FiatPayoutDetails{
					Type:             "fiat",
					BankName:         "Bancolombia",
					BankAccountOwner: "Demo Recipient S.A.S.",
					FiatAndRailDetails: mural.CopDetails{
						Type:              "cop",
						Symbol:            "COP",
						PhoneNumber:       "+573001234567",
						AccountType:       "CHECKING",
						BankAccountNumber: "1234567890",
						DocumentNumber:    "9001234568",
						DocumentType:      "RUC",
					},
				},
				RecipientInfo: mural.BusinessRecipientInfo{
					Type:  "business",
					Name:  "Demo Recipient S.A.S.",
					Email: "demo-recipient@example.com",
					PhysicalAddress: mural.PhysicalAddressInput{
						Address1: "Calle 123 #45-67",
						Country:  "CO",
						State:    "ANT",
						City:     "Medellín",
						Zip:      "050021",
					},
				},
			},
		},
	}

	payout, err := a.mural.CreatePayoutRequest(ctx, payoutReq)
	if err != nil {
		log.Printf("mural create payout error for order %s: %T %+v", id.String(), err, err)
		return
	}

	// persist the payout request ID and initial status on the order.
	if payout.ID != "" {
		if payoutUUID, err := uuid.Parse(payout.ID); err == nil {
			if err := a.orders.UpdatePayoutMetadata(ctx, id, payoutUUID, payout.Status); err != nil {
				log.Printf("failed to update payout metadata for order %s: %v", id.String(), err)
			}
		}
	}

	executed, err := a.mural.ExecutePayoutRequest(ctx, payout.ID, "FLEXIBLE")
	if err != nil {
		log.Printf("mural execute payout error for order %s: %T %+v", id.String(), err, err)
		return
	}

	// update stored payout status to reflect execution result.
	if executed.ID != "" {
		if payoutUUID, err := uuid.Parse(executed.ID); err == nil {
			if err := a.orders.UpdatePayoutMetadata(ctx, id, payoutUUID, executed.Status); err != nil {
				log.Printf("failed to update payout metadata after execute for order %s: %v", id.String(), err)
			}
		}
	}

	if executed.Status == "EXECUTED" {
		// we already set a COP estimate earlier; keep that as the withdrawn amount.
		order, err := a.orders.GetByID(ctx, id)
		if err != nil {
			log.Printf("failed to reload order %s after payout: %v", id.String(), err)
			return
		}
		_ = a.orders.UpdateStatus(ctx, id, models.StatusWithdrawn, order.AmountCOP)
	}
}

// muralAccountID returns the configured Mural Account ID from env as a fallback.
func (a *App) muralAccountID() string {
	if v := os.Getenv("MURAL_ACCOUNT_ID"); v != "" {
		return v
	}
	// for safety, return empty string if not set; client will error.
	return ""
}

func (a *App) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, "Bearer ") {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		token := strings.TrimPrefix(auth, "Bearer ")
		if token != "admin-token" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	}
}

func (a *App) cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if origin == "" {
			origin = "*"
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		w.Header().Set("Access-Control-Allow-Credentials", "true")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
