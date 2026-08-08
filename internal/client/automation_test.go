package client

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/sreforce/terraform-provider-pulse/internal/client/clienttest"
)

const (
	testToken       = "automation-test-token"
	testComponentID = "00000000-0000-4000-8000-000000000101"
)

func TestCreateComponentUsesCanonicalContract(t *testing.T) {
	t.Parallel()

	mock := clienttest.NewServer(t, testToken, clienttest.Expectation{
		Method:                http.MethodPost,
		RequestURI:            "/api/automation/v1/components",
		RequireIdempotencyKey: true,
		RequestBody:           []byte("{\"external_key\":\"main-net/core/citrea-sequencer\",\"kind\":\"external\",\"name\":\"Sequencer\",\"component_type_id\":\"00000000-0000-4000-8000-000000000201\",\"owner_team_id\":null,\"relevance_tag_ids\":[],\"filter_tag_ids\":[],\"alert_enabled\":false}\n"),
		StatusCode:            http.StatusCreated,
		ResponseBody:          clienttest.Fixture(t, "component.json"),
	})
	implementation := newMockClient(t, mock)

	result, err := implementation.CreateComponent(context.Background(), ComponentCreateRequest{
		ExternalKey:     "main-net/core/citrea-sequencer",
		Kind:            ComponentKindExternal,
		Name:            "Sequencer",
		ComponentTypeID: "00000000-0000-4000-8000-000000000201",
		RelevanceTagIDs: []string{},
		FilterTagIDs:    []string{},
	}, MutationOptions{})
	if err != nil {
		t.Fatalf("create component: %v", err)
	}
	if result.ID != testComponentID || result.ExternalKey != "main-net/core/citrea-sequencer" || result.State != ComponentStateUnknown {
		t.Fatalf("component = %#v", result)
	}
}

func TestComponentUpdateAndArchiveUseFreshKeysAndQuotedRevision(t *testing.T) {
	t.Parallel()

	updateBody := []byte("{\"name\":\"Sequencer\",\"component_type_id\":\"00000000-0000-4000-8000-000000000201\",\"owner_team_id\":null,\"relevance_tag_ids\":[],\"filter_tag_ids\":[],\"alert_enabled\":false}\n")
	mock := clienttest.NewServer(t, testToken,
		clienttest.Expectation{
			Method:                http.MethodPatch,
			RequestURI:            "/api/automation/v1/components/" + testComponentID,
			RequireIdempotencyKey: true,
			IfMatch:               `"7"`,
			RequestBody:           updateBody,
			StatusCode:            http.StatusOK,
			ResponseBody:          clienttest.Fixture(t, "component.json"),
		},
		clienttest.Expectation{
			Method:                http.MethodDelete,
			RequestURI:            "/api/automation/v1/components/" + testComponentID,
			RequireIdempotencyKey: true,
			IfMatch:               `"7"`,
			StatusCode:            http.StatusNoContent,
		},
	)
	implementation := newMockClient(t, mock)

	_, err := implementation.UpdateComponent(context.Background(), testComponentID, ComponentUpdateRequest{
		Name:            "Sequencer",
		ComponentTypeID: "00000000-0000-4000-8000-000000000201",
		RelevanceTagIDs: []string{},
		FilterTagIDs:    []string{},
	}, MutationOptions{Revision: 7})
	if err != nil {
		t.Fatalf("update component: %v", err)
	}
	if err := implementation.ArchiveComponent(context.Background(), testComponentID, MutationOptions{Revision: 7}); err != nil {
		t.Fatalf("archive component: %v", err)
	}

	requests := mock.Requests()
	if requests[0].IdempotencyKey == requests[1].IdempotencyKey {
		t.Fatal("separate provider operations reused an idempotency key")
	}
}

func TestRollupCreateReplaceAndDeletePreconditions(t *testing.T) {
	t.Parallel()

	rollupBody := []byte("{\"rules\":[{\"child_component_ids\":[\"00000000-0000-4000-8000-000000000101\"],\"when_child_yellow\":\"yellow\",\"when_child_red\":\"red\"}]}\n")
	parentID := "00000000-0000-4000-8000-000000000102"
	mock := clienttest.NewServer(t, testToken,
		clienttest.Expectation{
			Method:                http.MethodPut,
			RequestURI:            "/api/automation/v1/components/" + parentID + "/rollup",
			RequireIdempotencyKey: true,
			IfNoneMatch:           "*",
			RequestBody:           rollupBody,
			StatusCode:            http.StatusCreated,
			ResponseBody:          clienttest.Fixture(t, "rollup.json"),
		},
		clienttest.Expectation{
			Method:                http.MethodPut,
			RequestURI:            "/api/automation/v1/components/" + parentID + "/rollup",
			RequireIdempotencyKey: true,
			IfMatch:               `"3"`,
			RequestBody:           rollupBody,
			StatusCode:            http.StatusOK,
			ResponseBody:          clienttest.Fixture(t, "rollup.json"),
		},
		clienttest.Expectation{
			Method:                http.MethodDelete,
			RequestURI:            "/api/automation/v1/components/" + parentID + "/rollup",
			RequireIdempotencyKey: true,
			IfMatch:               `"3"`,
			StatusCode:            http.StatusNoContent,
		},
	)
	implementation := newMockClient(t, mock)
	payload := ComponentRollupReplaceRequest{Rules: []RollupRule{{
		ChildComponentIDs: []string{testComponentID},
		WhenChildYellow:   RollupEffectYellow,
		WhenChildRed:      RollupEffectRed,
	}}}

	if _, err := implementation.ReplaceComponentRollup(context.Background(), parentID, payload, MutationOptions{}); err != nil {
		t.Fatalf("create rollup: %v", err)
	}
	if _, err := implementation.ReplaceComponentRollup(context.Background(), parentID, payload, MutationOptions{Revision: 3}); err != nil {
		t.Fatalf("replace rollup: %v", err)
	}
	if err := implementation.DeleteComponentRollup(context.Background(), parentID, MutationOptions{Revision: 3}); err != nil {
		t.Fatalf("delete rollup: %v", err)
	}
}

func TestIntegrationIssueRotateAdoptAndDeleteContract(t *testing.T) {
	t.Parallel()

	createBody := []byte("{\"source\":\"grafana\",\"source_key\":\"sequencer-commitment\"}\n")
	rotateBody := []byte("{\"revoke_predecessor_immediately\":true}\n")
	mock := clienttest.NewServer(t, testToken,
		clienttest.Expectation{
			Method:                http.MethodPost,
			RequestURI:            "/api/automation/v1/components/" + testComponentID + "/integration",
			RequireIdempotencyKey: true,
			RequestBody:           createBody,
			StatusCode:            http.StatusCreated,
			ResponseBody:          clienttest.Fixture(t, "integration-issued.json"),
		},
		clienttest.Expectation{
			Method:                http.MethodPost,
			RequestURI:            "/api/automation/v1/components/" + testComponentID + "/integration/rotate",
			RequireIdempotencyKey: true,
			IfMatch:               `"1"`,
			RequestBody:           rotateBody,
			StatusCode:            http.StatusOK,
			ResponseBody:          clienttest.Fixture(t, "integration-issued.json"),
		},
		clienttest.Expectation{
			Method:                http.MethodPost,
			RequestURI:            "/api/automation/v1/components/" + testComponentID + "/integration/adopt",
			RequireIdempotencyKey: true,
			IfMatch:               `"1"`,
			StatusCode:            http.StatusOK,
			ResponseBody:          clienttest.Fixture(t, "integration-issued.json"),
		},
		clienttest.Expectation{
			Method:                http.MethodDelete,
			RequestURI:            "/api/automation/v1/components/" + testComponentID + "/integration",
			RequireIdempotencyKey: true,
			IfMatch:               `"1"`,
			StatusCode:            http.StatusNoContent,
		},
	)
	implementation := newMockClient(t, mock)

	issued, err := implementation.CreateComponentIntegration(context.Background(), testComponentID, ComponentIntegrationCreateRequest{
		Source:    IntegrationSourceGrafana,
		SourceKey: "sequencer-commitment",
	}, MutationOptions{})
	if err != nil {
		t.Fatalf("create integration: %v", err)
	}
	if issued.Integration.SourceKey != "sequencer-commitment" || issued.Secret == nil || issued.Secret.Value == "" {
		t.Fatalf("issued integration = %#v", issued.Integration)
	}
	if _, err := implementation.RotateComponentIntegration(context.Background(), testComponentID, MutationOptions{Revision: 1, RevokePredecessorImmediately: true}); err != nil {
		t.Fatalf("rotate integration: %v", err)
	}
	if _, err := implementation.AdoptComponentIntegration(context.Background(), testComponentID, MutationOptions{Revision: 1}); err != nil {
		t.Fatalf("adopt integration: %v", err)
	}
	if err := implementation.DeleteComponentIntegration(context.Background(), testComponentID, MutationOptions{Revision: 1}); err != nil {
		t.Fatalf("delete integration: %v", err)
	}

	requests := mock.Requests()
	seen := make(map[string]struct{}, len(requests))
	for _, request := range requests {
		if _, duplicate := seen[request.IdempotencyKey]; duplicate {
			t.Fatal("different integration operations reused an idempotency key")
		}
		seen[request.IdempotencyKey] = struct{}{}
	}
}

func TestDomainRetryReusesOneIdempotencyKey(t *testing.T) {
	t.Parallel()

	body := []byte("{\"external_key\":\"main-net/core/citrea-sequencer\",\"kind\":\"external\",\"name\":\"Sequencer\",\"component_type_id\":\"00000000-0000-4000-8000-000000000201\",\"owner_team_id\":null,\"relevance_tag_ids\":[],\"filter_tag_ids\":[],\"alert_enabled\":false}\n")
	mock := clienttest.NewServer(t, testToken,
		clienttest.Expectation{
			Method:                http.MethodPost,
			RequestURI:            "/api/automation/v1/components",
			RequireIdempotencyKey: true,
			RequestBody:           body,
			StatusCode:            http.StatusServiceUnavailable,
			ResponseBody:          []byte(`{"code":"service_unavailable","error":"temporary"}`),
		},
		clienttest.Expectation{
			Method:                http.MethodPost,
			RequestURI:            "/api/automation/v1/components",
			RequireIdempotencyKey: true,
			RequestBody:           body,
			StatusCode:            http.StatusCreated,
			ResponseBody:          clienttest.Fixture(t, "component.json"),
		},
	)
	implementation, err := New(Config{
		BaseURL:    mock.URL(),
		Token:      testToken,
		HTTPClient: mock.HTTPClient(),
		Retry:      RetryConfig{MaxAttempts: 2, MinDelay: time.Nanosecond, MaxDelay: time.Nanosecond},
	})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	_, err = implementation.CreateComponent(context.Background(), ComponentCreateRequest{
		ExternalKey:     "main-net/core/citrea-sequencer",
		Kind:            ComponentKindExternal,
		Name:            "Sequencer",
		ComponentTypeID: "00000000-0000-4000-8000-000000000201",
		RelevanceTagIDs: []string{},
		FilterTagIDs:    []string{},
	}, MutationOptions{})
	if err != nil {
		t.Fatalf("create component: %v", err)
	}
	requests := mock.Requests()
	if requests[0].IdempotencyKey != requests[1].IdempotencyKey {
		t.Fatal("HTTP retry changed the operation idempotency key")
	}
}

func TestCatalogPathsAndDirectEnvelopes(t *testing.T) {
	t.Parallel()

	mock := clienttest.NewServer(t, testToken,
		clienttest.Expectation{Method: http.MethodGet, RequestURI: "/api/automation/v1/organization", StatusCode: http.StatusOK, ResponseBody: []byte(`{"id":"org-id","name":"Chainway","slug":"chainway"}`)},
		clienttest.Expectation{Method: http.MethodGet, RequestURI: "/api/automation/v1/component-types?cursor=a%2B%2F&limit=25", StatusCode: http.StatusOK, ResponseBody: []byte(`{"items":[{"id":"type-id","name":"Service","green_label":"Operational","yellow_label":"Degraded","red_label":"Down","unknown_label":"Unknown"}],"next_cursor":""}`)},
	)
	implementation := newMockClient(t, mock)

	organization, err := implementation.CurrentOrganization(context.Background())
	if err != nil || organization.Slug != "chainway" {
		t.Fatalf("organization = %#v err = %v", organization, err)
	}
	page, err := implementation.ListComponentTypes(context.Background(), ListOptions{Cursor: "a+/", Limit: 25})
	if err != nil || len(page.Items) != 1 || page.Items[0].Name != "Service" || page.NextCursor != "" {
		t.Fatalf("component type page = %#v err = %v", page, err)
	}
}

func TestMutationOptionsFailClosedBeforeRequest(t *testing.T) {
	t.Parallel()

	implementation, err := New(Config{BaseURL: "https://pulse.example.com", Token: testToken, Retry: RetryConfig{MaxAttempts: 1}})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	if _, err := implementation.UpdateComponent(context.Background(), testComponentID, ComponentUpdateRequest{}, MutationOptions{}); err == nil {
		t.Fatal("update accepted a missing revision")
	}
	if _, err := implementation.ReplaceComponentRollup(context.Background(), testComponentID, ComponentRollupReplaceRequest{}, MutationOptions{Revision: -1}); err == nil {
		t.Fatal("rollup replacement accepted a negative revision")
	}
	if _, err := implementation.AdoptComponentIntegration(context.Background(), testComponentID, MutationOptions{Revision: 1, RevokePredecessorImmediately: true}); err == nil {
		t.Fatal("adoption accepted client-controlled predecessor revocation")
	}
}

func newMockClient(t *testing.T, mock *clienttest.Server) *Client {
	t.Helper()
	implementation, err := New(Config{
		BaseURL:    mock.URL(),
		Token:      testToken,
		HTTPClient: mock.HTTPClient(),
		Retry:      RetryConfig{MaxAttempts: 1},
	})
	if err != nil {
		t.Fatalf("create client: %v", err)
	}
	return implementation
}
