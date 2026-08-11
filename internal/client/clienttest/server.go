// Package clienttest provides a strict in-process Pulse automation API mock for
// provider unit and protocol tests. It never makes network calls outside the
// local httptest listener.
package clienttest

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

const maxRecordedRequestBody = 1024 * 1024

// Expectation describes one request and its deterministic response.
type Expectation struct {
	Method                string
	RequestURI            string
	IdempotencyKey        string
	RequireIdempotencyKey bool
	IfMatch               string
	IfNoneMatch           string
	RequestBody           []byte
	StatusCode            int
	ResponseHeader        http.Header
	ResponseBody          []byte
}

// RecordedRequest captures non-secret request details useful in assertions.
// Authorization values are intentionally never retained.
type RecordedRequest struct {
	Method         string
	RequestURI     string
	IdempotencyKey string
	IfMatch        string
	IfNoneMatch    string
	Body           []byte
}

// Server is a strict FIFO expectation server.
type Server struct {
	t            testing.TB
	token        string
	server       *httptest.Server
	mu           sync.Mutex
	expectations []Expectation
	requests     []RecordedRequest
	next         int
}

// NewServer starts a local mock server. expectedToken is compared without ever
// printing or recording its value.
func NewServer(t testing.TB, expectedToken string, expectations ...Expectation) *Server {
	t.Helper()
	mock := &Server{t: t, token: expectedToken, expectations: append([]Expectation(nil), expectations...)}
	mock.server = httptest.NewServer(http.HandlerFunc(mock.serveHTTP))
	t.Cleanup(func() {
		mock.server.Close()
		mock.mu.Lock()
		defer mock.mu.Unlock()
		if mock.next != len(mock.expectations) {
			t.Errorf("Pulse mock consumed %d of %d expectations", mock.next, len(mock.expectations))
		}
	})
	return mock
}

// URL returns the local mock origin.
func (s *Server) URL() string { return s.server.URL }

// HTTPClient returns the httptest client for use in client.Config.
func (s *Server) HTTPClient() *http.Client { return s.server.Client() }

// Requests returns defensive copies of all non-secret request records.
func (s *Server) Requests() []RecordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]RecordedRequest, len(s.requests))
	for index, request := range s.requests {
		result[index] = request
		result[index].Body = append([]byte(nil), request.Body...)
	}
	return result
}

func (s *Server) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	body, err := io.ReadAll(io.LimitReader(request.Body, maxRecordedRequestBody+1))
	if err != nil {
		s.t.Errorf("Pulse mock could not read request body: %v", err)
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	if len(body) > maxRecordedRequestBody {
		s.t.Errorf("Pulse mock request body exceeded %d bytes", maxRecordedRequestBody)
		writer.WriteHeader(http.StatusRequestEntityTooLarge)
		return
	}

	record := RecordedRequest{
		Method:         request.Method,
		RequestURI:     request.URL.RequestURI(),
		IdempotencyKey: request.Header.Get("Idempotency-Key"),
		IfMatch:        request.Header.Get("If-Match"),
		IfNoneMatch:    request.Header.Get("If-None-Match"),
		Body:           append([]byte(nil), body...),
	}

	s.mu.Lock()
	s.requests = append(s.requests, record)
	if s.next >= len(s.expectations) {
		s.mu.Unlock()
		s.t.Errorf("Pulse mock received unexpected %s %s", request.Method, request.URL.RequestURI())
		writer.WriteHeader(http.StatusInternalServerError)
		return
	}
	expectation := s.expectations[s.next]
	s.next++
	s.mu.Unlock()

	if request.Header.Get("Authorization") != "Bearer "+s.token {
		s.t.Errorf("Pulse mock request Authorization did not contain the expected bearer credential")
	}
	compareString(s.t, "method", request.Method, expectation.Method)
	compareString(s.t, "request URI", request.URL.RequestURI(), expectation.RequestURI)
	if expectation.RequireIdempotencyKey {
		if !isUUID(request.Header.Get("Idempotency-Key")) {
			s.t.Errorf("Pulse mock Idempotency-Key was not a UUID")
		}
	} else {
		compareString(s.t, "Idempotency-Key", request.Header.Get("Idempotency-Key"), expectation.IdempotencyKey)
	}
	compareString(s.t, "If-Match", request.Header.Get("If-Match"), expectation.IfMatch)
	compareString(s.t, "If-None-Match", request.Header.Get("If-None-Match"), expectation.IfNoneMatch)
	if expectation.RequestBody != nil && !bytes.Equal(body, expectation.RequestBody) {
		s.t.Errorf("Pulse mock request body mismatch: got %s want %s", printableJSON(body), printableJSON(expectation.RequestBody))
	}

	for name, values := range expectation.ResponseHeader {
		for _, value := range values {
			writer.Header().Add(name, value)
		}
	}
	status := expectation.StatusCode
	if status == 0 {
		status = http.StatusOK
	}
	writer.WriteHeader(status)
	if _, err := writer.Write(expectation.ResponseBody); err != nil {
		s.t.Errorf("Pulse mock could not write response: %v", err)
	}
}

func compareString(t testing.TB, label string, got string, want string) {
	t.Helper()
	if got != want {
		t.Errorf("Pulse mock %s = %q, want %q", label, got, want)
	}
}

func printableJSON(value []byte) string {
	if len(value) > 2048 {
		return fmt.Sprintf("<%d bytes>", len(value))
	}
	return string(value)
}

func isUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for index, character := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return value[14] == '4' && (value[19] == '8' || value[19] == '9' || value[19] == 'a' || value[19] == 'b')
}
