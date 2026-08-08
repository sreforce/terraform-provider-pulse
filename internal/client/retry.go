package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

const (
	defaultMaxAttempts     = 4
	defaultMinRetryDelay   = 200 * time.Millisecond
	defaultMaxRetryDelay   = 3 * time.Second
	maxErrorResponseBody   = 64 * 1024
	maxSuccessResponseBody = 8 * 1024 * 1024
)

// RetryConfig bounds automatic retries. Zero values select conservative
// defaults. MaxAttempts includes the initial request.
type RetryConfig struct {
	MaxAttempts int
	MinDelay    time.Duration
	MaxDelay    time.Duration
}

func normalizeRetryConfig(config RetryConfig) (RetryConfig, error) {
	if config.MaxAttempts == 0 {
		config.MaxAttempts = defaultMaxAttempts
	}
	if config.MinDelay == 0 {
		config.MinDelay = defaultMinRetryDelay
	}
	if config.MaxDelay == 0 {
		config.MaxDelay = defaultMaxRetryDelay
	}
	if config.MaxAttempts < 1 || config.MaxAttempts > 10 {
		return RetryConfig{}, errors.New("Pulse retry max attempts must be between 1 and 10")
	}
	if config.MinDelay < 0 || config.MaxDelay < 0 {
		return RetryConfig{}, errors.New("Pulse retry delays must not be negative")
	}
	if config.MaxDelay < config.MinDelay {
		return RetryConfig{}, errors.New("Pulse retry maximum delay must not be less than minimum delay")
	}
	return config, nil
}

func (c *Client) doWithRetry(request *http.Request, responseBody any) error {
	replaySafe := requestReplaySafe(request)
	var lastErr error

	for attempt := 1; attempt <= c.retry.MaxAttempts; attempt++ {
		attemptRequest, err := cloneRequest(request, attempt)
		if err != nil {
			return err
		}
		response, err := c.httpClient.Do(attemptRequest)
		if err != nil {
			lastErr = &TransportError{err: err}
			if !replaySafe || attempt == c.retry.MaxAttempts || !retryableTransportError(err) {
				return lastErr
			}
			if err := sleepContext(request.Context(), c.retryDelay(attempt, 0)); err != nil {
				return err
			}
			continue
		}

		if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
			body, readErr := readBounded(response.Body, maxErrorResponseBody)
			response.Body.Close()
			if readErr != nil {
				lastErr = &TransportError{err: readErr}
				if replaySafe && attempt < c.retry.MaxAttempts && retryableTransportError(readErr) {
					if err := sleepContext(request.Context(), c.retryDelay(attempt, 0)); err != nil {
						return err
					}
					continue
				}
				return lastErr
			}

			responseErr := decodeResponseError(response, body)
			lastErr = responseErr
			if replaySafe && attempt < c.retry.MaxAttempts && responseErr.Retryable() {
				if err := sleepContext(request.Context(), c.retryDelay(attempt, responseErr.RetryAfter)); err != nil {
					return err
				}
				continue
			}
			return responseErr
		}

		if responseBody == nil || response.StatusCode == http.StatusNoContent {
			response.Body.Close()
			return nil
		}

		body, readErr := readBounded(response.Body, maxSuccessResponseBody)
		response.Body.Close()
		if readErr != nil {
			lastErr = &TransportError{err: readErr}
			if replaySafe && attempt < c.retry.MaxAttempts && retryableTransportError(readErr) {
				if err := sleepContext(request.Context(), c.retryDelay(attempt, 0)); err != nil {
					return err
				}
				continue
			}
			return lastErr
		}
		if err := json.Unmarshal(body, responseBody); err != nil {
			lastErr = fmt.Errorf("decode Pulse API response: %w", err)
			if replaySafe && attempt < c.retry.MaxAttempts && incompleteJSON(err, len(body)) {
				if err := sleepContext(request.Context(), c.retryDelay(attempt, 0)); err != nil {
					return err
				}
				continue
			}
			return lastErr
		}
		return nil
	}

	return lastErr
}

func cloneRequest(request *http.Request, attempt int) (*http.Request, error) {
	clone := request.Clone(request.Context())
	if request.Body == nil {
		return clone, nil
	}
	if request.GetBody == nil {
		if attempt == 1 {
			return request, nil
		}
		return nil, errors.New("Pulse API request body cannot be replayed")
	}
	body, err := request.GetBody()
	if err != nil {
		return nil, fmt.Errorf("recreate Pulse API request body: %w", err)
	}
	clone.Body = body
	return clone, nil
}

func requestReplaySafe(request *http.Request) bool {
	switch request.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return request.Header.Get("Idempotency-Key") != ""
	}
}

func retryableTransportError(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var networkErr net.Error
	return errors.As(err, &networkErr) && (networkErr.Timeout() || networkErr.Temporary())
}

func incompleteJSON(err error, bodyLength int) bool {
	if errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var syntaxErr *json.SyntaxError
	return errors.As(err, &syntaxErr) && int(syntaxErr.Offset) >= bodyLength
}

func readBounded(reader io.Reader, maximum int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(reader, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maximum {
		return nil, errors.New("Pulse API response exceeded the size limit")
	}
	return body, nil
}

func (c *Client) retryDelay(attempt int, retryAfter time.Duration) time.Duration {
	delay := c.retry.MinDelay
	for step := 1; step < attempt && delay < c.retry.MaxDelay; step++ {
		if delay > c.retry.MaxDelay/2 {
			delay = c.retry.MaxDelay
			break
		}
		delay *= 2
	}
	if retryAfter > delay {
		delay = retryAfter
	}
	if delay > c.retry.MaxDelay {
		return c.retry.MaxDelay
	}
	return delay
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
