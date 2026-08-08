package clienttest

import (
	"bytes"
	"net/http"
	"testing"
)

func TestServerMatchesRequestsWithoutRecordingAuthorization(t *testing.T) {
	t.Parallel()

	mock := NewServer(t, "test-token", Expectation{
		Method:         http.MethodPatch,
		RequestURI:     "/api/automation/v1/components/component-id",
		IdempotencyKey: "operation-id",
		IfMatch:        "7",
		RequestBody:    []byte(`{"name":"Sequencer"}`),
		StatusCode:     http.StatusOK,
		ResponseBody:   Fixture(t, "component.json"),
	})

	request, err := http.NewRequest(http.MethodPatch, mock.URL()+"/api/automation/v1/components/component-id", bytes.NewReader([]byte(`{"name":"Sequencer"}`)))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	request.Header.Set("Authorization", "Bearer test-token")
	request.Header.Set("Idempotency-Key", "operation-id")
	request.Header.Set("If-Match", "7")
	response, err := mock.HTTPClient().Do(request)
	if err != nil {
		t.Fatalf("send request: %v", err)
	}
	response.Body.Close()

	records := mock.Requests()
	if len(records) != 1 {
		t.Fatalf("request records = %d, want 1", len(records))
	}
	if records[0].RequestURI != "/api/automation/v1/components/component-id" {
		t.Fatalf("request URI = %q", records[0].RequestURI)
	}
}
