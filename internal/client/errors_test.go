package client

import (
	"net/http"
	"testing"
	"time"
)

func TestDecodeResponseErrorUsesStableEnvelopeOnly(t *testing.T) {
	t.Parallel()

	response := &http.Response{StatusCode: http.StatusConflict, Header: make(http.Header)}
	response.Header.Set("X-Request-ID", "request-123")
	response.Header.Set("Retry-After", "10")
	err := decodeResponseError(response, []byte(`{
		"code": "stale_revision",
		"error": "submitted token must-not-leak",
		"field_violations": [
			{"field":"revision","message":"must-not-leak"},
			{"field":"not safe!","message":"ignored"}
		]
	}`))

	if err.Code != ErrorCodeStaleRevision {
		t.Fatalf("code = %q, want %q", err.Code, ErrorCodeStaleRevision)
	}
	if err.RequestID != "request-123" {
		t.Fatalf("request ID = %q, want request-123", err.RequestID)
	}
	if err.RetryAfter != 10*time.Second {
		t.Fatalf("retry after = %s, want 10s", err.RetryAfter)
	}
	if len(err.Violations) != 1 || err.Violations[0] != (FieldViolation{Field: "revision"}) {
		t.Fatalf("violations = %#v", err.Violations)
	}
}

func TestFallbackErrorCodes(t *testing.T) {
	t.Parallel()

	tests := map[int]ErrorCode{
		http.StatusUnauthorized:        ErrorCodeAuthenticationFailed,
		http.StatusForbidden:           ErrorCodePermissionDenied,
		http.StatusNotFound:            ErrorCodeNotFound,
		http.StatusConflict:            ErrorCodeConflict,
		http.StatusUnprocessableEntity: ErrorCodeValidationFailed,
		http.StatusTooManyRequests:     ErrorCodeRateLimited,
		http.StatusInternalServerError: ErrorCodeServiceFailure,
	}
	for status, want := range tests {
		if got := fallbackErrorCode(status); got != want {
			t.Errorf("status %d code = %q, want %q", status, got, want)
		}
	}
}
