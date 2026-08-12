package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDoRetriesIdempotencyKeyedMutation(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	implementation, err := New(Config{
		BaseURL: "https://pulse.example.com",
		Token:   "token",
		Retry:   RetryConfig{MaxAttempts: 2, MinDelay: time.Nanosecond, MaxDelay: time.Nanosecond},
		HTTPClient: doerFunc(func(request *http.Request) (*http.Response, error) {
			attempt := attempts.Add(1)
			if got, want := request.Header.Get("Idempotency-Key"), "operation-123"; got != want {
				t.Errorf("Idempotency-Key = %q, want %q", got, want)
			}
			body, readErr := io.ReadAll(request.Body)
			if readErr != nil {
				t.Errorf("read request body: %v", readErr)
			}
			if got, want := string(body), "{\"name\":\"Example service\"}\n"; got != want {
				t.Errorf("request body = %q, want %q", got, want)
			}
			if attempt == 1 {
				return response(http.StatusServiceUnavailable, `{"code":"service_unavailable","error":"temporary"}`), nil
			}
			return response(http.StatusOK, `{"id":"component-id"}`), nil
		}),
	})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	request, err := implementation.NewRequest(context.Background(), http.MethodPost, "api/automation/v1/components", map[string]string{"name": "Example service"})
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	request.Header.Set("Idempotency-Key", "operation-123")

	var result struct {
		ID string `json:"id"`
	}
	if err := implementation.Do(request, &result); err != nil {
		t.Fatalf("send request: %v", err)
	}
	if got, want := attempts.Load(), int32(2); got != want {
		t.Fatalf("attempts = %d, want %d", got, want)
	}
	if got, want := result.ID, "component-id"; got != want {
		t.Fatalf("component ID = %q, want %q", got, want)
	}
}

func TestDoDoesNotRetryUnkeyedMutation(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	implementation, err := New(Config{
		BaseURL: "https://pulse.example.com",
		Token:   "token",
		Retry:   RetryConfig{MaxAttempts: 3, MinDelay: time.Nanosecond, MaxDelay: time.Nanosecond},
		HTTPClient: doerFunc(func(*http.Request) (*http.Response, error) {
			attempts.Add(1)
			return response(http.StatusServiceUnavailable, `{"code":"service_unavailable","error":"temporary"}`), nil
		}),
	})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	request, err := implementation.NewRequest(context.Background(), http.MethodPost, "api/automation/v1/components", map[string]string{"name": "Example service"})
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	err = implementation.Do(request, nil)
	if !IsErrorCode(err, ErrorCodeServiceFailure) {
		t.Fatalf("error = %v, want service_failure", err)
	}
	if got, want := attempts.Load(), int32(1); got != want {
		t.Fatalf("attempts = %d, want %d", got, want)
	}
}

func TestDoRetriesTruncatedSecretResponseAndReturnsRecoveryMetadata(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	implementation, err := New(Config{
		BaseURL: "https://pulse.example.com",
		Token:   "token",
		Retry:   RetryConfig{MaxAttempts: 2, MinDelay: time.Nanosecond, MaxDelay: time.Nanosecond},
		HTTPClient: doerFunc(func(*http.Request) (*http.Response, error) {
			if attempts.Add(1) == 1 {
				return response(http.StatusCreated, `{"integration":{"id":"integration-id"},"secret":{"value":"must-not-leak"`), nil
			}
			return response(http.StatusConflict, `{
				"code": "secret_reissue_required",
				"error": "unreachable secret must-not-leak",
				"recovery": {
					"integration_id": "integration-id",
					"credential_version_id": "version-id",
					"revision": 9
				}
			}`), nil
		}),
	})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	request, err := implementation.NewRequest(context.Background(), http.MethodPost, "api/automation/v1/components/component-id/integration", map[string]string{"source": "grafana"})
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	request.Header.Set("Idempotency-Key", "operation-123")

	var result map[string]any
	err = implementation.Do(request, &result)
	metadata, ok := SecretReissueMetadataFromError(err)
	if !ok {
		t.Fatalf("error = %v, want secret recovery metadata", err)
	}
	if metadata.IntegrationID != "integration-id" || metadata.CredentialVersionID != "version-id" || metadata.Revision != 9 {
		t.Fatalf("metadata = %#v", metadata)
	}
	if strings.Contains(err.Error(), "must-not-leak") {
		t.Fatalf("error disclosed secret material: %v", err)
	}
}

func TestDoDoesNotExposeMalformedErrorBody(t *testing.T) {
	t.Parallel()

	implementation, err := New(Config{
		BaseURL: "https://pulse.example.com",
		Token:   "token",
		Retry:   RetryConfig{MaxAttempts: 1},
		HTTPClient: doerFunc(func(*http.Request) (*http.Response, error) {
			return response(http.StatusUnprocessableEntity, `secret=must-not-leak`), nil
		}),
	})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	request, err := implementation.NewRequest(context.Background(), http.MethodGet, "api/automation/v1/organization", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	err = implementation.Do(request, nil)
	if !IsErrorCode(err, ErrorCodeValidationFailed) {
		t.Fatalf("error = %v, want validation_failed", err)
	}
	if strings.Contains(err.Error(), "must-not-leak") {
		t.Fatalf("error disclosed response body: %v", err)
	}
}

func TestDoStopsRetryWhenContextIsCancelled(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	var attempts atomic.Int32
	implementation, err := New(Config{
		BaseURL: "https://pulse.example.com",
		Token:   "token",
		Retry:   RetryConfig{MaxAttempts: 3, MinDelay: time.Second, MaxDelay: time.Second},
		HTTPClient: doerFunc(func(*http.Request) (*http.Response, error) {
			attempts.Add(1)
			cancel()
			return response(http.StatusServiceUnavailable, `{"code":"service_unavailable","error":"temporary"}`), nil
		}),
	})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	request, err := implementation.NewRequest(ctx, http.MethodGet, "api/automation/v1/organization", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	err = implementation.Do(request, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context cancellation", err)
	}
	if got, want := attempts.Load(), int32(1); got != want {
		t.Fatalf("attempts = %d, want %d", got, want)
	}
}

func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}
