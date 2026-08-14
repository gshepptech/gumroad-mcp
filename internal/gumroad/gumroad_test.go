package gumroad

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// serve stands up a fake API. handler receives the attempt number, 1-based, so
// a test can fail the first N calls and then succeed.
func serve(t *testing.T, handler func(attempt int, w http.ResponseWriter, r *http.Request)) (*Client, func() int, func() []*http.Request) {
	t.Helper()
	var attempts int
	var seen []*http.Request

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		seen = append(seen, r.Clone(r.Context()))
		w.Header().Set("Content-Type", "application/json")
		handler(attempts, w, r)
	}))
	t.Cleanup(srv.Close)

	c := New("tok")
	c.BaseURL = srv.URL
	c.RetryBase = time.Millisecond // do not sleep for seconds in tests
	return c, func() int { return attempts }, func() []*http.Request { return seen }
}

func TestRetriesRateLimitsThenSucceeds(t *testing.T) {
	c, attempts, _ := serve(t, func(n int, w http.ResponseWriter, _ *http.Request) {
		if n < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			io.WriteString(w, `{"success":false,"message":"slow down"}`)
			return
		}
		io.WriteString(w, `{"success":true,"product_reviews":[{"id":"R1","rating":5}]}`)
	})

	reviews, _, err := c.ListReviews(context.Background(), "P1", "")
	if err != nil {
		t.Fatalf("gave up on a retryable failure: %v", err)
	}
	if attempts() != 3 {
		t.Errorf("made %d attempts, want 3", attempts())
	}
	if len(reviews) != 1 || reviews[0].ID != "R1" {
		t.Errorf("decoded %+v", reviews)
	}
}

func TestRetriesServerErrors(t *testing.T) {
	c, attempts, _ := serve(t, func(n int, w http.ResponseWriter, _ *http.Request) {
		if n < 2 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		io.WriteString(w, `{"success":true,"tax_forms":[]}`)
	})
	if _, err := c.ListTaxForms(context.Background(), 2025); err != nil {
		t.Fatalf("gave up on a 502: %v", err)
	}
	if attempts() != 2 {
		t.Errorf("made %d attempts, want 2", attempts())
	}
}

// TestDoesNotRetryClientErrors: a 404 will not fix itself, and retrying it
// wastes the caller's rate-limit budget.
func TestDoesNotRetryClientErrors(t *testing.T) {
	c, attempts, _ := serve(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"success":false,"message":"The product could not be found."}`)
	})

	_, _, err := c.ListReviews(context.Background(), "nope", "")
	if err == nil {
		t.Fatal("a 404 was reported as success")
	}
	if attempts() != 1 {
		t.Errorf("retried an unretryable error %d times", attempts()-1)
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not an *APIError: %T", err)
	}
	if apiErr.StatusCode != 404 || apiErr.Message != "The product could not be found." {
		t.Errorf("lost the detail: %+v", apiErr)
	}
}

func TestGivesUpAfterMaxAttempts(t *testing.T) {
	c, attempts, _ := serve(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	if _, err := c.GetEarnings(context.Background(), 2025); err == nil {
		t.Fatal("persistent rate limiting was reported as success")
	}
	if attempts() != maxAttempts {
		t.Errorf("made %d attempts, want %d", attempts(), maxAttempts)
	}
}

// TestRetryIsCancellable: the backoff must not outlive its context.
func TestRetryIsCancellable(t *testing.T) {
	c, _, _ := serve(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	c.RetryBase = time.Hour // long enough that only cancellation can end it

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	done := make(chan error, 1)
	go func() { _, err := c.GetEarnings(ctx, 2025); done <- err }()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("backoff ignored the cancelled context")
	}
}

// TestRetryableErrorSurvivesWrapping is the regression for a real bug: the
// retry check used a bare type assertion, so a wrapped *APIError stopped
// matching and retries silently died.
func TestRetryableErrorSurvivesWrapping(t *testing.T) {
	wrapped := fmt.Errorf("fetching reviews: %w", &APIError{StatusCode: 429})

	var apiErr *APIError
	if !asAPIError(wrapped, &apiErr) {
		t.Fatal("a wrapped *APIError was not recognised, so it would never be retried")
	}
	if !apiErr.Retryable() {
		t.Error("429 is not marked retryable")
	}
}

func TestSuccessFalseOn200IsAnError(t *testing.T) {
	c, _, _ := serve(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `{"success":false,"message":"Not available for this account."}`)
	})
	_, err := c.GetEarnings(context.Background(), 2025)
	if err == nil {
		t.Fatal("200 with success:false was treated as success")
	}
	if !strings.Contains(err.Error(), "Not available for this account.") {
		t.Errorf("lost the message: %v", err)
	}
}

// TestAuthenticatedRequestsCarryTheToken and its counterpart below are the pair
// that matters: the public endpoints are on a seller's own subdomain, so
// sending the token there would leak it to a host that has no business with it.
func TestAuthenticatedRequestsCarryTheToken(t *testing.T) {
	c, _, seen := serve(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"success":true,"tax_forms":[]}`)
	})
	if _, err := c.ListTaxForms(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	if got := seen()[0].Header.Get("Authorization"); got != "Bearer tok" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer tok")
	}
}

func TestPublicRequestsSendNoCredential(t *testing.T) {
	var seen *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Clone(r.Context())
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"api_version":1,"id":"P1","name":"Pencil"}`)
	}))
	defer srv.Close()

	c := New("tok")
	c.RetryBase = time.Millisecond
	// Point the public helper straight at the test server.
	var out PublicProduct
	if err := c.getPublic(context.Background(), srv.URL, &out); err != nil {
		t.Fatal(err)
	}
	if got := seen.Header.Get("Authorization"); got != "" {
		t.Errorf("public request carried a credential: %q", got)
	}
	if out.Name != "Pencil" {
		t.Errorf("decoded %+v", out)
	}
}

func TestPublicRequestsRetryToo(t *testing.T) {
	var attempts int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "application/json")
		if attempts < 2 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		io.WriteString(w, `{"api_version":1,"name":"Pencil"}`)
	}))
	defer srv.Close()

	c := New("")
	c.RetryBase = time.Millisecond
	var out PublicProduct
	if err := c.getPublic(context.Background(), srv.URL, &out); err != nil {
		t.Fatalf("public endpoint gave up on a 429: %v", err)
	}
	if attempts != 2 {
		t.Errorf("made %d attempts, want 2", attempts)
	}
}

func TestSellerHostAcceptsUsernameOrHost(t *testing.T) {
	for in, want := range map[string]string{
		"sahil":                     "sahil.gumroad.com",
		"sahil.gumroad.com":         "sahil.gumroad.com",
		"https://sahil.gumroad.com": "sahil.gumroad.com",
		"https://shop.example.com/": "shop.example.com",
		"  sahil  ":                 "sahil.gumroad.com",
	} {
		if got := sellerHost(in); got != want {
			t.Errorf("sellerHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReviewsPaginate(t *testing.T) {
	c, _, seen := serve(t, func(_ int, w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"success":true,"next_page_key":"k2","product_reviews":[{"id":"R1"}]}`)
	})
	reviews, next, err := c.ListReviews(context.Background(), "P1", "k1")
	if err != nil {
		t.Fatal(err)
	}
	if next != "k2" {
		t.Errorf("next_page_key = %q", next)
	}
	if len(reviews) != 1 {
		t.Errorf("got %d reviews", len(reviews))
	}
	if got := seen()[0].URL.Query().Get("page_key"); got != "k1" {
		t.Errorf("page_key was not forwarded: %q", got)
	}
}
