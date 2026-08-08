package client

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/sreforce/terraform-provider-pulse/internal/client/clienttest"
)

func TestComponentIntegrationSecretFormattingIsAlwaysRedacted(t *testing.T) {
	t.Parallel()

	secret := ComponentIntegrationSecret{Value: "plaintext-must-not-appear", VersionID: "version-id"}
	mutation := ComponentIntegrationMutation{Secret: &secret}
	values := []any{secret, &secret, mutation, &mutation}
	verbs := []string{"%v", "%+v", "%#v", "%s", "%q"}
	for _, value := range values {
		for _, verb := range verbs {
			formatted := fmt.Sprintf(verb, value)
			if strings.Contains(formatted, secret.Value) {
				t.Fatalf("format %q of %T disclosed plaintext: %s", verb, value, formatted)
			}
			if !strings.Contains(formatted, "<sensitive>") {
				t.Fatalf("format %q of %T was not visibly redacted: %s", verb, value, formatted)
			}
		}
	}
}

func TestIntegrationMutationRejectsIncompleteSuccess(t *testing.T) {
	t.Parallel()

	mock := clienttest.NewServer(t, testToken, clienttest.Expectation{
		Method:                http.MethodPost,
		RequestURI:            "/api/automation/v1/components/" + testComponentID + "/integration",
		RequireIdempotencyKey: true,
		RequestBody:           []byte("{\"source\":\"grafana\",\"source_key\":\"sequencer-commitment\"}\n"),
		StatusCode:            http.StatusCreated,
		ResponseBody:          []byte(`{}`),
	})
	implementation := newMockClient(t, mock)

	result, err := implementation.CreateComponentIntegration(context.Background(), testComponentID, ComponentIntegrationCreateRequest{
		Source:    IntegrationSourceGrafana,
		SourceKey: "sequencer-commitment",
	}, MutationOptions{})
	if !IsContractError(err) {
		t.Fatalf("error = %v, want contract error", err)
	}
	if result.Secret != nil || result.Integration.ID != "" {
		t.Fatalf("invalid success data escaped: %#v", result)
	}
}

func TestIntegrationMutationRejectsMismatchedSecretVersionWithoutLeaking(t *testing.T) {
	t.Parallel()

	response := []byte(`{
		"integration": {
			"id": "00000000-0000-4000-8000-000000000301",
			"component_id": "00000000-0000-4000-8000-000000000101",
			"source": "grafana",
			"source_key": "sequencer-commitment",
			"endpoint": "https://pulse.example.com/webhooks/component-integrations/00000000-0000-4000-8000-000000000301/grafana",
			"lifecycle_owner": "automation",
			"status": "active",
			"credential_version_id": "expected-version",
			"revision": 1,
			"archived_at": null
		},
		"secret": {"value":"plaintext-must-not-leak","version_id":"wrong-version"}
	}`)
	mock := clienttest.NewServer(t, testToken, clienttest.Expectation{
		Method:                http.MethodPost,
		RequestURI:            "/api/automation/v1/components/" + testComponentID + "/integration",
		RequireIdempotencyKey: true,
		StatusCode:            http.StatusCreated,
		ResponseBody:          response,
	})
	implementation := newMockClient(t, mock)

	result, err := implementation.CreateComponentIntegration(context.Background(), testComponentID, ComponentIntegrationCreateRequest{
		Source:    IntegrationSourceGrafana,
		SourceKey: "sequencer-commitment",
	}, MutationOptions{})
	if !IsContractError(err) {
		t.Fatalf("error = %v, want contract error", err)
	}
	if strings.Contains(err.Error(), "plaintext-must-not-leak") || result.Secret != nil {
		t.Fatalf("contract failure exposed one-time secret: error=%v result=%#v", err, result)
	}
}

func TestIntegrationEndpointRequiresEncryptedTransport(t *testing.T) {
	t.Parallel()

	integration := ComponentIntegration{
		ID:                  "00000000-0000-4000-8000-000000000301",
		ComponentID:         testComponentID,
		Source:              IntegrationSourceGrafana,
		SourceKey:           "sequencer-commitment",
		Endpoint:            "http://pulse.example.com/webhooks/component-integrations/00000000-0000-4000-8000-000000000301/grafana",
		LifecycleOwner:      IntegrationLifecycleOwnerAutomation,
		Status:              IntegrationStatusActive,
		CredentialVersionID: "00000000-0000-4000-8000-000000000401",
		Revision:            1,
	}
	if err := validateIntegration(integration, testComponentID, true); !IsContractError(err) {
		t.Fatalf("remote plaintext integration endpoint error = %v, want contract error", err)
	}

	integration.Endpoint = "http://127.0.0.1:8080/webhooks/component-integrations/00000000-0000-4000-8000-000000000301/grafana"
	if err := validateIntegration(integration, testComponentID, false); !IsContractError(err) {
		t.Fatalf("unapproved loopback endpoint error = %v, want contract error", err)
	}
	if err := validateIntegration(integration, testComponentID, true); err != nil {
		t.Fatalf("explicit loopback development endpoint was rejected: %v", err)
	}
}

func TestLostIntegrationSecretRecoverySequence(t *testing.T) {
	t.Parallel()

	createBody := []byte("{\"source\":\"grafana\",\"source_key\":\"sequencer-commitment\"}\n")
	rotationResponse := []byte(`{
		"integration": {
			"id": "00000000-0000-4000-8000-000000000301",
			"component_id": "00000000-0000-4000-8000-000000000101",
			"source": "grafana",
			"source_key": "sequencer-commitment",
			"endpoint": "https://pulse.example.com/webhooks/component-integrations/00000000-0000-4000-8000-000000000301/grafana",
			"lifecycle_owner": "automation",
			"status": "active",
			"credential_version_id": "00000000-0000-4000-8000-000000000402",
			"revision": 2,
			"archived_at": null
		},
		"secret": {
			"value": "replacement-one-time-secret",
			"version_id": "00000000-0000-4000-8000-000000000402"
		}
	}`)
	mock := clienttest.NewServer(t, testToken,
		clienttest.Expectation{
			Method:                http.MethodPost,
			RequestURI:            "/api/automation/v1/components/" + testComponentID + "/integration",
			RequireIdempotencyKey: true,
			RequestBody:           createBody,
			StatusCode:            http.StatusCreated,
			ResponseBody:          []byte(`{"integration":{"id":"00000000-0000-4000-8000-000000000301"},"secret":{"value":"unreachable"`),
		},
		clienttest.Expectation{
			Method:                http.MethodPost,
			RequestURI:            "/api/automation/v1/components/" + testComponentID + "/integration",
			RequireIdempotencyKey: true,
			RequestBody:           createBody,
			StatusCode:            http.StatusConflict,
			ResponseBody:          clienttest.Fixture(t, "secret-reissue-required.json"),
		},
		clienttest.Expectation{
			Method:                http.MethodPost,
			RequestURI:            "/api/automation/v1/components/" + testComponentID + "/integration/rotate",
			RequireIdempotencyKey: true,
			IfMatch:               `"1"`,
			RequestBody:           []byte("{\"revoke_predecessor_immediately\":true}\n"),
			StatusCode:            http.StatusOK,
			ResponseBody:          rotationResponse,
		},
	)
	implementation, err := New(Config{
		BaseURL:           mock.URL(),
		Token:             testToken,
		HTTPClient:        mock.HTTPClient(),
		Retry:             RetryConfig{MaxAttempts: 2, MinDelay: time.Nanosecond, MaxDelay: time.Nanosecond},
		AllowInsecureHTTP: true,
	})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}

	_, err = implementation.CreateComponentIntegration(context.Background(), testComponentID, ComponentIntegrationCreateRequest{
		Source:    IntegrationSourceGrafana,
		SourceKey: "sequencer-commitment",
	}, MutationOptions{})
	recovery, ok := SecretReissueMetadataFromError(err)
	if !ok || recovery.Revision != 1 {
		t.Fatalf("create error = %v recovery = %#v", err, recovery)
	}
	rotated, err := implementation.RotateComponentIntegration(context.Background(), testComponentID, MutationOptions{
		Revision:                     recovery.Revision,
		RevokePredecessorImmediately: true,
	})
	if err != nil {
		t.Fatalf("rotate unreachable secret: %v", err)
	}
	if rotated.Secret == nil || rotated.Secret.VersionID != "00000000-0000-4000-8000-000000000402" {
		t.Fatalf("rotation result = %#v", rotated.Integration)
	}

	requests := mock.Requests()
	if requests[0].IdempotencyKey != requests[1].IdempotencyKey {
		t.Fatal("lost-response retry changed the original operation key")
	}
	if requests[2].IdempotencyKey == requests[1].IdempotencyKey {
		t.Fatal("recovery rotation reused the original operation key")
	}
}
