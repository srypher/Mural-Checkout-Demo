package mural

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"time"
)

// Config holds configuration for the Mural API client.
type Config struct {
	BaseURL        string
	APIKey         string
	TransferKey    string
	OrganizationID string
	AccountID      string
}

// Client is a minimal typed client for the Mural API tailored to this app.
type Client struct {
	httpClient *http.Client
	baseURL    *url.URL

	apiKey      string
	transferKey string

	organizationID string
	accountID      string
}

// NewClient constructs a new Mural API client.
func NewClient(cfg Config) (*Client, error) {
	if cfg.APIKey == "" {
		return nil, fmt.Errorf("mural api key is required")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = "https://api-staging.muralpay.com"
	}

	u, err := url.Parse(cfg.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid mural base url: %w", err)
	}

	return &Client{
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
		baseURL:        u,
		apiKey:         cfg.APIKey,
		transferKey:    cfg.TransferKey,
		organizationID: cfg.OrganizationID,
		accountID:      cfg.AccountID,
	}, nil
}

// SetOrganizationID allows callers (e.g. after fetching an Account) to
// dynamically configure the on-behalf-of Organization without requiring an
// environment variable.
func (c *Client) SetOrganizationID(orgID string) {
	c.organizationID = orgID
}

// SetAccountID allows callers to dynamically configure which Account to use
// for account-scoped operations like transactions search.
func (c *Client) SetAccountID(accountID string) {
	c.accountID = accountID
}

// ServiceError represents a MuralServiceException response.
type ServiceError struct {
	ErrorInstanceID string                 `json:"errorInstanceId"`
	Name            string                 `json:"name"`
	Message         string                 `json:"message"`
	Params          map[string]interface{} `json:"params"`

	StatusCode int `json:"-"`
}

func (e *ServiceError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("mural error %s (%s): %s params=%v", e.Name, e.ErrorInstanceID, e.Message, e.Params)
}

// Account represents a subset of the Mural Account schema we care about.
type Account struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Description    string          `json:"description,omitempty"`
	IsAPIEnabled   bool            `json:"isApiEnabled"`
	Status         string          `json:"status"`
	AccountDetails *AccountDetails `json:"accountDetails,omitempty"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
	// OrganizationID identifies the owning Organization for this Account.
	OrganizationID string `json:"organizationId,omitempty"`
}

// GetAccounts fetches all Accounts visible to the current API key / organization.
func (c *Client) GetAccounts(ctx context.Context) ([]Account, error) {
	var out []Account
	if err := c.do(ctx, http.MethodGet, "/api/accounts", nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

type AccountDetails struct {
	Balances      []TokenBalance `json:"balances,omitempty"`
	WalletDetails *WalletDetails `json:"walletDetails,omitempty"`
}

type TokenBalance struct {
	TokenAmount float64 `json:"tokenAmount"`
	TokenSymbol string  `json:"tokenSymbol"`
}

type WalletDetails struct {
	WalletAddress string `json:"walletAddress"`
	Blockchain    string `json:"blockchain"`
}

// TokenAmount is reused across several endpoints (amount + symbol).
type TokenAmount struct {
	TokenAmount float64 `json:"tokenAmount"`
	TokenSymbol string  `json:"tokenSymbol"`
}

// Transaction represents a subset of the Transaction schema returned by the
// Search Transactions API for an Account.
type Transaction struct {
	ID string `json:"id"`
	// Memo is not currently used for matching but included for potential debugging.
	Memo string `json:"memo,omitempty"`
	// Direction is typically "DEPOSIT" or "PAYOUT".
	Direction string `json:"direction,omitempty"`
	// ExecutedAt is the time the transaction was executed/settled on-chain.
	ExecutedAt time.Time `json:"executedAt,omitempty"`
	// We only capture the token amount/symbol we care about; other fields are omitted.
	TokenAmount TokenAmount `json:"tokenAmount,omitempty"`
}

// SearchTransactionsForAccountResponse is the envelope returned by
// POST /api/transactions/search/account/{accountId}.
type SearchTransactionsForAccountResponse struct {
	Count        int           `json:"count"`
	NextID       *string       `json:"nextId"`
	Transactions []Transaction `json:"transactions"`
}

// Payin represents a subset of the Payin schema (deposits into an Account).
type Payin struct {
	ID                   string    `json:"id"`
	DestinationAccountID string    `json:"destinationAccountId"`
	Status               string    `json:"payinStatus"`
	CreatedAt            time.Time `json:"createdAt"`
	UpdatedAt            time.Time `json:"updatedAt"`
}

// SearchPayinsResponse is returned by POST /api/payins/search.
type SearchPayinsResponse struct {
	Count  int     `json:"count"`
	NextID *string `json:"nextId"`
	Payins []Payin `json:"payins"`
}

// Organization represents a subset of the Organization schema.
type Organization struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// SearchOrganizationsResponse is returned by POST /api/organizations/search.
type SearchOrganizationsResponse struct {
	Count         int            `json:"count"`
	NextID        *string        `json:"nextId"`
	Organizations []Organization `json:"organizations"`
}

// TokenToFiatQuoteRequest is a minimal request for /api/payouts/fees/token-to-fiat.
type TokenToFiatQuoteRequest struct {
	TokenFeeRequests []struct {
		Amount struct {
			TokenAmount float64 `json:"tokenAmount"`
			TokenSymbol string  `json:"tokenSymbol"`
		} `json:"amount"`
		FiatAndRailCode string `json:"fiatAndRailCode"`
	} `json:"tokenFeeRequests"`
}

type TokenToFiatQuoteResult struct {
	EstimatedFiatAmount struct {
		Amount       float64 `json:"amount"`
		CurrencyCode string  `json:"currencyCode"`
	} `json:"estimatedFiatAmount"`
}

// QuoteTokenToFiat calls the token-to-fiat fees endpoint and returns the result list.
func (c *Client) QuoteTokenToFiat(ctx context.Context, tokenAmount float64, tokenSymbol, fiatAndRail string) ([]TokenToFiatQuoteResult, error) {
	req := TokenToFiatQuoteRequest{
		TokenFeeRequests: []struct {
			Amount struct {
				TokenAmount float64 `json:"tokenAmount"`
				TokenSymbol string  `json:"tokenSymbol"`
			} `json:"amount"`
			FiatAndRailCode string `json:"fiatAndRailCode"`
		}{
			{
				Amount: struct {
					TokenAmount float64 `json:"tokenAmount"`
					TokenSymbol string  `json:"tokenSymbol"`
				}{
					TokenAmount: tokenAmount,
					TokenSymbol: tokenSymbol,
				},
				FiatAndRailCode: fiatAndRail,
			},
		},
	}

	var out []TokenToFiatQuoteResult
	if err := c.do(ctx, http.MethodPost, "/api/payouts/fees/token-to-fiat", nil, req, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// SearchTransactionsForAccount searches Transactions for the configured Account.
// For this demo we rely on the default result ordering and do client-side filtering.
func (c *Client) SearchTransactionsForAccount(ctx context.Context, limit int) (*SearchTransactionsForAccountResponse, error) {
	// The OpenAPI spec defines limit/nextId as query parameters; for simplicity
	// we rely on default server-side pagination here and just request the first page.
	body := map[string]any{}
	var out SearchTransactionsForAccountResponse
	if err := c.do(ctx, http.MethodPost, "/api/transactions/search/account/"+c.accountID, nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SearchPayins searches Payins associated with the current Organization.
// For this demo we request the first page and perform client-side filtering.
func (c *Client) SearchPayins(ctx context.Context, limit int) (*SearchPayinsResponse, error) {
	body := map[string]any{} // no server-side filters for now
	var out SearchPayinsResponse
	if err := c.do(ctx, http.MethodPost, "/api/payins/search", nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// SearchOrganizations searches Organizations visible to the current API key.
// For this demo we request the first page and pick the first organization.
func (c *Client) SearchOrganizations(ctx context.Context, nameFilter string) (*SearchOrganizationsResponse, error) {
	body := map[string]any{}
	if nameFilter != "" {
		body["name"] = nameFilter
	}
	var out SearchOrganizationsResponse
	if err := c.do(ctx, http.MethodPost, "/api/organizations/search", nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// CreatePayoutRequestRequest models NewPayoutRequestInput for a simple inline COP fiat payout.
type CreatePayoutRequestRequest struct {
	SourceAccountID string            `json:"sourceAccountId"`
	Memo            string            `json:"memo,omitempty"`
	Payouts         []PayoutInfoInput `json:"payouts"`
}

// PayoutInfoInput corresponds to the PayoutInfoInput schema for inline fiat payouts.
type PayoutInfoInput struct {
	Amount        TokenAmount           `json:"amount"`
	PayoutDetails FiatPayoutDetails     `json:"payoutDetails"`
	RecipientInfo BusinessRecipientInfo `json:"recipientInfo"`
}

// FiatPayoutDetails corresponds to the FiatPayoutDetails schema, specialized to CopDetails.
type FiatPayoutDetails struct {
	Type               string     `json:"type"` // "fiat"
	BankName           string     `json:"bankName"`
	BankAccountOwner   string     `json:"bankAccountOwner"`
	FiatAndRailDetails CopDetails `json:"fiatAndRailDetails"`
}

// CopDetails corresponds to the CopDetails schema.
type CopDetails struct {
	Type              string `json:"type"` // "cop"
	Symbol            string `json:"symbol"`
	PhoneNumber       string `json:"phoneNumber"`
	AccountType       string `json:"accountType"` // "CHECKING" or "SAVINGS"
	BankAccountNumber string `json:"bankAccountNumber"`
	DocumentNumber    string `json:"documentNumber"`
	DocumentType      string `json:"documentType"`
}

// BusinessRecipientInfo corresponds to BusinessRecipientInfo schema.
type BusinessRecipientInfo struct {
	Type            string               `json:"type"` // "business"
	Name            string               `json:"name"`
	Email           string               `json:"email"`
	PhysicalAddress PhysicalAddressInput `json:"physicalAddress"`
}

// PhysicalAddressInput corresponds to PhysicalAddressInput schema.
type PhysicalAddressInput struct {
	Address1 string `json:"address1"`
	Address2 string `json:"address2,omitempty"`
	Country  string `json:"country"`
	State    string `json:"state"`
	City     string `json:"city"`
	Zip      string `json:"zip"`
}

// CreatePayoutRequestResponse is a subset of the payout creation response.
type CreatePayoutRequestResponse struct {
	ID              string    `json:"id"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
	SourceAccountID string    `json:"sourceAccountId"`
}

// CreatePayoutRequest creates a payout request.
func (c *Client) CreatePayoutRequest(ctx context.Context, req CreatePayoutRequestRequest) (*CreatePayoutRequestResponse, error) {
	var out CreatePayoutRequestResponse
	if err := c.do(ctx, http.MethodPost, "/api/payouts/payout", nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ExecutePayoutRequest executes an existing payout request by ID.
func (c *Client) ExecutePayoutRequest(ctx context.Context, payoutRequestID string, exchangeRateMode string) (*CreatePayoutRequestResponse, error) {
	if c.transferKey == "" {
		return nil, fmt.Errorf("mural transfer key is required to execute payouts")
	}
	headers := map[string]string{
		"transfer-api-key": c.transferKey,
	}
	body := map[string]string{}
	if exchangeRateMode != "" {
		body["exchangeRateToleranceMode"] = exchangeRateMode
	}
	var out CreatePayoutRequestResponse
	if err := c.do(ctx, http.MethodPost, "/api/payouts/payout/"+payoutRequestID+"/execute", headers, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// PayoutRequest represents a subset of the PayoutRequest schema.
type PayoutRequest struct {
	ID              string    `json:"id"`
	Status          string    `json:"status"`
	SourceAccountID string    `json:"sourceAccountId"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

// GetPayoutRequest fetches a payout request by ID.
func (c *Client) GetPayoutRequest(ctx context.Context, payoutRequestID string) (*PayoutRequest, error) {
	var out PayoutRequest
	if err := c.do(ctx, http.MethodGet, "/api/payouts/payout/"+payoutRequestID, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetAccount fetches the configured Account by ID.
func (c *Client) GetAccount(ctx context.Context) (*Account, error) {
	var out Account
	if err := c.do(ctx, http.MethodGet, "/api/accounts/"+c.accountID, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// Webhook represents a Mural webhook configuration.
type Webhook struct {
	ID     string   `json:"id"`
	URL    string   `json:"url"`
	Status string   `json:"status"`
	Events []string `json:"events"`
}

// CreateWebhookRequest is the body for POST /api/webhooks.
type CreateWebhookRequest struct {
	URL    string   `json:"url"`
	Events []string `json:"events"`
}

// UpdateWebhookStatusRequest is the body for PATCH /api/webhooks/{id}/status.
type UpdateWebhookStatusRequest struct {
	Status string `json:"status"`
}

// WebhookInfo describes the webhook we manage for this app.
type WebhookInfo struct {
	ID        string
	PublicKey string
}

// ListWebhooks returns all webhooks configured for the organization.
func (c *Client) ListWebhooks(ctx context.Context) ([]Webhook, error) {
	var out []Webhook
	if err := c.do(ctx, http.MethodGet, "/api/webhooks", nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateWebhook creates a new webhook with the given callback URL and events.
func (c *Client) CreateWebhook(ctx context.Context, callbackURL string, events []string) (*Webhook, error) {
	body := CreateWebhookRequest{
		URL:    callbackURL,
		Events: events,
	}
	var out Webhook
	if err := c.do(ctx, http.MethodPost, "/api/webhooks", nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateWebhookStatus sets the status (e.g. ACTIVE/DISABLED) for a webhook.
func (c *Client) UpdateWebhookStatus(ctx context.Context, id string, status string) (*Webhook, error) {
	body := UpdateWebhookStatusRequest{Status: status}
	var out Webhook
	if err := c.do(ctx, http.MethodPatch, "/api/webhooks/"+id+"/status", nil, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteWebhook deletes a webhook by ID.
func (c *Client) DeleteWebhook(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/webhooks/"+id, nil, nil, nil)
}

// do is a small HTTP helper that encodes body as JSON and decodes JSON responses.
func (c *Client) do(ctx context.Context, method, p string, headers map[string]string, body any, out any) error {
	u := *c.baseURL
	u.Path = path.Join(u.Path, p)

	var buf io.ReadWriter
	if body != nil {
		buf = &bytes.Buffer{}
		if err := json.NewEncoder(buf).Encode(body); err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), buf)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// Attach on-behalf-of by default when we have an org ID.
	if c.organizationID != "" {
		req.Header.Set("on-behalf-of", c.organizationID)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	// Handle non-2xx as potential MuralServiceException.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var svcErr ServiceError
		data, readErr := io.ReadAll(resp.Body)
		if readErr == nil && len(data) > 0 {
			_ = json.Unmarshal(data, &svcErr)
		}
		if svcErr.Name != "" {
			svcErr.StatusCode = resp.StatusCode
			return &svcErr
		}
		if len(data) == 0 {
			return fmt.Errorf("mural http %d with empty body", resp.StatusCode)
		}
		return fmt.Errorf("mural http %d: %s", resp.StatusCode, string(data))
	}

	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
