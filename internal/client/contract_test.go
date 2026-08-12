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
		Method:                http.MethodPut,
		RequestURI:            "/api/automation/v1/components/" + testComponentID + "/integrations/grafana",
		RequireIdempotencyKey: true,
		RequestBody:           []byte("{}\n"),
		StatusCode:            http.StatusCreated,
		ResponseBody:          []byte(`{}`),
	})
	implementation := newMockClient(t, mock)

	result, err := implementation.UpsertComponentIntegration(context.Background(), testComponentID, IntegrationProviderGrafana, ComponentIntegrationUpsertRequest{}, MutationOptions{})
	if !IsContractError(err) {
		t.Fatalf("error = %v, want contract error", err)
	}
	if result.Secret != nil || result.Integration.ComponentID != "" {
		t.Fatalf("invalid success data escaped: %#v", result)
	}
}

func TestIntegrationMutationRejectsMismatchedSecretVersionWithoutLeaking(t *testing.T) {
	t.Parallel()

	response := []byte(`{
		"integration": {
			"component_id": "00000000-0000-4000-8000-000000000101",
			"provider": "grafana",
			"endpoint": "https://pulse.example.com/webhooks/components/00000000-0000-4000-8000-000000000101/grafana",
			"lifecycle_owner": "automation",
			"status": "active",
			"credential_version_id": "expected-version",
			"revision": 1,
			"archived_at": null
		},
		"secret": {"value":"plaintext-must-not-leak","version_id":"wrong-version"}
	}`)
	mock := clienttest.NewServer(t, testToken, clienttest.Expectation{
		Method:                http.MethodPut,
		RequestURI:            "/api/automation/v1/components/" + testComponentID + "/integrations/grafana",
		RequireIdempotencyKey: true,
		StatusCode:            http.StatusCreated,
		ResponseBody:          response,
	})
	implementation := newMockClient(t, mock)

	result, err := implementation.UpsertComponentIntegration(context.Background(), testComponentID, IntegrationProviderGrafana, ComponentIntegrationUpsertRequest{}, MutationOptions{})
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
		ComponentID:         testComponentID,
		Provider:            IntegrationProviderGrafana,
		Endpoint:            "http://pulse.example.com/webhooks/components/" + testComponentID + "/grafana",
		LifecycleOwner:      IntegrationLifecycleOwnerAutomation,
		Status:              IntegrationStatusActive,
		CredentialVersionID: "00000000-0000-4000-8000-000000000401",
		Revision:            1,
	}
	if err := validateIntegration(integration, testComponentID, IntegrationProviderGrafana, true); !IsContractError(err) {
		t.Fatalf("remote plaintext integration endpoint error = %v, want contract error", err)
	}

	integration.Endpoint = "http://127.0.0.1:8080/webhooks/components/" + testComponentID + "/grafana"
	if err := validateIntegration(integration, testComponentID, IntegrationProviderGrafana, false); !IsContractError(err) {
		t.Fatalf("unapproved loopback endpoint error = %v, want contract error", err)
	}
	if err := validateIntegration(integration, testComponentID, IntegrationProviderGrafana, true); err != nil {
		t.Fatalf("explicit loopback development endpoint was rejected: %v", err)
	}
}

func TestLostIntegrationSecretRecoverySequence(t *testing.T) {
	t.Parallel()

	createBody := []byte("{}\n")
	rotationResponse := []byte(`{
		"integration": {
			"component_id": "00000000-0000-4000-8000-000000000101",
			"provider": "grafana",
			"endpoint": "https://pulse.example.com/webhooks/components/00000000-0000-4000-8000-000000000101/grafana",
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
			Method:                http.MethodPut,
			RequestURI:            "/api/automation/v1/components/" + testComponentID + "/integrations/grafana",
			RequireIdempotencyKey: true,
			RequestBody:           createBody,
			StatusCode:            http.StatusCreated,
			ResponseBody:          []byte(`{"integration":{"component_id":"00000000-0000-4000-8000-000000000101"},"secret":{"value":"unreachable"`),
		},
		clienttest.Expectation{
			Method:                http.MethodPut,
			RequestURI:            "/api/automation/v1/components/" + testComponentID + "/integrations/grafana",
			RequireIdempotencyKey: true,
			RequestBody:           createBody,
			StatusCode:            http.StatusConflict,
			ResponseBody:          clienttest.Fixture(t, "secret-reissue-required.json"),
		},
		clienttest.Expectation{
			Method:                http.MethodPost,
			RequestURI:            "/api/automation/v1/components/" + testComponentID + "/integrations/grafana/rotate",
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

	_, err = implementation.UpsertComponentIntegration(context.Background(), testComponentID, IntegrationProviderGrafana, ComponentIntegrationUpsertRequest{}, MutationOptions{})
	recovery, ok := SecretReissueMetadataFromError(err)
	if !ok || recovery.Revision != 1 {
		t.Fatalf("create error = %v recovery = %#v", err, recovery)
	}
	rotated, err := implementation.RotateComponentIntegration(context.Background(), testComponentID, IntegrationProviderGrafana, MutationOptions{
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

func TestRollupResponseDoesNotAdoptDynamicIngestionChildren(t *testing.T) {
	t.Parallel()
	parentID := "00000000-0000-4000-8000-000000000102"
	mock := clienttest.NewServer(t, testToken, clienttest.Expectation{
		Method:     http.MethodGet,
		RequestURI: "/api/automation/v1/components/" + parentID + "/rollup",
		StatusCode: http.StatusOK,
		ResponseBody: []byte(`{
			"parent_component_id":"00000000-0000-4000-8000-000000000102",
			"rules":[],
			"revision":7,
			"dynamic_ingestion_children":["00000000-0000-4000-8000-000000000199"]
		}`),
	})
	implementation := newMockClient(t, mock)
	rollup, err := implementation.GetComponentRollup(context.Background(), parentID)
	if err != nil {
		t.Fatalf("read static rollup rules: %v", err)
	}
	if rollup.Rules == nil || len(rollup.Rules) != 0 || rollup.Revision != 7 {
		t.Fatalf("static rollup = %#v", rollup)
	}
}
