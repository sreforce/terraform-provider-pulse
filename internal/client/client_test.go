package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestNewRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	tests := map[string]Config{
		"missing URL":            {Token: "token"},
		"unsupported URL scheme": {BaseURL: "ftp://pulse.example.com", Token: "token"},
		"missing URL host":       {BaseURL: "https:///api", Token: "token"},
		"URL user info":          {BaseURL: "https://user@pulse.example.com", Token: "token"},
		"URL query":              {BaseURL: "https://pulse.example.com?x=1", Token: "token"},
		"missing token":          {BaseURL: "https://pulse.example.com"},
		"token whitespace":       {BaseURL: "https://pulse.example.com", Token: " token"},
		"token line break":       {BaseURL: "https://pulse.example.com", Token: "tok\nen"},
	}

	for name, config := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := New(config); err == nil {
				t.Fatal("expected configuration error")
			}
		})
	}
}

func TestNewRequestUsesConfiguredOriginAndAuthentication(t *testing.T) {
	t.Parallel()

	client, err := New(Config{
		BaseURL:   "https://pulse.example.com/automation/v1",
		Token:     "secret-token",
		UserAgent: "terraform-provider-pulse/test",
	})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	request, err := client.NewRequest(context.Background(), http.MethodPost, "components?active=true", map[string]string{"name": "Sequencer"})
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	if got, want := request.URL.String(), "https://pulse.example.com/automation/v1/components?active=true"; got != want {
		t.Fatalf("request URL = %q, want %q", got, want)
	}
	if got, want := request.Header.Get("Authorization"), "Bearer secret-token"; got != want {
		t.Fatalf("Authorization = %q, want %q", got, want)
	}
	if got, want := request.Header.Get("User-Agent"), "terraform-provider-pulse/test"; got != want {
		t.Fatalf("User-Agent = %q, want %q", got, want)
	}
	if got, want := request.Header.Get("Content-Type"), "application/json"; got != want {
		t.Fatalf("Content-Type = %q, want %q", got, want)
	}

	body, err := io.ReadAll(request.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if got, want := string(body), "{\"name\":\"Sequencer\"}\n"; got != want {
		t.Fatalf("body = %q, want %q", got, want)
	}
}

func TestNewRequestRejectsOriginBypass(t *testing.T) {
	t.Parallel()

	client, err := New(Config{BaseURL: "https://pulse.example.com", Token: "token"})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	for _, path := range []string{"/absolute", "https://attacker.example/path", "../outside"} {
		if _, err := client.NewRequest(context.Background(), http.MethodGet, path, nil); err == nil {
			t.Fatalf("expected path %q to be rejected", path)
		}
	}
}

func TestDoDecodesSuccess(t *testing.T) {
	t.Parallel()

	client, err := New(Config{
		BaseURL: "https://pulse.example.com",
		Token:   "token",
		HTTPClient: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"id":"component-id"}`)),
				Header:     make(http.Header),
			}, nil
		}),
	})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	request, err := client.NewRequest(context.Background(), http.MethodGet, "object", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	var response struct {
		ID string `json:"id"`
	}
	if err := client.Do(request, &response); err != nil {
		t.Fatalf("send request: %v", err)
	}
	if got, want := response.ID, "component-id"; got != want {
		t.Fatalf("response ID = %q, want %q", got, want)
	}
}

func TestDoReturnsSafeResponseError(t *testing.T) {
	t.Parallel()

	client, err := New(Config{
		BaseURL: "https://pulse.example.com",
		Token:   "token",
		HTTPClient: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusUnauthorized,
				Body:       io.NopCloser(strings.NewReader(`{"detail":"sensitive server detail"}`)),
				Header:     make(http.Header),
			}, nil
		}),
	})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	request, err := client.NewRequest(context.Background(), http.MethodGet, "object", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}

	err = client.Do(request, nil)
	var responseErr *ResponseError
	if !errors.As(err, &responseErr) {
		t.Fatalf("error = %v, want *ResponseError", err)
	}
	if responseErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status code = %d, want %d", responseErr.StatusCode, http.StatusUnauthorized)
	}
	if strings.Contains(err.Error(), "sensitive server detail") {
		t.Fatalf("error exposed response body: %v", err)
	}
}

func TestClientDoesNotForwardBearerCredentialAcrossRedirect(t *testing.T) {
	t.Parallel()

	var targetRequests atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		targetRequests.Add(1)
	}))
	defer target.Close()

	origin := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, target.URL+"/capture", http.StatusTemporaryRedirect)
	}))
	defer origin.Close()

	implementation, err := New(Config{
		BaseURL:    origin.URL,
		Token:      "sensitive-token",
		HTTPClient: origin.Client(),
		Retry:      RetryConfig{MaxAttempts: 1},
	})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	request, err := implementation.NewRequest(context.Background(), http.MethodGet, "api/automation/v1/organization", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	err = implementation.Do(request, nil)
	var responseErr *ResponseError
	if !errors.As(err, &responseErr) || responseErr.StatusCode != http.StatusTemporaryRedirect {
		t.Fatalf("error = %v, want redirect response error", err)
	}
	if targetRequests.Load() != 0 {
		t.Fatal("client followed a redirect carrying an automation credential")
	}
}
