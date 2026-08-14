package tui

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/gshepptech/gumroad-mcp/pkg/gate"
)

// The TUI reads everything through the official CLI's `--json` output, so these
// types mirror its response shapes rather than the raw API.
//
// Numeric fields are json.Number throughout: the CLI emits sales_usd_cents as a
// float and sales_count as an integer, and a fixed numeric type would fail to
// decode one or the other.

// Product is a row on the Products tab.
type Product struct {
	ID                 string            `json:"id"`
	Name               string            `json:"name"`
	Price              int               `json:"price"`
	Currency           string            `json:"currency"`
	FormattedPrice     string            `json:"formatted_price"`
	Published          bool              `json:"published"`
	Deleted            bool              `json:"deleted"`
	SalesCount         json.Number       `json:"sales_count"`
	SalesUSDCents      json.Number       `json:"sales_usd_cents"`
	ShortURL           string            `json:"short_url"`
	LandingURL         string            `json:"landing_url"`
	CategoryLabel      string            `json:"category_label"`
	CustomSummary      string            `json:"custom_summary"`
	CustomPermalink    string            `json:"custom_permalink"`
	Tags               []string          `json:"tags"`
	IsTieredMembership bool              `json:"is_tiered_membership"`
	Recurrences        []string          `json:"recurrences"`
	FileInfo           map[string]string `json:"file_info"`
	Variants           []VariantGroup    `json:"variants"`
}

// VariantGroup is a variant category embedded in a product.
type VariantGroup struct {
	Title   string          `json:"title"`
	Options []VariantOption `json:"options"`
}

// VariantOption is one selectable option.
type VariantOption struct {
	Name            string `json:"name"`
	PriceDifference *int   `json:"price_difference"`
}

// Sale is a row on the Sales tab.
type Sale struct {
	ID          string `json:"id"`
	Email       string `json:"email"`
	CreatedAt   string `json:"created_at"`
	Daystamp    string `json:"daystamp"`
	ProductName string `json:"product_name"`
	ProductID   string `json:"product_id"`

	Price      int    `json:"price"`
	GumroadFee int    `json:"gumroad_fee"`
	TaxCents   int    `json:"tax_cents"`
	Currency   string `json:"currency"`
	Quantity   int    `json:"quantity"`

	AmountRefundable string `json:"amount_refundable_in_currency"`
	FullName         string `json:"full_name"`
	LicenseKey       string `json:"license_key"`
	Referrer         string `json:"referrer"`

	Refunded            bool              `json:"refunded"`
	PartiallyRefunded   bool              `json:"partially_refunded"`
	Disputed            bool              `json:"disputed"`
	Shipped             bool              `json:"shipped"`
	IsRecurringBilling  bool              `json:"is_recurring_billing"`
	Cancelled           bool              `json:"cancelled"`
	Ended               bool              `json:"ended"`
	VariantsAndQuantity string            `json:"variants_and_quantity"`
	CustomFields        map[string]string `json:"custom_fields"`
}

// Subscriber is a row on the Subscribers tab.
type Subscriber struct {
	ID              string  `json:"id"`
	ProductName     string  `json:"product_name"`
	UserEmail       string  `json:"user_email"`
	CreatedAt       string  `json:"created_at"`
	Recurrence      string  `json:"recurrence"`
	Status          string  `json:"status"`
	LicenseKey      string  `json:"license_key"`
	CancelledAt     *string `json:"cancelled_at"`
	EndedAt         *string `json:"ended_at"`
	FailedAt        *string `json:"failed_at"`
	FreeTrialEndsAt *string `json:"free_trial_ends_at"`
}

// Email is a row on the Emails tab.
type Email struct {
	ID              string  `json:"id"`
	Subject         string  `json:"subject"`
	Message         string  `json:"message"`
	State           string  `json:"state"`
	AudienceType    string  `json:"audience_type"`
	PublishedAt     *string `json:"published_at"`
	ScheduledAt     *string `json:"scheduled_at"`
	RecipientsCount *int    `json:"recipients_count"`
	URL             *string `json:"url"`
}

// Payout is a row on the Payouts tab.
type Payout struct {
	ID                *string  `json:"id"`
	Amount            string   `json:"amount"`
	Currency          string   `json:"currency"`
	Status            string   `json:"status"`
	CreatedAt         string   `json:"created_at"`
	ProcessedAt       *string  `json:"processed_at"`
	PaymentProcessor  string   `json:"payment_processor"`
	PaypalEmail       *string  `json:"paypal_email"`
	BankAccountVisual *string  `json:"bank_account_visual"`
	Sales             []string `json:"sales"`
	RefundedSales     []string `json:"refunded_sales"`
	DisputedSales     []string `json:"disputed_sales"`
}

// Account is the signed-in seller, for the header.
type Account struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	URL         string `json:"url"`
}

// --- fetchers ----------------------------------------------------------------

// fetch runs a CLI command and decodes the value under key.
func fetch[T any](ctx context.Context, r *gate.Runner, key string, args ...string) ([]T, error) {
	raw, err := r.Run(ctx, args...)
	if err != nil {
		return nil, err
	}
	var env map[string]json.RawMessage
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("decode %s: %w", key, err)
	}
	body, ok := env[key]
	if !ok {
		return nil, nil // an empty collection is omitted rather than sent as []
	}
	var items []T
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, fmt.Errorf("decode %s: %w", key, err)
	}
	return items, nil
}

func fetchAccount(ctx context.Context, r *gate.Runner) (*Account, error) {
	raw, err := r.Run(ctx, "auth", "status")
	if err != nil {
		return nil, err
	}
	var env struct {
		Authenticated bool    `json:"authenticated"`
		User          Account `json:"user"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	if !env.Authenticated {
		return nil, fmt.Errorf("the Gumroad CLI is not logged in — run `gumroad auth login`")
	}
	return &env.User, nil
}

func fetchProducts(ctx context.Context, r *gate.Runner) ([]Product, error) {
	return fetch[Product](ctx, r, "products", "products", "list")
}

func fetchSales(ctx context.Context, r *gate.Runner) ([]Sale, error) {
	return fetch[Sale](ctx, r, "sales", "sales", "list")
}

func fetchSubscribers(ctx context.Context, r *gate.Runner, productID string) ([]Subscriber, error) {
	return fetch[Subscriber](ctx, r, "subscribers", "subscribers", "list", "--product", productID)
}

func fetchEmails(ctx context.Context, r *gate.Runner) ([]Email, error) {
	return fetch[Email](ctx, r, "emails", "emails", "list")
}

func fetchPayouts(ctx context.Context, r *gate.Runner) ([]Payout, error) {
	return fetch[Payout](ctx, r, "payouts", "payouts", "list")
}
