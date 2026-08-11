package provider

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/sreforce/terraform-provider-pulse/internal/client"
)

const (
	testIntegrationComponentID = "11111111-1111-4111-8111-111111111111"
	testIntegrationID          = "22222222-2222-4222-8222-222222222222"
	testIntegrationVersionOne  = "33333333-3333-4333-8333-333333333333"
	testIntegrationVersionTwo  = "44444444-4444-4444-8444-444444444444"
)

type fakeIntegrationAPI struct {
	get    func(context.Context, string) (client.ComponentIntegration, error)
	create func(context.Context, string, client.ComponentIntegrationCreateRequest, client.MutationOptions) (client.ComponentIntegrationMutation, error)
	rotate func(context.Context, string, client.MutationOptions) (client.ComponentIntegrationMutation, error)
	adopt  func(context.Context, string, client.MutationOptions) (client.ComponentIntegrationMutation, error)
	delete func(context.Context, string, client.MutationOptions) error
}

func (f *fakeIntegrationAPI) GetComponentIntegration(ctx context.Context, componentID string) (client.ComponentIntegration, error) {
	if f.get == nil {
		return client.ComponentIntegration{}, errors.New("unexpected GetComponentIntegration call")
	}
	return f.get(ctx, componentID)
}

func (f *fakeIntegrationAPI) CreateComponentIntegration(ctx context.Context, componentID string, request client.ComponentIntegrationCreateRequest, options client.MutationOptions) (client.ComponentIntegrationMutation, error) {
	if f.create == nil {
		return client.ComponentIntegrationMutation{}, errors.New("unexpected CreateComponentIntegration call")
	}
	return f.create(ctx, componentID, request, options)
}

func (f *fakeIntegrationAPI) RotateComponentIntegration(ctx context.Context, componentID string, options client.MutationOptions) (client.ComponentIntegrationMutation, error) {
	if f.rotate == nil {
		return client.ComponentIntegrationMutation{}, errors.New("unexpected RotateComponentIntegration call")
	}
	return f.rotate(ctx, componentID, options)
}

func (f *fakeIntegrationAPI) AdoptComponentIntegration(ctx context.Context, componentID string, options client.MutationOptions) (client.ComponentIntegrationMutation, error) {
	if f.adopt == nil {
		return client.ComponentIntegrationMutation{}, errors.New("unexpected AdoptComponentIntegration call")
	}
	return f.adopt(ctx, componentID, options)
}

func (f *fakeIntegrationAPI) DeleteComponentIntegration(ctx context.Context, componentID string, options client.MutationOptions) error {
	if f.delete == nil {
		return errors.New("unexpected DeleteComponentIntegration call")
	}
	return f.delete(ctx, componentID, options)
}

func TestComponentIntegrationResourceSchemaProtectsOneTimeSecret(t *testing.T) {
	t.Parallel()

	implementation := &componentIntegrationResource{}
	var response resource.SchemaResponse
	implementation.Schema(context.Background(), resource.SchemaRequest{}, &response)
	if diagnostics := response.Schema.ValidateImplementation(context.Background()); diagnostics.HasError() {
		t.Fatalf("schema implementation diagnostics: %v", diagnostics)
	}

	secret, ok := response.Schema.Attributes["secret"].(resourceschema.StringAttribute)
	if !ok || !secret.Computed || !secret.Sensitive || secret.Required || secret.Optional {
		t.Fatalf("secret schema = %#v, want computed-only sensitive string", response.Schema.Attributes["secret"])
	}
	for _, name := range []string{"component_id", "source", "source_key", "rotation_trigger"} {
		attribute, ok := response.Schema.Attributes[name].(resourceschema.StringAttribute)
		if !ok || !attribute.Required {
			t.Fatalf("%s schema = %#v, want required string", name, response.Schema.Attributes[name])
		}
	}
	rotationRequired, ok := response.Schema.Attributes["rotation_required"].(resourceschema.BoolAttribute)
	if !ok || !rotationRequired.Computed {
		t.Fatalf("rotation_required schema = %#v, want computed boolean", response.Schema.Attributes["rotation_required"])
	}
}

func TestComponentIntegrationSourceKeySupportsStableHierarchyPaths(t *testing.T) {
	t.Parallel()

	valid := []string{
		"main-net/core/citrea-sequencer/sequencer-stopped",
		"grafana:sequencer_stopped.v2",
	}
	for _, sourceKey := range valid {
		if !sourceKeyPattern.MatchString(sourceKey) {
			t.Fatalf("sourceKeyPattern rejected valid key %q", sourceKey)
		}
	}

	invalid := []string{
		"Main-Net/core/sequencer",
		"/main-net/core/sequencer",
		"main net/core/sequencer",
		strings.Repeat("a", 129),
	}
	for _, sourceKey := range invalid {
		if sourceKeyPattern.MatchString(sourceKey) {
			t.Fatalf("sourceKeyPattern accepted invalid key %q", sourceKey)
		}
	}
}

func TestComponentIntegrationCreateRecoversLostOneTimeSecret(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	remote := componentIntegrationTestRemote(testIntegrationVersionTwo, 2)
	mutation := integrationTestMutation(remote, "new-component-secret")

	var createRequest client.ComponentIntegrationCreateRequest
	var rotateOptions client.MutationOptions
	fake := &fakeIntegrationAPI{
		create: func(_ context.Context, componentID string, request client.ComponentIntegrationCreateRequest, _ client.MutationOptions) (client.ComponentIntegrationMutation, error) {
			if componentID != testIntegrationComponentID {
				t.Fatalf("create component ID = %q", componentID)
			}
			createRequest = request
			return client.ComponentIntegrationMutation{}, &client.ResponseError{
				StatusCode: http.StatusConflict,
				Code:       client.ErrorCodeSecretReissueRequired,
				SecretReissue: &client.SecretReissueMetadata{
					IntegrationID:       testIntegrationID,
					CredentialVersionID: testIntegrationVersionOne,
					Revision:            1,
				},
			}
		},
		rotate: func(_ context.Context, componentID string, options client.MutationOptions) (client.ComponentIntegrationMutation, error) {
			if componentID != testIntegrationComponentID {
				t.Fatalf("rotate component ID = %q", componentID)
			}
			rotateOptions = options
			return mutation, nil
		},
	}
	implementation := &componentIntegrationResource{api: fake}
	schemaValue := componentIntegrationTestSchema(t, implementation)
	plan := componentIntegrationTestModel()
	response := resource.CreateResponse{State: tfsdk.State{Schema: schemaValue}}
	implementation.Create(ctx, resource.CreateRequest{Plan: componentIntegrationTestPlan(t, schemaValue, plan)}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("create diagnostics: %v", response.Diagnostics)
	}

	if string(createRequest.Source) != grafanaIntegrationSource || createRequest.SourceKey != "sequencer-commitment" {
		t.Fatalf("create request = %#v", createRequest)
	}
	if rotateOptions.Revision != 1 {
		t.Fatalf("recovery rotate revision = %d, want 1", rotateOptions.Revision)
	}
	if !rotateOptions.RevokePredecessorImmediately {
		t.Fatal("lost-secret recovery must revoke the unreachable predecessor immediately")
	}
	var state componentIntegrationResourceModel
	assertIntegrationNoDiagnostics(t, response.State.Get(ctx, &state))
	if got, want := state.Secret.ValueString(), "new-component-secret"; got != want {
		t.Fatalf("secret = %q, want %q", got, want)
	}
	if got, want := state.ObservedVersion.ValueString(), testIntegrationVersionTwo; got != want {
		t.Fatalf("observed version = %q, want %q", got, want)
	}
	if state.RotationRequired.ValueBool() {
		t.Fatal("successful secret reissue must clear rotation_required")
	}
}

func TestComponentIntegrationReadPreservesMatchingSecret(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	remote := componentIntegrationTestRemote(testIntegrationVersionOne, 8)
	current := componentIntegrationTestModel()
	current.ID = types.StringValue(testIntegrationID)
	current.Endpoint = types.StringValue(remote.Endpoint)
	current.Secret = types.StringValue("stored-secret")
	current.ObservedVersion = types.StringValue(testIntegrationVersionOne)
	current.RotationRequired = types.BoolValue(false)
	current.LifecycleOwner = types.StringValue("automation")
	current.Revision = types.Int64Value(7)
	current.Status = types.StringValue("active")

	implementation := &componentIntegrationResource{api: &fakeIntegrationAPI{
		get: func(_ context.Context, componentID string) (client.ComponentIntegration, error) {
			if componentID != testIntegrationComponentID {
				t.Fatalf("read component ID = %q", componentID)
			}
			return remote, nil
		},
	}}
	schemaValue := componentIntegrationTestSchema(t, implementation)
	response := resource.ReadResponse{State: componentIntegrationTestState(t, schemaValue, current)}
	implementation.Read(ctx, resource.ReadRequest{State: componentIntegrationTestState(t, schemaValue, current)}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", response.Diagnostics)
	}
	var refreshed componentIntegrationResourceModel
	assertIntegrationNoDiagnostics(t, response.State.Get(ctx, &refreshed))
	if got, want := refreshed.Secret.ValueString(), "stored-secret"; got != want {
		t.Fatalf("matching secret = %q, want %q", got, want)
	}
	if got, want := refreshed.Revision.ValueInt64(), int64(8); got != want {
		t.Fatalf("revision = %d, want %d", got, want)
	}
}

func TestComponentIntegrationReadMarksOutOfBandVersionForRotation(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	remote := componentIntegrationTestRemote(testIntegrationVersionTwo, 9)
	current := componentIntegrationTestModel()
	current.ID = types.StringValue(testIntegrationID)
	current.Endpoint = types.StringValue(remote.Endpoint)
	current.Secret = types.StringValue("now-stale-secret")
	current.ObservedVersion = types.StringValue(testIntegrationVersionOne)
	current.RotationRequired = types.BoolValue(false)
	current.LifecycleOwner = types.StringValue("automation")
	current.Revision = types.Int64Value(8)
	current.Status = types.StringValue("active")

	implementation := &componentIntegrationResource{api: &fakeIntegrationAPI{
		get: func(context.Context, string) (client.ComponentIntegration, error) { return remote, nil },
	}}
	schemaValue := componentIntegrationTestSchema(t, implementation)
	response := resource.ReadResponse{State: componentIntegrationTestState(t, schemaValue, current)}
	implementation.Read(ctx, resource.ReadRequest{State: componentIntegrationTestState(t, schemaValue, current)}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("version drift must warn without aborting refresh: %v", response.Diagnostics)
	}
	if response.Diagnostics.WarningsCount() != 1 {
		t.Fatalf("warning count = %d, want 1", response.Diagnostics.WarningsCount())
	}
	var refreshed componentIntegrationResourceModel
	assertIntegrationNoDiagnostics(t, response.State.Get(ctx, &refreshed))
	if !refreshed.Secret.IsNull() {
		t.Fatalf("drifted secret = %#v, want null", refreshed.Secret)
	}
	if !refreshed.RotationRequired.ValueBool() {
		t.Fatal("version drift must set rotation_required")
	}
	if got, want := refreshed.ObservedVersion.ValueString(), testIntegrationVersionTwo; got != want {
		t.Fatalf("observed version = %q, want %q", got, want)
	}
}

func TestComponentIntegrationModifyPlanBlocksUntilRotationTriggerChanges(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	implementation := &componentIntegrationResource{}
	schemaValue := componentIntegrationTestSchema(t, implementation)
	state := componentIntegrationTestModel()
	state.ID = types.StringValue(testIntegrationID)
	state.Endpoint = types.StringValue("https://pulse.example.test/webhooks/component-integrations/" + testIntegrationID + "/grafana")
	state.Secret = types.StringNull()
	state.ObservedVersion = types.StringValue(testIntegrationVersionTwo)
	state.RotationRequired = types.BoolValue(true)
	state.LifecycleOwner = types.StringValue("automation")
	state.Revision = types.Int64Value(9)
	state.Status = types.StringValue("active")

	unchanged := state
	blocked := resource.ModifyPlanResponse{Plan: componentIntegrationTestPlan(t, schemaValue, unchanged)}
	implementation.ModifyPlan(ctx, resource.ModifyPlanRequest{
		State: componentIntegrationTestState(t, schemaValue, state),
		Plan:  componentIntegrationTestPlan(t, schemaValue, unchanged),
	}, &blocked)
	if !blocked.Diagnostics.HasError() {
		t.Fatal("unchanged rotation_trigger must block planning when rotation_required is true")
	}

	changed := state
	changed.RotationTrigger = types.StringValue("2026-09-rotate")
	allowed := resource.ModifyPlanResponse{Plan: componentIntegrationTestPlan(t, schemaValue, changed)}
	implementation.ModifyPlan(ctx, resource.ModifyPlanRequest{
		State: componentIntegrationTestState(t, schemaValue, state),
		Plan:  componentIntegrationTestPlan(t, schemaValue, changed),
	}, &allowed)
	if allowed.Diagnostics.HasError() {
		t.Fatalf("changed rotation_trigger diagnostics: %v", allowed.Diagnostics)
	}
	var planned componentIntegrationResourceModel
	assertIntegrationNoDiagnostics(t, allowed.Plan.Get(ctx, &planned))
	if !planned.Secret.IsUnknown() || !planned.RotationRequired.IsUnknown() {
		t.Fatalf("rotation outputs must be unknown: secret=%#v rotation_required=%#v", planned.Secret, planned.RotationRequired)
	}
}

func TestComponentIntegrationModifyPlanRejectsSourceKeyChangeOnSameLeaf(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	implementation := &componentIntegrationResource{}
	schemaValue := componentIntegrationTestSchema(t, implementation)
	state := componentIntegrationTestModel()
	state.ID = types.StringValue(testIntegrationID)
	state.Endpoint = types.StringValue("https://pulse.example.test/webhooks/component-integrations/" + testIntegrationID + "/grafana")
	state.Secret = types.StringValue("stored-secret")
	state.ObservedVersion = types.StringValue(testIntegrationVersionOne)
	state.RotationRequired = types.BoolValue(false)
	state.LifecycleOwner = types.StringValue("automation")
	state.Revision = types.Int64Value(3)
	state.Status = types.StringValue("active")
	planned := state
	planned.SourceKey = types.StringValue("different-alert-mapping")

	response := resource.ModifyPlanResponse{Plan: componentIntegrationTestPlan(t, schemaValue, planned)}
	implementation.ModifyPlan(ctx, resource.ModifyPlanRequest{
		State: componentIntegrationTestState(t, schemaValue, state),
		Plan:  componentIntegrationTestPlan(t, schemaValue, planned),
	}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("source_key change on the same component must require a new alert leaf")
	}
}

func TestComponentIntegrationModifyPlanRejectsHumanOwnedDestroy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	implementation := &componentIntegrationResource{}
	schemaValue := componentIntegrationTestSchema(t, implementation)
	state := componentIntegrationTestModel()
	state.ID = types.StringValue(testIntegrationID)
	state.Endpoint = types.StringValue("https://pulse.example.test/webhooks/component-integrations/" + testIntegrationID + "/grafana")
	state.Secret = types.StringValue("stored-secret")
	state.ObservedVersion = types.StringValue(testIntegrationVersionOne)
	state.RotationRequired = types.BoolValue(false)
	state.LifecycleOwner = types.StringValue("human")
	state.Revision = types.Int64Value(3)
	state.Status = types.StringValue("active")
	nullPlan := tfsdk.Plan{
		Schema: schemaValue,
		Raw:    tftypes.NewValue(schemaValue.Type().TerraformType(ctx), nil),
	}
	response := resource.ModifyPlanResponse{Plan: nullPlan}
	implementation.ModifyPlan(ctx, resource.ModifyPlanRequest{
		State: componentIntegrationTestState(t, schemaValue, state),
		Plan:  nullPlan,
	}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("human-owned integration destroy must require a completed adoption")
	}

	state.LifecycleOwner = types.StringValue("automation")
	allowed := resource.ModifyPlanResponse{Plan: nullPlan}
	implementation.ModifyPlan(ctx, resource.ModifyPlanRequest{
		State: componentIntegrationTestState(t, schemaValue, state),
		Plan:  nullPlan,
	}, &allowed)
	if allowed.Diagnostics.HasError() {
		t.Fatalf("automation-owned integration destroy diagnostics: %v", allowed.Diagnostics)
	}
}

func TestComponentIntegrationUpdateRotatesAndUsesCurrentRevision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	current := componentIntegrationTestModel()
	current.ID = types.StringValue(testIntegrationID)
	current.Endpoint = types.StringValue("https://pulse.example.test/webhooks/component-integrations/" + testIntegrationID + "/grafana")
	current.Secret = types.StringValue("old-secret")
	current.ObservedVersion = types.StringValue(testIntegrationVersionOne)
	current.RotationRequired = types.BoolValue(false)
	current.LifecycleOwner = types.StringValue("automation")
	current.Revision = types.Int64Value(4)
	current.Status = types.StringValue("active")
	planned := current
	planned.RotationTrigger = types.StringValue("2026-09-rotate")
	planned.Secret = types.StringUnknown()
	planned.ObservedVersion = types.StringUnknown()
	planned.RotationRequired = types.BoolUnknown()
	planned.Revision = types.Int64Unknown()

	var captured client.MutationOptions
	remote := componentIntegrationTestRemote(testIntegrationVersionTwo, 5)
	implementation := &componentIntegrationResource{api: &fakeIntegrationAPI{
		rotate: func(_ context.Context, componentID string, options client.MutationOptions) (client.ComponentIntegrationMutation, error) {
			if componentID != testIntegrationComponentID {
				t.Fatalf("rotate component ID = %q", componentID)
			}
			captured = options
			return integrationTestMutation(remote, "rotated-secret"), nil
		},
	}}
	schemaValue := componentIntegrationTestSchema(t, implementation)
	response := resource.UpdateResponse{State: tfsdk.State{Schema: schemaValue}}
	implementation.Update(ctx, resource.UpdateRequest{
		State: componentIntegrationTestState(t, schemaValue, current),
		Plan:  componentIntegrationTestPlan(t, schemaValue, planned),
	}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("update diagnostics: %v", response.Diagnostics)
	}
	if got, want := captured.Revision, int64(4); got != want {
		t.Fatalf("rotate revision = %d, want %d", got, want)
	}
	if captured.RevokePredecessorImmediately {
		t.Fatal("normal requested rotation must use the server's bounded predecessor overlap")
	}
	var updated componentIntegrationResourceModel
	assertIntegrationNoDiagnostics(t, response.State.Get(ctx, &updated))
	if got, want := updated.Secret.ValueString(), "rotated-secret"; got != want {
		t.Fatalf("rotated secret = %q, want %q", got, want)
	}
	if updated.RotationRequired.ValueBool() {
		t.Fatal("successful rotation must clear rotation_required")
	}
}

func TestComponentIntegrationHumanOwnershipRequiresExplicitAdoption(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	current := componentIntegrationTestModel()
	current.ID = types.StringValue(testIntegrationID)
	current.Endpoint = types.StringValue("https://pulse.example.test/webhooks/component-integrations/" + testIntegrationID + "/grafana")
	current.Secret = types.StringNull()
	current.ObservedVersion = types.StringValue(testIntegrationVersionOne)
	current.RotationRequired = types.BoolValue(true)
	current.LifecycleOwner = types.StringValue("human")
	current.Revision = types.Int64Value(3)
	current.Status = types.StringValue("active")
	planned := current
	planned.RotationTrigger = types.StringValue("2026-09-adopt")
	planned.Adopt = types.BoolValue(false)

	implementation := &componentIntegrationResource{api: &fakeIntegrationAPI{}}
	schemaValue := componentIntegrationTestSchema(t, implementation)
	response := resource.UpdateResponse{State: tfsdk.State{Schema: schemaValue}}
	implementation.Update(ctx, resource.UpdateRequest{
		State: componentIntegrationTestState(t, schemaValue, current),
		Plan:  componentIntegrationTestPlan(t, schemaValue, planned),
	}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("human-owned integration update must require adopt=true")
	}
}

func TestComponentIntegrationAdoptionTransfersOwnershipAndRotates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	current := componentIntegrationTestModel()
	current.ID = types.StringValue(testIntegrationID)
	current.Endpoint = types.StringValue("https://pulse.example.test/webhooks/component-integrations/" + testIntegrationID + "/grafana")
	current.Secret = types.StringNull()
	current.ObservedVersion = types.StringValue(testIntegrationVersionOne)
	current.RotationRequired = types.BoolValue(true)
	current.LifecycleOwner = types.StringValue("human")
	current.Revision = types.Int64Value(3)
	current.Status = types.StringValue("active")
	planned := current
	planned.RotationTrigger = types.StringValue("2026-09-adopt")
	planned.Adopt = types.BoolValue(true)
	planned.Secret = types.StringUnknown()
	planned.ObservedVersion = types.StringUnknown()
	planned.RotationRequired = types.BoolUnknown()
	planned.LifecycleOwner = types.StringUnknown()
	planned.Revision = types.Int64Unknown()

	var captured client.MutationOptions
	remote := componentIntegrationTestRemote(testIntegrationVersionTwo, 4)
	implementation := &componentIntegrationResource{api: &fakeIntegrationAPI{
		adopt: func(_ context.Context, componentID string, options client.MutationOptions) (client.ComponentIntegrationMutation, error) {
			if componentID != testIntegrationComponentID {
				t.Fatalf("adopt component ID = %q", componentID)
			}
			captured = options
			return integrationTestMutation(remote, "adopted-secret"), nil
		},
	}}
	schemaValue := componentIntegrationTestSchema(t, implementation)
	response := resource.UpdateResponse{State: tfsdk.State{Schema: schemaValue}}
	implementation.Update(ctx, resource.UpdateRequest{
		State: componentIntegrationTestState(t, schemaValue, current),
		Plan:  componentIntegrationTestPlan(t, schemaValue, planned),
	}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("adopt diagnostics: %v", response.Diagnostics)
	}
	if got, want := captured.Revision, int64(3); got != want {
		t.Fatalf("adopt revision = %d, want %d", got, want)
	}
	var adopted componentIntegrationResourceModel
	assertIntegrationNoDiagnostics(t, response.State.Get(ctx, &adopted))
	if got, want := adopted.LifecycleOwner.ValueString(), "automation"; got != want {
		t.Fatalf("lifecycle owner = %q, want %q", got, want)
	}
	if got, want := adopted.Secret.ValueString(), "adopted-secret"; got != want {
		t.Fatalf("adopted secret = %q, want %q", got, want)
	}
}

func TestComponentIntegrationDeleteArchivesWithRevision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	current := componentIntegrationTestModel()
	current.ID = types.StringValue(testIntegrationID)
	current.Endpoint = types.StringValue("https://pulse.example.test/webhooks/component-integrations/" + testIntegrationID + "/grafana")
	current.Secret = types.StringValue("stored-secret")
	current.ObservedVersion = types.StringValue(testIntegrationVersionOne)
	current.RotationRequired = types.BoolValue(false)
	current.LifecycleOwner = types.StringValue("automation")
	current.Revision = types.Int64Value(12)
	current.Status = types.StringValue("active")

	var captured client.MutationOptions
	implementation := &componentIntegrationResource{api: &fakeIntegrationAPI{
		delete: func(_ context.Context, componentID string, options client.MutationOptions) error {
			if componentID != testIntegrationComponentID {
				t.Fatalf("delete component ID = %q", componentID)
			}
			captured = options
			return nil
		},
	}}
	schemaValue := componentIntegrationTestSchema(t, implementation)
	response := resource.DeleteResponse{State: tfsdk.State{Schema: schemaValue}}
	implementation.Delete(ctx, resource.DeleteRequest{State: componentIntegrationTestState(t, schemaValue, current)}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("delete diagnostics: %v", response.Diagnostics)
	}
	if got, want := captured.Revision, int64(12); got != want {
		t.Fatalf("archive revision = %d, want %d", got, want)
	}
}

func TestComponentIntegrationDeleteRejectsHumanOwnedIntegration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	current := componentIntegrationTestModel()
	current.ID = types.StringValue(testIntegrationID)
	current.Endpoint = types.StringValue("https://pulse.example.test/webhooks/component-integrations/" + testIntegrationID + "/grafana")
	current.Secret = types.StringValue("stored-secret")
	current.ObservedVersion = types.StringValue(testIntegrationVersionOne)
	current.RotationRequired = types.BoolValue(false)
	current.LifecycleOwner = types.StringValue("human")
	current.Revision = types.Int64Value(12)
	current.Status = types.StringValue("active")

	deleteCalled := false
	implementation := &componentIntegrationResource{api: &fakeIntegrationAPI{
		delete: func(context.Context, string, client.MutationOptions) error {
			deleteCalled = true
			return nil
		},
	}}
	schemaValue := componentIntegrationTestSchema(t, implementation)
	response := resource.DeleteResponse{State: tfsdk.State{Schema: schemaValue}}
	implementation.Delete(ctx, resource.DeleteRequest{State: componentIntegrationTestState(t, schemaValue, current)}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("human-owned integration archive must require a completed adoption")
	}
	if deleteCalled {
		t.Fatal("provider attempted to archive a human-owned integration")
	}
}

func TestComponentIntegrationImportUsesBoundComponentUUID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	implementation := &componentIntegrationResource{}
	schemaValue := componentIntegrationTestSchema(t, implementation)
	response := resource.ImportStateResponse{State: tfsdk.State{
		Schema: schemaValue,
		Raw:    tftypes.NewValue(schemaValue.Type().TerraformType(ctx), nil),
	}}
	implementation.ImportState(ctx, resource.ImportStateRequest{ID: testIntegrationComponentID}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("import diagnostics: %v", response.Diagnostics)
	}
	var imported types.String
	assertIntegrationNoDiagnostics(t, response.State.GetAttribute(ctx, path.Root("component_id"), &imported))
	if got, want := imported.ValueString(), testIntegrationComponentID; got != want {
		t.Fatalf("imported component ID = %q, want %q", got, want)
	}

	remote := componentIntegrationTestRemote(testIntegrationVersionOne, 3)
	implementation.api = &fakeIntegrationAPI{
		get: func(context.Context, string) (client.ComponentIntegration, error) { return remote, nil },
	}
	refreshed := resource.ReadResponse{State: response.State}
	implementation.Read(ctx, resource.ReadRequest{State: response.State}, &refreshed)
	if refreshed.Diagnostics.HasError() {
		t.Fatalf("import refresh diagnostics: %v", refreshed.Diagnostics)
	}
	var importedState componentIntegrationResourceModel
	assertIntegrationNoDiagnostics(t, refreshed.State.Get(ctx, &importedState))
	if !importedState.Secret.IsNull() || !importedState.RotationRequired.ValueBool() {
		t.Fatalf("import must require rotation: secret=%#v rotation_required=%#v", importedState.Secret, importedState.RotationRequired)
	}

	invalid := resource.ImportStateResponse{State: tfsdk.State{
		Schema: schemaValue,
		Raw:    tftypes.NewValue(schemaValue.Type().TerraformType(ctx), nil),
	}}
	implementation.ImportState(ctx, resource.ImportStateRequest{ID: testIntegrationID + "-invalid"}, &invalid)
	if !invalid.Diagnostics.HasError() {
		t.Fatal("invalid import UUID must be rejected")
	}
}

func componentIntegrationTestSchema(t *testing.T, implementation *componentIntegrationResource) resourceschema.Schema {
	t.Helper()
	var response resource.SchemaResponse
	implementation.Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	return response.Schema
}

func componentIntegrationTestModel() componentIntegrationResourceModel {
	return componentIntegrationResourceModel{
		ID:               types.StringUnknown(),
		ComponentID:      types.StringValue(testIntegrationComponentID),
		Source:           types.StringValue(grafanaIntegrationSource),
		SourceKey:        types.StringValue("sequencer-commitment"),
		RotationTrigger:  types.StringValue("2026-08-initial"),
		Adopt:            types.BoolValue(false),
		Endpoint:         types.StringUnknown(),
		Secret:           types.StringUnknown(),
		ObservedVersion:  types.StringUnknown(),
		RotationRequired: types.BoolUnknown(),
		LifecycleOwner:   types.StringUnknown(),
		Revision:         types.Int64Unknown(),
		Status:           types.StringUnknown(),
	}
}

func componentIntegrationTestRemote(versionID string, revision int64) client.ComponentIntegration {
	return client.ComponentIntegration{
		ID:                  testIntegrationID,
		ComponentID:         testIntegrationComponentID,
		Source:              grafanaIntegrationSource,
		SourceKey:           "sequencer-commitment",
		Endpoint:            "https://pulse.example.test/webhooks/component-integrations/" + testIntegrationID + "/grafana",
		LifecycleOwner:      client.IntegrationLifecycleOwnerAutomation,
		Status:              "active",
		CredentialVersionID: versionID,
		Revision:            revision,
	}
}

func integrationTestMutation(integration client.ComponentIntegration, secret string) client.ComponentIntegrationMutation {
	return client.ComponentIntegrationMutation{
		Integration: integration,
		Secret: &client.ComponentIntegrationSecret{
			Value:     secret,
			VersionID: integration.CredentialVersionID,
		},
	}
}

func componentIntegrationTestPlan(t *testing.T, schemaValue resourceschema.Schema, model componentIntegrationResourceModel) tfsdk.Plan {
	t.Helper()
	result := tfsdk.Plan{Schema: schemaValue}
	assertIntegrationNoDiagnostics(t, result.Set(context.Background(), &model))
	return result
}

func componentIntegrationTestState(t *testing.T, schemaValue resourceschema.Schema, model componentIntegrationResourceModel) tfsdk.State {
	t.Helper()
	result := tfsdk.State{Schema: schemaValue}
	assertIntegrationNoDiagnostics(t, result.Set(context.Background(), &model))
	return result
}

func assertIntegrationNoDiagnostics(t *testing.T, diagnostics interface {
	HasError() bool
}) {
	t.Helper()
	if diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diagnostics)
	}
}
