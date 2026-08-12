package provider

import (
	"context"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/sreforce/terraform-provider-pulse/internal/client"
	"github.com/sreforce/terraform-provider-pulse/internal/client/clienttest"
)

const (
	componentProtocolToken = "component-protocol-token"
	componentProtocolID    = "00000000-0000-4000-8000-000000000101"
	componentProtocolType  = "00000000-0000-4000-8000-000000000201"
)

func TestComponentResourceProtocolLifecycle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	createBody := []byte("{\"external_key\":\"main-net/core/citrea-sequencer\",\"kind\":\"external\",\"name\":\"Sequencer\",\"component_type_id\":\"" + componentProtocolType + "\",\"owner_team_id\":null,\"relevance_tag_ids\":[],\"filter_tag_ids\":[],\"alert_enabled\":false}\n")
	updateBody := []byte("{\"name\":\"Sequencer v2\",\"component_type_id\":\"" + componentProtocolType + "\",\"owner_team_id\":null,\"relevance_tag_ids\":[],\"filter_tag_ids\":[],\"alert_enabled\":false}\n")
	readBody := []byte(`{"id":"` + componentProtocolID + `","external_key":"main-net/core/citrea-sequencer","kind":"external","name":"Sequencer","component_type_id":"` + componentProtocolType + `","owner_team_id":null,"relevance_tag_ids":[],"filter_tag_ids":[],"alert_enabled":false,"state":"yellow","state_reason":"Warning signal active.","revision":7,"archived_at":null}`)
	updatedBody := []byte(`{"id":"` + componentProtocolID + `","external_key":"main-net/core/citrea-sequencer","kind":"external","name":"Sequencer v2","component_type_id":"` + componentProtocolType + `","owner_team_id":null,"relevance_tag_ids":[],"filter_tag_ids":[],"alert_enabled":false,"state":"red","state_reason":"Critical signal active.","revision":8,"archived_at":null}`)

	mock := clienttest.NewServer(t, componentProtocolToken,
		clienttest.Expectation{
			Method:                http.MethodPost,
			RequestURI:            "/api/automation/v1/components",
			RequireIdempotencyKey: true,
			RequestBody:           createBody,
			StatusCode:            http.StatusCreated,
			ResponseBody:          clienttest.Fixture(t, "component.json"),
		},
		clienttest.Expectation{
			Method:       http.MethodGet,
			RequestURI:   "/api/automation/v1/components/" + componentProtocolID,
			StatusCode:   http.StatusOK,
			ResponseBody: readBody,
		},
		clienttest.Expectation{
			Method:                http.MethodPatch,
			RequestURI:            "/api/automation/v1/components/" + componentProtocolID,
			RequireIdempotencyKey: true,
			IfMatch:               `"7"`,
			RequestBody:           updateBody,
			StatusCode:            http.StatusOK,
			ResponseBody:          updatedBody,
		},
		clienttest.Expectation{
			Method:                http.MethodDelete,
			RequestURI:            "/api/automation/v1/components/" + componentProtocolID,
			RequireIdempotencyKey: true,
			IfMatch:               `"8"`,
			StatusCode:            http.StatusNoContent,
		},
	)
	implementation := configuredComponentProtocolResource(t, mock)
	componentSchema := componentTestSchema(t, implementation)

	createResponse := resource.CreateResponse{State: tfsdk.State{Schema: componentSchema}}
	implementation.Create(ctx, resource.CreateRequest{
		Plan: componentTestPlan(t, componentSchema, componentProtocolModel()),
	}, &createResponse)
	if createResponse.Diagnostics.HasError() {
		t.Fatalf("create diagnostics: %v", createResponse.Diagnostics)
	}

	var created componentResourceModel
	if diagnostics := createResponse.State.Get(ctx, &created); diagnostics.HasError() {
		t.Fatalf("read created state: %v", diagnostics)
	}
	if got, want := created.ID.ValueString(), componentProtocolID; got != want {
		t.Fatalf("created id = %q, want %q", got, want)
	}
	if got, want := created.ConfigurationRevision.ValueInt64(), int64(7); got != want {
		t.Fatalf("created revision = %d, want %d", got, want)
	}

	readResponse := resource.ReadResponse{State: tfsdk.State{Schema: componentSchema}}
	implementation.Read(ctx, resource.ReadRequest{State: createResponse.State}, &readResponse)
	if readResponse.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", readResponse.Diagnostics)
	}

	var current componentResourceModel
	if diagnostics := readResponse.State.Get(ctx, &current); diagnostics.HasError() {
		t.Fatalf("read refreshed state: %v", diagnostics)
	}
	if got, want := current.State.ValueString(), "yellow"; got != want {
		t.Fatalf("read runtime state = %q, want %q", got, want)
	}

	planned := current
	planned.Name = types.StringValue("Sequencer v2")
	planned.State = types.StringUnknown()
	planned.ConfigurationRevision = types.Int64Unknown()
	updateResponse := resource.UpdateResponse{State: tfsdk.State{Schema: componentSchema}}
	implementation.Update(ctx, resource.UpdateRequest{
		Plan:  componentTestPlan(t, componentSchema, planned),
		State: readResponse.State,
	}, &updateResponse)
	if updateResponse.Diagnostics.HasError() {
		t.Fatalf("update diagnostics: %v", updateResponse.Diagnostics)
	}

	var updated componentResourceModel
	if diagnostics := updateResponse.State.Get(ctx, &updated); diagnostics.HasError() {
		t.Fatalf("read updated state: %v", diagnostics)
	}
	if got, want := updated.Name.ValueString(), "Sequencer v2"; got != want {
		t.Fatalf("updated name = %q, want %q", got, want)
	}
	if got, want := updated.ConfigurationRevision.ValueInt64(), int64(8); got != want {
		t.Fatalf("updated revision = %d, want %d", got, want)
	}

	deleteResponse := resource.DeleteResponse{State: tfsdk.State{Schema: componentSchema}}
	implementation.Delete(ctx, resource.DeleteRequest{State: updateResponse.State}, &deleteResponse)
	if deleteResponse.Diagnostics.HasError() {
		t.Fatalf("archive diagnostics: %v", deleteResponse.Diagnostics)
	}

	requests := mock.Requests()
	mutationKeys := []string{requests[0].IdempotencyKey, requests[2].IdempotencyKey, requests[3].IdempotencyKey}
	for index, key := range mutationKeys {
		if key == "" {
			t.Fatalf("mutation %d omitted its idempotency key", index)
		}
		for prior := 0; prior < index; prior++ {
			if mutationKeys[prior] == key {
				t.Fatal("separate component mutations reused an idempotency key")
			}
		}
	}
}

func TestComponentResourceProtocolImportThenRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	mock := clienttest.NewServer(t, componentProtocolToken, clienttest.Expectation{
		Method:       http.MethodGet,
		RequestURI:   "/api/automation/v1/components/" + componentProtocolID,
		StatusCode:   http.StatusOK,
		ResponseBody: clienttest.Fixture(t, "component.json"),
	})
	implementation := configuredComponentProtocolResource(t, mock)
	componentSchema := componentTestSchema(t, implementation)

	importResponse := resource.ImportStateResponse{
		State: componentTestState(t, componentSchema, componentImportStateModel()),
	}
	implementation.ImportState(ctx, resource.ImportStateRequest{ID: componentProtocolID}, &importResponse)
	if importResponse.Diagnostics.HasError() {
		t.Fatalf("import diagnostics: %v", importResponse.Diagnostics)
	}

	readResponse := resource.ReadResponse{State: tfsdk.State{Schema: componentSchema}}
	implementation.Read(ctx, resource.ReadRequest{State: importResponse.State}, &readResponse)
	if readResponse.Diagnostics.HasError() {
		t.Fatalf("import read diagnostics: %v", readResponse.Diagnostics)
	}

	var imported componentResourceModel
	if diagnostics := readResponse.State.Get(ctx, &imported); diagnostics.HasError() {
		t.Fatalf("read imported state: %v", diagnostics)
	}
	if got, want := imported.ExternalKey.ValueString(), "main-net/core/citrea-sequencer"; got != want {
		t.Fatalf("imported external key = %q, want %q", got, want)
	}
	if got, want := imported.Kind.ValueString(), "external"; got != want {
		t.Fatalf("imported kind = %q, want %q", got, want)
	}
	if got, want := imported.ConfigurationRevision.ValueInt64(), int64(7); got != want {
		t.Fatalf("imported revision = %d, want %d", got, want)
	}
	if imported.OwnerTeamID.IsNull() == false || len(imported.RelevanceTagIDs.Elements()) != 0 || len(imported.FilterTagIDs.Elements()) != 0 {
		t.Fatalf("imported optional references = owner %#v relevance %#v filter %#v", imported.OwnerTeamID, imported.RelevanceTagIDs, imported.FilterTagIDs)
	}
}

func configuredComponentProtocolResource(t *testing.T, mock *clienttest.Server) *ComponentResource {
	t.Helper()
	apiClient, err := client.New(client.Config{
		BaseURL:           mock.URL(),
		Token:             componentProtocolToken,
		HTTPClient:        mock.HTTPClient(),
		Retry:             client.RetryConfig{MaxAttempts: 1},
		AllowInsecureHTTP: true,
	})
	if err != nil {
		t.Fatalf("create Pulse client: %v", err)
	}

	implementation := &ComponentResource{}
	var response resource.ConfigureResponse
	implementation.Configure(context.Background(), resource.ConfigureRequest{ProviderData: apiClient}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("configure component resource: %v", response.Diagnostics)
	}
	return implementation
}

func componentProtocolModel() componentResourceModel {
	return componentResourceModel{
		ID:                    types.StringUnknown(),
		ExternalKey:           types.StringValue("main-net/core/citrea-sequencer"),
		Kind:                  types.StringValue("external"),
		Name:                  types.StringValue("Sequencer"),
		ComponentTypeID:       types.StringValue(componentProtocolType),
		OwnerTeamID:           types.StringNull(),
		RelevanceTagIDs:       types.SetValueMust(types.StringType, []attr.Value{}),
		FilterTagIDs:          types.SetValueMust(types.StringType, []attr.Value{}),
		AlertEnabled:          types.BoolValue(false),
		State:                 types.StringUnknown(),
		ConfigurationRevision: types.Int64Unknown(),
	}
}
