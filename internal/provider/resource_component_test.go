package provider

import (
	"context"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/sreforce/terraform-provider-pulse/internal/client"
)

type fakeComponentAPI struct {
	create  func(context.Context, client.ComponentCreateRequest, client.MutationOptions) (client.Component, error)
	get     func(context.Context, string) (client.Component, error)
	update  func(context.Context, string, client.ComponentUpdateRequest, client.MutationOptions) (client.Component, error)
	archive func(context.Context, string, client.MutationOptions) error
}

func (f *fakeComponentAPI) CreateComponent(ctx context.Context, request client.ComponentCreateRequest, options client.MutationOptions) (client.Component, error) {
	return f.create(ctx, request, options)
}

func (f *fakeComponentAPI) GetComponent(ctx context.Context, id string) (client.Component, error) {
	return f.get(ctx, id)
}

func (f *fakeComponentAPI) UpdateComponent(ctx context.Context, id string, request client.ComponentUpdateRequest, options client.MutationOptions) (client.Component, error) {
	return f.update(ctx, id, request, options)
}

func (f *fakeComponentAPI) ArchiveComponent(ctx context.Context, id string, options client.MutationOptions) error {
	return f.archive(ctx, id, options)
}

func TestComponentResourceSchemaSeparatesConfigurationAndRuntimeState(t *testing.T) {
	t.Parallel()

	implementation := &ComponentResource{}
	var response resource.SchemaResponse
	implementation.Schema(context.Background(), resource.SchemaRequest{}, &response)
	if diagnostics := response.Schema.ValidateImplementation(context.Background()); diagnostics.HasError() {
		t.Fatalf("schema implementation diagnostics: %v", diagnostics)
	}

	externalKey, ok := response.Schema.Attributes["external_key"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("external_key type = %T, want schema.StringAttribute", response.Schema.Attributes["external_key"])
	}
	if !externalKey.Required || len(externalKey.PlanModifiers) == 0 {
		t.Fatal("external_key must be required and immutable")
	}

	kind, ok := response.Schema.Attributes["kind"].(schema.StringAttribute)
	if !ok || !kind.Required || len(kind.PlanModifiers) == 0 {
		t.Fatal("kind must be a required immutable string")
	}

	state, ok := response.Schema.Attributes["state"].(schema.StringAttribute)
	if !ok || !state.Computed || state.Optional || state.Required {
		t.Fatal("runtime state must be computed-only")
	}

	revision, ok := response.Schema.Attributes["configuration_revision"].(schema.Int64Attribute)
	if !ok || !revision.Computed || revision.Optional || revision.Required {
		t.Fatal("configuration revision must be computed-only")
	}
}

func TestComponentKindValidator(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"external", "rollup"} {
		var response validator.StringResponse
		componentKindValidator{}.ValidateString(context.Background(), validator.StringRequest{ConfigValue: types.StringValue(value)}, &response)
		if response.Diagnostics.HasError() {
			t.Fatalf("kind %q returned diagnostics: %v", value, response.Diagnostics)
		}
	}

	var response validator.StringResponse
	componentKindValidator{}.ValidateString(context.Background(), validator.StringRequest{ConfigValue: types.StringValue("service")}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("unsupported kind must be rejected")
	}
}

func TestComponentResourceRejectsKindChangeForSameExternalKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	implementation := &ComponentResource{}
	componentSchema := componentTestSchema(t, implementation)

	current := componentTestModel()
	current.ID = types.StringValue("component-uuid")
	current.State = types.StringValue("unknown")
	current.ConfigurationRevision = types.Int64Value(1)
	planned := current
	planned.ID = types.StringUnknown()
	planned.Kind = types.StringValue("rollup")

	response := resource.ModifyPlanResponse{}
	implementation.ModifyPlan(ctx, resource.ModifyPlanRequest{
		State: componentTestState(t, componentSchema, current),
		Plan:  componentTestPlan(t, componentSchema, planned),
	}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("kind-only replacement must be rejected because external_key restores the same immutable component")
	}

	planned.ExternalKey = types.StringValue("main-net/core/sequencer/commitment-rollup")
	response = resource.ModifyPlanResponse{}
	implementation.ModifyPlan(ctx, resource.ModifyPlanRequest{
		State: componentTestState(t, componentSchema, current),
		Plan:  componentTestPlan(t, componentSchema, planned),
	}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("kind change with a new external_key must be allowed: %v", response.Diagnostics)
	}
	var replacement componentResourceModel
	if diagnostics := response.Plan.Get(ctx, &replacement); diagnostics.HasError() {
		t.Fatalf("read replacement plan: %v", diagnostics)
	}
	if !replacement.ID.IsUnknown() {
		t.Fatalf("replacement id = %#v, want unknown", replacement.ID)
	}
}

func TestComponentResourcePreservesIDForMutableUpdate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	implementation := &ComponentResource{}
	componentSchema := componentTestSchema(t, implementation)

	current := componentTestModel()
	current.ID = types.StringValue("component-uuid")
	current.State = types.StringValue("yellow")
	current.ConfigurationRevision = types.Int64Value(3)
	planned := current
	planned.ID = types.StringUnknown()
	planned.Name = types.StringValue("Updated display name")
	planned.State = types.StringUnknown()
	planned.ConfigurationRevision = types.Int64Unknown()

	response := resource.ModifyPlanResponse{}
	implementation.ModifyPlan(ctx, resource.ModifyPlanRequest{
		State: componentTestState(t, componentSchema, current),
		Plan:  componentTestPlan(t, componentSchema, planned),
	}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("modify plan diagnostics: %v", response.Diagnostics)
	}
	var modified componentResourceModel
	if diagnostics := response.Plan.Get(ctx, &modified); diagnostics.HasError() {
		t.Fatalf("read modified plan: %v", diagnostics)
	}
	if got, want := modified.ID.ValueString(), "component-uuid"; got != want {
		t.Fatalf("mutable update id = %q, want %q", got, want)
	}
}

func TestComponentResourceCreateUsesExternalKeyAndRemoteIdentity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	ownerTeamID := "team-platform"
	remote := client.Component{
		ID:              "component-uuid",
		ExternalKey:     "main-net/core/sequencer/commitment",
		Kind:            "external",
		Name:            "Sequencer commitment",
		ComponentTypeID: "type-service",
		OwnerTeamID:     &ownerTeamID,
		RelevanceTagIDs: []string{"tag-production", "tag-citrea"},
		FilterTagIDs:    []string{"tag-core"},
		AlertEnabled:    false,
		State:           "unknown",
		Revision:        1,
	}

	var requests []client.ComponentCreateRequest
	fake := &fakeComponentAPI{
		create: func(_ context.Context, request client.ComponentCreateRequest, option client.MutationOptions) (client.Component, error) {
			requests = append(requests, request)
			if option.Revision != 0 {
				t.Fatalf("create revision = %d, want 0", option.Revision)
			}
			return remote, nil
		},
	}
	implementation := &ComponentResource{client: fake}
	componentSchema := componentTestSchema(t, implementation)
	planModel := componentTestModel()

	for range 2 {
		request := resource.CreateRequest{Plan: componentTestPlan(t, componentSchema, planModel)}
		response := resource.CreateResponse{State: tfsdk.State{Schema: componentSchema}}
		implementation.Create(ctx, request, &response)
		if response.Diagnostics.HasError() {
			t.Fatalf("create diagnostics: %v", response.Diagnostics)
		}

		var state componentResourceModel
		if diagnostics := response.State.Get(ctx, &state); diagnostics.HasError() {
			t.Fatalf("read create state: %v", diagnostics)
		}
		if got, want := state.ID.ValueString(), remote.ID; got != want {
			t.Fatalf("state id = %q, want %q", got, want)
		}
		if got, want := state.State.ValueString(), "unknown"; got != want {
			t.Fatalf("runtime state = %q, want %q", got, want)
		}
		if got, want := state.ConfigurationRevision.ValueInt64(), int64(1); got != want {
			t.Fatalf("revision = %d, want %d", got, want)
		}
	}

	if got, want := requests[0].RelevanceTagIDs, []string{"tag-citrea", "tag-production"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("canonical relevance tag ids = %#v, want %#v", got, want)
	}
	if got, want := requests[0].FilterTagIDs, []string{"tag-core"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("canonical filter tag ids = %#v, want %#v", got, want)
	}
	if requests[0].OwnerTeamID == nil || *requests[0].OwnerTeamID != ownerTeamID {
		t.Fatalf("owner team id = %#v, want %q", requests[0].OwnerTeamID, ownerTeamID)
	}
}

func TestComponentResourceCreateRejectsMismatchedRemoteIdentity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fake := &fakeComponentAPI{
		create: func(_ context.Context, request client.ComponentCreateRequest, _ client.MutationOptions) (client.Component, error) {
			return client.Component{
				ID:              "unrelated-component",
				ExternalKey:     request.ExternalKey + "-other",
				Kind:            request.Kind,
				Name:            request.Name,
				ComponentTypeID: request.ComponentTypeID,
				State:           "unknown",
				Revision:        1,
			}, nil
		},
	}
	implementation := &ComponentResource{client: fake}
	componentSchema := componentTestSchema(t, implementation)
	response := resource.CreateResponse{State: tfsdk.State{Schema: componentSchema}}
	implementation.Create(ctx, resource.CreateRequest{Plan: componentTestPlan(t, componentSchema, componentTestModel())}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("create must reject a response for a different immutable external identity")
	}
}

func TestComponentResourceUpdateUsesPriorRevisionAndFullConfiguration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	current := componentTestModel()
	current.ID = types.StringValue("component-uuid")
	current.State = types.StringValue("yellow")
	current.ConfigurationRevision = types.Int64Value(7)

	planned := current
	planned.Name = types.StringValue("Sequencer alerts")
	planned.OwnerTeamID = types.StringNull()
	planned.RelevanceTagIDs = types.SetValueMust(types.StringType, []attr.Value{types.StringValue("tag-production")})
	planned.FilterTagIDs = types.SetValueMust(types.StringType, []attr.Value{types.StringValue("tag-core")})
	planned.AlertEnabled = types.BoolValue(true)
	planned.ConfigurationRevision = types.Int64Unknown()

	remote := client.Component{
		ID:              "component-uuid",
		ExternalKey:     planned.ExternalKey.ValueString(),
		Kind:            client.ComponentKind(planned.Kind.ValueString()),
		Name:            planned.Name.ValueString(),
		ComponentTypeID: planned.ComponentTypeID.ValueString(),
		RelevanceTagIDs: []string{"tag-production"},
		FilterTagIDs:    []string{"tag-core"},
		AlertEnabled:    true,
		State:           "red",
		Revision:        8,
	}

	var gotID string
	var gotRequest client.ComponentUpdateRequest
	var gotOptions client.MutationOptions
	fake := &fakeComponentAPI{
		update: func(_ context.Context, id string, request client.ComponentUpdateRequest, options client.MutationOptions) (client.Component, error) {
			gotID, gotRequest, gotOptions = id, request, options
			return remote, nil
		},
	}
	implementation := &ComponentResource{client: fake}
	componentSchema := componentTestSchema(t, implementation)
	response := resource.UpdateResponse{State: tfsdk.State{Schema: componentSchema}}
	implementation.Update(ctx, resource.UpdateRequest{
		Plan:  componentTestPlan(t, componentSchema, planned),
		State: componentTestState(t, componentSchema, current),
	}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("update diagnostics: %v", response.Diagnostics)
	}

	if gotID != "component-uuid" {
		t.Fatalf("update id = %q, want component-uuid", gotID)
	}
	if gotOptions.Revision != 7 {
		t.Fatalf("update options = %#v, want prior revision", gotOptions)
	}
	if gotRequest.Name != "Sequencer alerts" || gotRequest.OwnerTeamID != nil || !gotRequest.AlertEnabled {
		t.Fatalf("update request = %#v", gotRequest)
	}

	var state componentResourceModel
	if diagnostics := response.State.Get(ctx, &state); diagnostics.HasError() {
		t.Fatalf("read update state: %v", diagnostics)
	}
	if got, want := state.ConfigurationRevision.ValueInt64(), int64(8); got != want {
		t.Fatalf("revision = %d, want %d", got, want)
	}
	if got, want := state.State.ValueString(), "red"; got != want {
		t.Fatalf("runtime state = %q, want %q", got, want)
	}
}

func TestComponentResourceReadObservesRuntimeStateWithoutChangingConfiguration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	ownerTeamID := "team-platform"
	remote := client.Component{
		ID:              "component-uuid",
		ExternalKey:     "main-net/core/sequencer/commitment",
		Kind:            "external",
		Name:            "Sequencer commitment",
		ComponentTypeID: "type-service",
		OwnerTeamID:     &ownerTeamID,
		RelevanceTagIDs: []string{"tag-production"},
		FilterTagIDs:    []string{"tag-core"},
		AlertEnabled:    false,
		State:           "red",
		Revision:        4,
	}
	fake := &fakeComponentAPI{
		get: func(_ context.Context, id string) (client.Component, error) {
			if id != remote.ID {
				t.Fatalf("read id = %q, want %q", id, remote.ID)
			}
			return remote, nil
		},
	}
	implementation := &ComponentResource{client: fake}
	componentSchema := componentTestSchema(t, implementation)
	current, diagnostics := componentModelFromRemote(ctx, remote)
	if diagnostics.HasError() {
		t.Fatalf("build current state: %v", diagnostics)
	}

	firstResponse := resource.ReadResponse{State: tfsdk.State{Schema: componentSchema}}
	implementation.Read(ctx, resource.ReadRequest{State: componentTestState(t, componentSchema, current)}, &firstResponse)
	if firstResponse.Diagnostics.HasError() {
		t.Fatalf("first read diagnostics: %v", firstResponse.Diagnostics)
	}

	remote.State = "yellow"
	secondResponse := resource.ReadResponse{State: tfsdk.State{Schema: componentSchema}}
	implementation.Read(ctx, resource.ReadRequest{State: firstResponse.State}, &secondResponse)
	if secondResponse.Diagnostics.HasError() {
		t.Fatalf("second read diagnostics: %v", secondResponse.Diagnostics)
	}

	var refreshed componentResourceModel
	if diagnostics := secondResponse.State.Get(ctx, &refreshed); diagnostics.HasError() {
		t.Fatalf("read refreshed state: %v", diagnostics)
	}
	if got, want := refreshed.State.ValueString(), "yellow"; got != want {
		t.Fatalf("runtime state = %q, want %q", got, want)
	}
	if refreshed.ExternalKey != current.ExternalKey || refreshed.Name != current.Name || refreshed.ConfigurationRevision != current.ConfigurationRevision {
		t.Fatalf("runtime refresh changed managed configuration: before=%#v after=%#v", current, refreshed)
	}
}

func TestComponentResourceReadRemovesNotFoundComponent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	current := componentTestModel()
	current.ID = types.StringValue("component-uuid")
	current.State = types.StringValue("unknown")
	current.ConfigurationRevision = types.Int64Value(2)

	fake := &fakeComponentAPI{
		get: func(context.Context, string) (client.Component, error) {
			return client.Component{}, &client.ResponseError{StatusCode: 404, Code: client.ErrorCodeNotFound}
		},
	}
	implementation := &ComponentResource{client: fake}
	componentSchema := componentTestSchema(t, implementation)
	currentState := componentTestState(t, componentSchema, current)
	response := resource.ReadResponse{State: currentState}
	implementation.Read(ctx, resource.ReadRequest{State: currentState}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", response.Diagnostics)
	}
	if !response.State.Raw.IsNull() {
		t.Fatal("not-found component must be removed from Terraform state")
	}
}

func TestComponentModelNormalizesMissingTagArraysToEmptySets(t *testing.T) {
	t.Parallel()
	model, diagnostics := componentModelFromRemote(context.Background(), client.Component{
		ID:              "component-uuid",
		ExternalKey:     "main-net/core/sequencer",
		Kind:            client.ComponentKindRollup,
		Name:            "Sequencer",
		ComponentTypeID: "type-service",
		AlertEnabled:    false,
		State:           client.ComponentStateUnknown,
		Revision:        1,
	})
	if diagnostics.HasError() {
		t.Fatalf("model diagnostics: %v", diagnostics)
	}
	if model.RelevanceTagIDs.IsNull() || model.RelevanceTagIDs.IsUnknown() || len(model.RelevanceTagIDs.Elements()) != 0 {
		t.Fatalf("relevance tags = %#v, want known empty set", model.RelevanceTagIDs)
	}
	if model.FilterTagIDs.IsNull() || model.FilterTagIDs.IsUnknown() || len(model.FilterTagIDs.Elements()) != 0 {
		t.Fatalf("filter tags = %#v, want known empty set", model.FilterTagIDs)
	}
}

func TestComponentResourceUpdateFailsClosedOnMissingRevision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	current := componentTestModel()
	current.ID = types.StringValue("component-uuid")
	current.State = types.StringValue("unknown")
	current.ConfigurationRevision = types.Int64Unknown()
	planned := current
	planned.ConfigurationRevision = types.Int64Unknown()

	called := false
	fake := &fakeComponentAPI{
		update: func(context.Context, string, client.ComponentUpdateRequest, client.MutationOptions) (client.Component, error) {
			called = true
			return client.Component{}, nil
		},
	}
	implementation := &ComponentResource{client: fake}
	componentSchema := componentTestSchema(t, implementation)
	response := resource.UpdateResponse{State: tfsdk.State{Schema: componentSchema}}
	implementation.Update(ctx, resource.UpdateRequest{
		Plan:  componentTestPlan(t, componentSchema, planned),
		State: componentTestState(t, componentSchema, current),
	}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("missing revision must fail the update")
	}
	if called {
		t.Fatal("update API must not be called without a known positive revision")
	}
}

func TestComponentResourceDeleteArchivesWithPriorRevision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	current := componentTestModel()
	current.ID = types.StringValue("component-uuid")
	current.State = types.StringValue("unknown")
	current.ConfigurationRevision = types.Int64Value(9)

	var gotID string
	var gotOptions client.MutationOptions
	fake := &fakeComponentAPI{
		archive: func(_ context.Context, id string, options client.MutationOptions) error {
			gotID, gotOptions = id, options
			return nil
		},
	}
	implementation := &ComponentResource{client: fake}
	componentSchema := componentTestSchema(t, implementation)
	response := resource.DeleteResponse{State: tfsdk.State{Schema: componentSchema}}
	implementation.Delete(ctx, resource.DeleteRequest{State: componentTestState(t, componentSchema, current)}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("delete diagnostics: %v", response.Diagnostics)
	}
	if gotID != "component-uuid" || gotOptions.Revision != 9 {
		t.Fatalf("archive call = id %q options %#v", gotID, gotOptions)
	}
}

func TestComponentResourceImportUsesUUID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	implementation := &ComponentResource{}
	componentSchema := componentTestSchema(t, implementation)
	response := resource.ImportStateResponse{State: componentTestState(t, componentSchema, componentImportStateModel())}
	implementation.ImportState(ctx, resource.ImportStateRequest{ID: "component-uuid"}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("import diagnostics: %v", response.Diagnostics)
	}
	var id types.String
	if diagnostics := response.State.GetAttribute(ctx, path.Root("id"), &id); diagnostics.HasError() {
		t.Fatalf("read imported id: %v", diagnostics)
	}
	if got, want := id.ValueString(), "component-uuid"; got != want {
		t.Fatalf("imported id = %q, want %q", got, want)
	}
}

func componentTestSchema(t *testing.T, implementation *ComponentResource) schema.Schema {
	t.Helper()
	var response resource.SchemaResponse
	implementation.Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	return response.Schema
}

func componentTestModel() componentResourceModel {
	return componentResourceModel{
		ID:              types.StringUnknown(),
		ExternalKey:     types.StringValue("main-net/core/sequencer/commitment"),
		Kind:            types.StringValue("external"),
		Name:            types.StringValue("Sequencer commitment"),
		ComponentTypeID: types.StringValue("type-service"),
		OwnerTeamID:     types.StringValue("team-platform"),
		RelevanceTagIDs: types.SetValueMust(types.StringType, []attr.Value{
			types.StringValue("tag-production"),
			types.StringValue("tag-citrea"),
		}),
		FilterTagIDs: types.SetValueMust(types.StringType, []attr.Value{
			types.StringValue("tag-core"),
		}),
		AlertEnabled:          types.BoolValue(false),
		State:                 types.StringUnknown(),
		ConfigurationRevision: types.Int64Unknown(),
	}
}

func componentImportStateModel() componentResourceModel {
	return componentResourceModel{
		ID:                    types.StringNull(),
		ExternalKey:           types.StringNull(),
		Kind:                  types.StringNull(),
		Name:                  types.StringNull(),
		ComponentTypeID:       types.StringNull(),
		OwnerTeamID:           types.StringNull(),
		RelevanceTagIDs:       types.SetNull(types.StringType),
		FilterTagIDs:          types.SetNull(types.StringType),
		AlertEnabled:          types.BoolNull(),
		State:                 types.StringNull(),
		ConfigurationRevision: types.Int64Null(),
	}
}

func componentTestPlan(t *testing.T, componentSchema schema.Schema, model componentResourceModel) tfsdk.Plan {
	t.Helper()
	plan := tfsdk.Plan{Schema: componentSchema}
	if diagnostics := plan.Set(context.Background(), &model); diagnostics.HasError() {
		t.Fatalf("set plan: %v", diagnostics)
	}
	return plan
}

func componentTestState(t *testing.T, componentSchema schema.Schema, model componentResourceModel) tfsdk.State {
	t.Helper()
	state := tfsdk.State{Schema: componentSchema}
	if diagnostics := state.Set(context.Background(), &model); diagnostics.HasError() {
		t.Fatalf("set state: %v", diagnostics)
	}
	return state
}

var _ client.ComponentAPI = (*fakeComponentAPI)(nil)
