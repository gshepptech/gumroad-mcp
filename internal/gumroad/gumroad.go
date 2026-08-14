// Package gumroad is a minimal Gumroad API client covering only the endpoints
// the official `gumroad` CLI does not.
//
// Everything the CLI can do, gloop drives through the CLI — see internal/gum.
// This package exists for the handful of endpoints with no command: product
// reviews, Tax Center forms and earnings, and the unauthenticated public
// product and profile payloads.
//
// It authenticates with the token the official CLI already resolved, so gloop
// never stores a credential of its own.
package gumroad

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the v2 API root.
const DefaultBaseURL = "https://api.gumroad.com/v2"

// Client talks to the Gumroad API on behalf of one access token.
type Client struct {
	Token     string
	BaseURL   string
	HTTP      *http.Client
	UserAgent string
	// RetryBase is the first backoff delay; each retry doubles it. Zero means
	// DefaultRetryBase. Tests set it small so they do not sleep for seconds.
	RetryBase time.Duration
}

// New returns a Client for the given access token, which callers get from
// gate.Runner.Token.
func New(token string) *Client {
	return &Client{
		Token:     token,
		BaseURL:   DefaultBaseURL,
		HTTP:      &http.Client{Timeout: 30 * time.Second},
		UserAgent: "gloop",
	}
}

// APIError is a non-2xx response, or a 2xx carrying success:false.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("gumroad api: %d: %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("gumroad api: %d", e.StatusCode)
}

// Retryable reports whether the request is worth sending again unchanged.
func (e *APIError) Retryable() bool {
	return e.StatusCode == 429 || e.StatusCode >= 500
}

const (
	// maxAttempts is the initial try plus three retries.
	maxAttempts = 4
	// DefaultRetryBase gives 1s, 2s, 4s — enough for a per-minute limiter to refill.
	DefaultRetryBase = time.Second
)

// get issues an authenticated GET and decodes the response.
func (c *Client) get(ctx context.Context, path string, params url.Values, out any) error {
	u := c.BaseURL + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	return c.retry(ctx, func() error { return c.once(ctx, u, true, out) })
}

// retry runs do until it succeeds, fails unretryably, or runs out of attempts.
//
// Reviews are the one thing this client paginates, and a large catalogue will
// meet the rate limiter, so a bare failure is not good enough.
func (c *Client) retry(ctx context.Context, do func() error) error {
	var lastErr error
	for attempt := range maxAttempts {
		if attempt > 0 {
			base := c.RetryBase
			if base <= 0 {
				base = DefaultRetryBase
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(1<<(attempt-1)) * base):
			}
		}

		err := do()
		if err == nil {
			return nil
		}
		lastErr = err

		var apiErr *APIError
		if !asAPIError(err, &apiErr) || !apiErr.Retryable() {
			return err
		}
	}
	return lastErr
}

func (c *Client) once(ctx context.Context, u string, auth bool, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.UserAgent)
	// The public endpoints live on the seller's own subdomain and must carry
	// no credential.
	if auth && c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeError(resp.StatusCode, b)
	}

	// A 200 can still carry success:false.
	var env struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	if json.Unmarshal(b, &env) == nil && !env.Success && env.Message != "" {
		return &APIError{StatusCode: resp.StatusCode, Message: env.Message}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(b, out)
}

func decodeError(status int, b []byte) error {
	var env struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(b, &env)
	return &APIError{StatusCode: status, Message: env.Message}
}

// Unwrap lets errors.As find an APIError through any wrapping. The retry loop
// depends on this: a bare type assertion stops matching the moment a caller
// wraps the error, which silently disables retries.
func asAPIError(err error, target **APIError) bool {
	return errors.As(err, target)
}

// --- reviews ----------------------------------------------------------------

// Review is a publicly visible product review.
type Review struct {
	ID         string          `json:"id"`
	Rating     int             `json:"rating"`
	Message    string          `json:"message"`
	CreatedAt  string          `json:"created_at"`
	PurchaseID string          `json:"purchase_id"`
	RaterName  string          `json:"rater_name"`
	Response   *ReviewResponse `json:"response"`
}

// ReviewResponse is the seller's reply to a review.
type ReviewResponse struct {
	Message   string `json:"message"`
	CreatedAt string `json:"created_at"`
}

// ListReviews returns one page of a product's public reviews, newest first,
// plus the key for the next page. Star-only ratings with no written message are
// not returned, though they do count towards the product's average.
func (c *Client) ListReviews(ctx context.Context, productID, pageKey string) ([]Review, string, error) {
	params := url.Values{}
	if pageKey != "" {
		params.Set("page_key", pageKey)
	}
	var r struct {
		ProductReviews []Review `json:"product_reviews"`
		NextPageKey    string   `json:"next_page_key"`
	}
	path := "/products/" + url.PathEscape(productID) + "/reviews"
	if err := c.get(ctx, path, params, &r); err != nil {
		return nil, "", err
	}
	return r.ProductReviews, r.NextPageKey, nil
}

// --- tax centre -------------------------------------------------------------

// TaxForm is a generated 1099.
type TaxForm struct {
	TaxYear     int     `json:"tax_year"`
	TaxFormType string  `json:"tax_form_type"`
	FiledAt     *string `json:"filed_at"`
}

// ListTaxForms returns generated 1099s. Pass year 0 for every available year.
// US sellers with the tax centre enabled only; needs the view_tax_data scope.
func (c *Client) ListTaxForms(ctx context.Context, year int) ([]TaxForm, error) {
	params := url.Values{}
	if year > 0 {
		params.Set("year", strconv.Itoa(year))
	}
	var r struct {
		TaxForms []TaxForm `json:"tax_forms"`
	}
	if err := c.get(ctx, "/tax_forms", params, &r); err != nil {
		return nil, err
	}
	return r.TaxForms, nil
}

// Earnings is the annual breakdown shown in the Tax Center.
type Earnings struct {
	Year                 int    `json:"year"`
	Currency             string `json:"currency"`
	GrossCents           int64  `json:"gross_cents"`
	FeesCents            int64  `json:"fees_cents"`
	TaxesCents           int64  `json:"taxes_cents"`
	AffiliateCreditCents int64  `json:"affiliate_credit_cents"`
	NetCents             int64  `json:"net_cents"`
}

// GetEarnings returns one year's earnings. Fully refunded sales are excluded
// from every aggregate.
func (c *Client) GetEarnings(ctx context.Context, year int) (*Earnings, error) {
	var e Earnings
	params := url.Values{"year": {strconv.Itoa(year)}}
	if err := c.get(ctx, "/earnings", params, &e); err != nil {
		return nil, err
	}
	return &e, nil
}

// --- public, unauthenticated endpoints --------------------------------------

// PublicProduct is the display payload behind any product page.
type PublicProduct struct {
	APIVersion       int             `json:"api_version"`
	ID               string          `json:"id"`
	Permalink        string          `json:"permalink"`
	Name             string          `json:"name"`
	NativeType       string          `json:"native_type"`
	URL              string          `json:"url"`
	ThumbnailURL     string          `json:"thumbnail_url"`
	CreatedAt        string          `json:"created_at"`
	UpdatedAt        string          `json:"updated_at"`
	Seller           json.RawMessage `json:"seller"`
	PriceCents       int             `json:"price_cents"`
	CurrencyCode     string          `json:"currency_code"`
	PriceFormatted   string          `json:"price_formatted"`
	IsPayWhatYouWant bool            `json:"is_pay_what_you_want"`
	DescriptionHTML  string          `json:"description_html"`
	Summary          string          `json:"summary"`
	Covers           json.RawMessage `json:"covers"`
	Attributes       json.RawMessage `json:"attributes"`
	Ratings          json.RawMessage `json:"ratings"`
	SalesCount       *int            `json:"sales_count"`
	Options          json.RawMessage `json:"options"`
	IsPublished      bool            `json:"is_published"`
	IsPhysical       bool            `json:"is_physical"`
	RefundPolicy     json.RawMessage `json:"refund_policy"`
}

// PublicProfile is the display payload behind any creator profile.
type PublicProfile struct {
	APIVersion    int             `json:"api_version"`
	ID            string          `json:"id"`
	Username      string          `json:"username"`
	Name          string          `json:"name"`
	Bio           string          `json:"bio"`
	AvatarURL     string          `json:"avatar_url"`
	ProfileURL    string          `json:"profile_url"`
	Subdomain     string          `json:"subdomain"`
	TwitterHandle string          `json:"twitter_handle"`
	IsVerified    bool            `json:"is_verified"`
	Products      json.RawMessage `json:"products"`
}

// GetPublicProduct fetches any seller's public product data. seller is a bare
// username ("sahil") or a full host ("sahil.gumroad.com").
func (c *Client) GetPublicProduct(ctx context.Context, seller, permalink string) (*PublicProduct, error) {
	var out PublicProduct
	u := fmt.Sprintf("https://%s/l/%s.json", sellerHost(seller), url.PathEscape(permalink))
	if err := c.getPublic(ctx, u, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetPublicProfile fetches any creator's public profile.
func (c *Client) GetPublicProfile(ctx context.Context, seller string) (*PublicProfile, error) {
	var out PublicProfile
	u := fmt.Sprintf("https://%s/.json", sellerHost(seller))
	if err := c.getPublic(ctx, u, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// sellerHost accepts a username or an already-qualified host.
func sellerHost(seller string) string {
	s := strings.TrimSpace(seller)
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimSuffix(s, "/")
	if strings.Contains(s, ".") {
		return s
	}
	return s + ".gumroad.com"
}

// getPublic fetches an unauthenticated endpoint. These live on the seller's own
// subdomain rather than the API host, so they send no Authorization header —
// but they meet the same rate limiter, so they retry the same way.
func (c *Client) getPublic(ctx context.Context, u string, out any) error {
	return c.retry(ctx, func() error { return c.once(ctx, u, false, out) })
}
