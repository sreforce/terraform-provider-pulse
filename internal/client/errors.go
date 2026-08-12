package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ErrorCode is a stable machine-readable Pulse automation API error code.
type ErrorCode string

const (
	ErrorCodeUnknown                ErrorCode = "unknown"
	ErrorCodeAuthenticationRequired ErrorCode = "authentication_required"
	ErrorCodePermissionDenied       ErrorCode = "permission_denied"
	ErrorCodeResourceNotFound       ErrorCode = "resource_not_found"
	ErrorCodeValidationFailed       ErrorCode = "validation_failed"
	ErrorCodeAlreadyExists          ErrorCode = "already_exists"
	ErrorCodeConflict               ErrorCode = "conflict"
	ErrorCodeStaleRevision          ErrorCode = "stale_revision"
	ErrorCodeOwnershipConflict      ErrorCode = "ownership_conflict"
	ErrorCodeIdempotencyConflict    ErrorCode = "idempotency_conflict"
	ErrorCodeSecretReissueRequired  ErrorCode = "secret_reissue_required"
	ErrorCodeRateLimited            ErrorCode = "rate_limited"
	ErrorCodeServiceUnavailable     ErrorCode = "service_unavailable"

	// Compatibility aliases keep caller code readable while preserving the
	// canonical automation API wire values above.
	ErrorCodeAuthenticationFailed = ErrorCodeAuthenticationRequired
	ErrorCodeNotFound             = ErrorCodeResourceNotFound
	ErrorCodeServiceFailure       = ErrorCodeServiceUnavailable
)

var safeIdentifierPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

// FieldViolation contains only a stable field identifier. Human server messages
// are deliberately not retained because they can contain submitted values,
// including secrets.
type FieldViolation struct {
	Field string
}

// SecretReissueMetadata identifies an integration version whose one-time
// plaintext response was not received. It never contains secret material.
type SecretReissueMetadata struct {
	CredentialVersionID string
	Revision            int64
}

// ResponseError describes a non-success HTTP response. Error intentionally
// omits the response body and server message so Terraform diagnostics cannot
// disclose echoed request values or credential material.
type ResponseError struct {
	StatusCode    int
	Code          ErrorCode
	RequestID     string
	RetryAfter    time.Duration
	Violations    []FieldViolation
	SecretReissue *SecretReissueMetadata
}

func (e *ResponseError) Error() string {
	if e == nil {
		return "Pulse API request failed"
	}
	if e.Code != "" && e.Code != ErrorCodeUnknown {
		return fmt.Sprintf("Pulse API returned HTTP status %d (%s)", e.StatusCode, e.Code)
	}
	return fmt.Sprintf("Pulse API returned HTTP status %d", e.StatusCode)
}

// Retryable reports whether the server response is safe to retry when the
// request itself is replay-safe.
func (e *ResponseError) Retryable() bool {
	if e == nil {
		return false
	}
	if e.Code == ErrorCodeRateLimited || e.Code == ErrorCodeServiceUnavailable {
		return true
	}
	switch e.StatusCode {
	case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway,
		http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// TransportError hides the URL and submitted values while preserving the
// underlying error for retry classification and errors.Is/errors.As.
type TransportError struct {
	err error
}

func (e *TransportError) Error() string { return "Pulse API request transport failed" }
func (e *TransportError) Unwrap() error { return e.err }

// IsErrorCode reports whether err is a Pulse response carrying code.
func IsErrorCode(err error, code ErrorCode) bool {
	var responseErr *ResponseError
	return errors.As(err, &responseErr) && responseErr.Code == code
}

// SecretReissueMetadataFromError extracts safe recovery metadata from a lost
// one-time-secret response.
func SecretReissueMetadataFromError(err error) (SecretReissueMetadata, bool) {
	var responseErr *ResponseError
	if !errors.As(err, &responseErr) || responseErr.Code != ErrorCodeSecretReissueRequired || responseErr.SecretReissue == nil {
		return SecretReissueMetadata{}, false
	}
	return *responseErr.SecretReissue, true
}

type wireErrorEnvelope struct {
	Code       string                     `json:"code"`
	Error      string                     `json:"error"`
	RequestID  string                     `json:"request_id"`
	Violations []wireFieldViolation       `json:"field_violations"`
	Recovery   *wireSecretReissueMetadata `json:"recovery"`
}

type wireFieldViolation struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

type wireSecretReissueMetadata struct {
	CredentialVersionID string `json:"credential_version_id"`
	Revision            int64  `json:"revision"`
}

func decodeResponseError(response *http.Response, body []byte) *ResponseError {
	result := &ResponseError{
		StatusCode: response.StatusCode,
		Code:       fallbackErrorCode(response.StatusCode),
		RetryAfter: parseRetryAfter(response.Header.Get("Retry-After"), time.Now()),
	}
	result.RequestID = safeIdentifier(response.Header.Get("X-Request-ID"))

	var envelope wireErrorEnvelope
	if len(body) == 0 || json.Unmarshal(body, &envelope) != nil {
		return result
	}
	if result.RequestID == "" {
		result.RequestID = safeIdentifier(envelope.RequestID)
	}
	if code := safeErrorCode(envelope.Code); code != ErrorCodeUnknown {
		result.Code = code
	}
	for _, violation := range envelope.Violations {
		field := safeIdentifier(violation.Field)
		if field != "" {
			result.Violations = append(result.Violations, FieldViolation{Field: field})
		}
	}
	if result.Code == ErrorCodeSecretReissueRequired && envelope.Recovery != nil {
		versionID := safeIdentifier(envelope.Recovery.CredentialVersionID)
		if versionID != "" && envelope.Recovery.Revision > 0 {
			result.SecretReissue = &SecretReissueMetadata{
				CredentialVersionID: versionID,
				Revision:            envelope.Recovery.Revision,
			}
		}
	}
	return result
}

func safeErrorCode(raw string) ErrorCode {
	if !safeIdentifierPattern.MatchString(raw) || strings.ToLower(raw) != raw {
		return ErrorCodeUnknown
	}
	return ErrorCode(raw)
}

func safeIdentifier(raw string) string {
	if !safeIdentifierPattern.MatchString(raw) {
		return ""
	}
	return raw
}

func fallbackErrorCode(status int) ErrorCode {
	switch status {
	case http.StatusUnauthorized:
		return ErrorCodeAuthenticationRequired
	case http.StatusForbidden:
		return ErrorCodePermissionDenied
	case http.StatusNotFound:
		return ErrorCodeResourceNotFound
	case http.StatusUnprocessableEntity:
		return ErrorCodeValidationFailed
	case http.StatusConflict:
		return ErrorCodeConflict
	case http.StatusTooManyRequests:
		return ErrorCodeRateLimited
	default:
		if status >= http.StatusInternalServerError {
			return ErrorCodeServiceUnavailable
		}
		return ErrorCodeUnknown
	}
}

func parseRetryAfter(raw string, now time.Time) time.Duration {
	if raw == "" {
		return 0
	}
	if seconds, err := strconv.ParseInt(raw, 10, 32); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}
	when, err := http.ParseTime(raw)
	if err != nil || !when.After(now) {
		return 0
	}
	return when.Sub(now)
}
