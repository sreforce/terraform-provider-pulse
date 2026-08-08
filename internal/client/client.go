package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultTimeout = 30 * time.Second
)

// HTTPDoer is the subset of http.Client used by the Pulse client.
type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// API is the client contract consumed by future provider resources and data sources.
type API interface {
	NewRequest(context.Context, string, string, any) (*http.Request, error)
	Do(*http.Request, any) error
}

// Config configures a Pulse API client without selecting any API endpoint paths.
type Config struct {
	BaseURL    string
	Token      string
	UserAgent  string
	HTTPClient HTTPDoer
	Retry      RetryConfig
}

// Client is an authenticated Pulse HTTP client.
type Client struct {
	baseURL    *url.URL
	token      string
	userAgent  string
	httpClient HTTPDoer
	retry      RetryConfig
}

// New validates configuration and returns an authenticated client.
func New(config Config) (*Client, error) {
	baseURL, err := parseBaseURL(config.BaseURL)
	if err != nil {
		return nil, err
	}

	if config.Token == "" || strings.TrimSpace(config.Token) == "" {
		return nil, errors.New("pulse API token must not be empty")
	}
	if config.Token != strings.TrimSpace(config.Token) {
		return nil, errors.New("pulse API token must not contain surrounding whitespace")
	}
	if strings.ContainsAny(config.Token, "\r\n") {
		return nil, errors.New("pulse API token must not contain line breaks")
	}

	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultTimeout}
	}

	userAgent := config.UserAgent
	if strings.TrimSpace(userAgent) == "" {
		userAgent = "terraform-provider-pulse/dev"
	}

	retry, err := normalizeRetryConfig(config.Retry)
	if err != nil {
		return nil, err
	}

	return &Client{
		baseURL:    baseURL,
		token:      config.Token,
		userAgent:  userAgent,
		httpClient: httpClient,
		retry:      retry,
	}, nil
}

// NewRequest creates an authenticated request for a relative Pulse API path.
// Domain packages own endpoint paths; this transport refuses absolute references
// so callers cannot bypass the configured Pulse origin.
func (c *Client) NewRequest(ctx context.Context, method, relativePath string, body any) (*http.Request, error) {
	reference, err := parseRelativeReference(relativePath)
	if err != nil {
		return nil, err
	}

	var requestBody io.Reader
	if body != nil {
		var encoded bytes.Buffer
		if err := json.NewEncoder(&encoded).Encode(body); err != nil {
			return nil, fmt.Errorf("encode Pulse API request body: %w", err)
		}
		requestBody = &encoded
	}

	request, err := http.NewRequestWithContext(ctx, method, c.baseURL.ResolveReference(reference).String(), requestBody)
	if err != nil {
		return nil, fmt.Errorf("create Pulse API request: %w", err)
	}

	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("User-Agent", c.userAgent)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	return request, nil
}

// Do sends a request and optionally decodes a successful JSON response. It may
// retry read-only requests and mutations carrying an Idempotency-Key. Mutation
// callers must not rely on HTTP method semantics alone for replay safety.
func (c *Client) Do(request *http.Request, responseBody any) error {
	return c.doWithRetry(request, responseBody)
}

func parseBaseURL(raw string) (*url.URL, error) {
	if raw == "" || strings.TrimSpace(raw) == "" {
		return nil, errors.New("pulse API URL must not be empty")
	}
	if raw != strings.TrimSpace(raw) {
		return nil, errors.New("pulse API URL must not contain surrounding whitespace")
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse Pulse API URL: %w", err)
	}
	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return nil, errors.New("pulse API URL scheme must be http or https")
	}
	if parsed.Host == "" {
		return nil, errors.New("pulse API URL must include a host")
	}
	if parsed.User != nil {
		return nil, errors.New("pulse API URL must not include user information")
	}
	if parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("pulse API URL must not include a query string or fragment")
	}

	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/"
	return parsed, nil
}

func parseRelativeReference(raw string) (*url.URL, error) {
	if strings.HasPrefix(raw, "/") {
		return nil, errors.New("pulse API request path must be relative")
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse Pulse API request path: %w", err)
	}
	if parsed.IsAbs() || parsed.Host != "" || parsed.User != nil {
		return nil, errors.New("pulse API request path must be relative")
	}
	for _, segment := range strings.Split(parsed.Path, "/") {
		if segment == ".." {
			return nil, errors.New("pulse API request path must not traverse parent paths")
		}
	}

	return parsed, nil
}

var _ API = (*Client)(nil)
