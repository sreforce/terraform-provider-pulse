package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/sreforce/terraform-provider-pulse/internal/client"
)

const (
	integrationTestComponentID = "00000000-0000-4000-8000-000000000101"
	integrationTestVersion1    = "00000000-0000-4000-8000-000000000401"
	integrationTestVersion2    = "00000000-0000-4000-8000-000000000402"
)

type fakeIntegrationAPI struct {
	get    func(context.Context, string, client.IntegrationProvider) (client.ComponentIntegration, error)
	upsert func(context.Context, string, client.IntegrationProvider, client.ComponentIntegrationUpsertRequest, client.MutationOptions) (client.ComponentIntegrationMutation, error)
	rotate func(context.Context, string, client.IntegrationProvider, client.MutationOptions) (client.ComponentIntegrationMutation, error)
	delete func(context.Context, string, client.IntegrationProvider, client.MutationOptions) error
}

func (f *fakeIntegrationAPI) GetComponentIntegration(ctx context.Context, componentID string, provider client.IntegrationProvider) (client.ComponentIntegration, error) {
	return f.get(ctx, componentID, provider)
}
func (f *fakeIntegrationAPI) UpsertComponentIntegration(ctx context.Context, componentID string, provider client.IntegrationProvider, payload client.ComponentIntegrationUpsertRequest, options client.MutationOptions) (client.ComponentIntegrationMutation, error) {
	return f.upsert(ctx, componentID, provider, payload, options)
}
func (f *fakeIntegrationAPI) RotateComponentIntegration(ctx context.Context, componentID string, provider client.IntegrationProvider, options client.MutationOptions) (client.ComponentIntegrationMutation, error) {
	return f.rotate(ctx, componentID, provider, options)
}
func (f *fakeIntegrationAPI) DeleteComponentIntegration(ctx context.Context, componentID string, provider client.IntegrationProvider, options client.MutationOptions) error {
	return f.delete(ctx, componentID, provider, options)
}

func TestComponentIntegrationSchemaV1UsesNaturalIdentity(t *testing.T) {
	t.Parallel()
	implementation := &componentIntegrationResource{}
	got := integrationTestSchema(t, implementation)
	if got.Version != 1 {
		t.Fatalf("schema version = %d, want 1", got.Version)
	}
	for _, name := range []string{"component_id", "integration_provider", "rotation_trigger", "adopt", "endpoint", "secret", "version", "rotation_required", "lifecycle_owner", "revision"} {
		if _, ok := got.Attributes[name]; !ok {
			t.Fatalf("missing schema attribute %q", name)
		}
	}
	for _, removed := range []string{"id", "source", "source_key", "observed_version", "status"} {
		if _, ok := got.Attributes[removed]; ok {
			t.Fatalf("removed v0 attribute %q is still public", removed)
		}
	}
}

func TestComponentIntegrationValidateProviders(t *testing.T) {
	t.Parallel()
	implementation := &componentIntegrationResource{}
	testSchema := integrationTestSchema(t, implementation)
	for _, provider := range []string{"grafana", "pagerduty", "pulse"} {
		model := integrationTestModel(provider)
		response := resource.ValidateConfigResponse{}
		implementation.ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: integrationTestConfig(t, testSchema, model)}, &response)
		if response.Diagnostics.HasError() {
			t.Fatalf("provider %q rejected: %v", provider, response.Diagnostics)
		}
	}
	invalid := integrationTestModel("email")
	response := resource.ValidateConfigResponse{}
	implementation.ValidateConfig(context.Background(), resource.ValidateConfigRequest{Config: integrationTestConfig(t, testSchema, invalid)}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("unsupported integration provider was accepted")
	}
}

func TestComponentIntegrationCreateUpsertsNaturalIdentity(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var gotComponent string
	var gotProvider client.IntegrationProvider
	var gotPayload client.ComponentIntegrationUpsertRequest
	fake := &fakeIntegrationAPI{upsert: func(_ context.Context, componentID string, provider client.IntegrationProvider, payload client.ComponentIntegrationUpsertRequest, options client.MutationOptions) (client.ComponentIntegrationMutation, error) {
		gotComponent, gotProvider, gotPayload = componentID, provider, payload
		if options.Revision != 0 {
			t.Fatalf("create revision = %d, want 0", options.Revision)
		}
		return integrationMutation(provider, integrationTestVersion1, 1), nil
	}}
	implementation := &componentIntegrationResource{api: fake}
	testSchema := integrationTestSchema(t, implementation)
	plan := integrationTestModel("pagerduty")
	plan.Adopt = types.BoolValue(true)
	response := resource.CreateResponse{State: tfsdk.State{Schema: testSchema}}
	implementation.Create(ctx, resource.CreateRequest{Plan: integrationTestPlan(t, testSchema, plan)}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("create diagnostics: %v", response.Diagnostics)
	}
	if gotComponent != integrationTestComponentID || gotProvider != client.IntegrationProviderPagerDuty || gotPayload.Adopt {
		t.Fatalf("upsert identity/payload = %q %q %#v", gotComponent, gotProvider, gotPayload)
	}
	var state componentIntegrationResourceModel
	response.State.Get(ctx, &state)
	if state.IntegrationProvider.ValueString() != "pagerduty" || state.Version.ValueString() != integrationTestVersion1 || state.Secret.ValueString() != "one-time-secret" {
		t.Fatalf("created state = %#v", state)
	}
}

func TestComponentIntegrationCreateExplicitAdoptionUsesObservedRevision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	upserts := 0
	fake := &fakeIntegrationAPI{
		upsert: func(_ context.Context, _ string, provider client.IntegrationProvider, payload client.ComponentIntegrationUpsertRequest, options client.MutationOptions) (client.ComponentIntegrationMutation, error) {
			upserts++
			if upserts == 1 {
				if payload.Adopt || options.Revision != 0 {
					t.Fatalf("initial upsert was not a safe non-adopting create: %#v %#v", payload, options)
				}
				return client.ComponentIntegrationMutation{}, &client.ResponseError{StatusCode: 409, Code: client.ErrorCodeOwnershipConflict}
			}
			if !payload.Adopt || options.Revision != 8 || provider != client.IntegrationProviderGrafana {
				t.Fatalf("guarded adoption = provider %q payload %#v options %#v", provider, payload, options)
			}
			return integrationMutation(provider, integrationTestVersion2, 9), nil
		},
		get: func(context.Context, string, client.IntegrationProvider) (client.ComponentIntegration, error) {
			metadata := integrationMetadata(client.IntegrationProviderGrafana, integrationTestVersion1, 8)
			metadata.LifecycleOwner = client.IntegrationLifecycleOwnerHuman
			return metadata, nil
		},
	}
	implementation := &componentIntegrationResource{api: fake}
	testSchema := integrationTestSchema(t, implementation)
	plan := integrationTestModel("grafana")
	plan.Adopt = types.BoolValue(true)
	response := resource.CreateResponse{State: tfsdk.State{Schema: testSchema}}
	implementation.Create(ctx, resource.CreateRequest{Plan: integrationTestPlan(t, testSchema, plan)}, &response)
	if response.Diagnostics.HasError() || upserts != 2 {
		t.Fatalf("adoption diagnostics=%v upserts=%d", response.Diagnostics, upserts)
	}
}

func TestComponentIntegrationReadDetectsOutOfBandVersion(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fake := &fakeIntegrationAPI{get: func(context.Context, string, client.IntegrationProvider) (client.ComponentIntegration, error) {
		return integrationMetadata(client.IntegrationProviderGrafana, integrationTestVersion2, 2), nil
	}}
	implementation := &componentIntegrationResource{api: fake}
	testSchema := integrationTestSchema(t, implementation)
	state := integrationStateModel("grafana", integrationTestVersion1, 1)
	response := resource.ReadResponse{State: integrationTestState(t, testSchema, state)}
	implementation.Read(ctx, resource.ReadRequest{State: integrationTestState(t, testSchema, state)}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("read diagnostics: %v", response.Diagnostics)
	}
	var refreshed componentIntegrationResourceModel
	response.State.Get(ctx, &refreshed)
	if !refreshed.Secret.IsNull() || !refreshed.RotationRequired.ValueBool() || refreshed.Version.ValueString() != integrationTestVersion2 {
		t.Fatalf("drift state = %#v", refreshed)
	}
}

func TestComponentIntegrationUpdateRotatesAndAdoptsWithProvider(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	t.Run("rotate", func(t *testing.T) {
		var called bool
		fake := &fakeIntegrationAPI{rotate: func(_ context.Context, componentID string, provider client.IntegrationProvider, options client.MutationOptions) (client.ComponentIntegrationMutation, error) {
			called = componentID == integrationTestComponentID && provider == client.IntegrationProviderPulse && options.Revision == 4
			return integrationMutation(provider, integrationTestVersion2, 5), nil
		}}
		implementation := &componentIntegrationResource{api: fake}
		testSchema := integrationTestSchema(t, implementation)
		state := integrationStateModel("pulse", integrationTestVersion1, 4)
		plan := state
		plan.RotationTrigger = types.StringValue("2026-08-rotate")
		plan.Secret, plan.Version, plan.Revision = types.StringUnknown(), types.StringUnknown(), types.Int64Unknown()
		response := resource.UpdateResponse{State: tfsdk.State{Schema: testSchema}}
		implementation.Update(ctx, resource.UpdateRequest{State: integrationTestState(t, testSchema, state), Plan: integrationTestPlan(t, testSchema, plan)}, &response)
		if response.Diagnostics.HasError() || !called {
			t.Fatalf("rotate diagnostics=%v called=%v", response.Diagnostics, called)
		}
	})
	t.Run("adopt", func(t *testing.T) {
		var adopted bool
		fake := &fakeIntegrationAPI{upsert: func(_ context.Context, _ string, provider client.IntegrationProvider, payload client.ComponentIntegrationUpsertRequest, options client.MutationOptions) (client.ComponentIntegrationMutation, error) {
			adopted = provider == client.IntegrationProviderGrafana && payload.Adopt && options.Revision == 3
			return integrationMutation(provider, integrationTestVersion2, 4), nil
		}}
		implementation := &componentIntegrationResource{api: fake}
		testSchema := integrationTestSchema(t, implementation)
		state := integrationStateModel("grafana", integrationTestVersion1, 3)
		state.LifecycleOwner = types.StringValue("human")
		plan := state
		plan.Adopt = types.BoolValue(true)
		plan.Secret, plan.Version, plan.Revision = types.StringUnknown(), types.StringUnknown(), types.Int64Unknown()
		response := resource.UpdateResponse{State: tfsdk.State{Schema: testSchema}}
		implementation.Update(ctx, resource.UpdateRequest{State: integrationTestState(t, testSchema, state), Plan: integrationTestPlan(t, testSchema, plan)}, &response)
		if response.Diagnostics.HasError() || !adopted {
			t.Fatalf("adopt diagnostics=%v called=%v", response.Diagnostics, adopted)
		}
	})
}

func TestComponentIntegrationImportUsesComponentAndProvider(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	implementation := &componentIntegrationResource{}
	testSchema := integrationTestSchema(t, implementation)
	response := resource.ImportStateResponse{State: tfsdk.State{Schema: testSchema, Raw: tftypes.NewValue(testSchema.Type().TerraformType(ctx), nil)}}
	implementation.ImportState(ctx, resource.ImportStateRequest{ID: integrationTestComponentID + "/pulse"}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("import diagnostics: %v", response.Diagnostics)
	}
	var componentID, provider types.String
	response.State.GetAttribute(ctx, path.Root("component_id"), &componentID)
	response.State.GetAttribute(ctx, path.Root("integration_provider"), &provider)
	if componentID.ValueString() != integrationTestComponentID || provider.ValueString() != "pulse" {
		t.Fatalf("import identity = %q/%q", componentID.ValueString(), provider.ValueString())
	}
	invalid := resource.ImportStateResponse{State: tfsdk.State{Schema: testSchema, Raw: tftypes.NewValue(testSchema.Type().TerraformType(ctx), nil)}}
	implementation.ImportState(ctx, resource.ImportStateRequest{ID: integrationTestComponentID}, &invalid)
	if !invalid.Diagnostics.HasError() {
		t.Fatal("legacy component-only import was accepted")
	}
}

func TestComponentIntegrationReissuesLostSecretForSameProvider(t *testing.T) {
	t.Parallel()
	calls := 0
	fake := &fakeIntegrationAPI{rotate: func(_ context.Context, _ string, provider client.IntegrationProvider, options client.MutationOptions) (client.ComponentIntegrationMutation, error) {
		calls++
		if provider != client.IntegrationProviderPagerDuty || !options.RevokePredecessorImmediately {
			t.Fatalf("reissue options = provider %q %#v", provider, options)
		}
		if calls == 1 {
			return client.ComponentIntegrationMutation{}, &client.ResponseError{
				Code: client.ErrorCodeSecretReissueRequired,
				SecretReissue: &client.SecretReissueMetadata{
					CredentialVersionID: integrationTestVersion1,
					Revision:            5,
				},
			}
		}
		return integrationMutation(provider, integrationTestVersion2, 6), nil
	}}
	implementation := &componentIntegrationResource{api: fake}
	result, err := implementation.reissueSecret(context.Background(), integrationTestComponentID, client.IntegrationProviderPagerDuty, client.SecretReissueMetadata{Revision: 4})
	if err != nil || calls != 2 || result.Secret == nil {
		t.Fatalf("reissue result=%#v err=%v calls=%d", result.Integration, err, calls)
	}
}

func TestComponentIntegrationDeleteUsesNaturalIdentity(t *testing.T) {
	t.Parallel()
	var called bool
	fake := &fakeIntegrationAPI{delete: func(_ context.Context, componentID string, provider client.IntegrationProvider, options client.MutationOptions) error {
		called = componentID == integrationTestComponentID && provider == client.IntegrationProviderGrafana && options.Revision == 2
		return nil
	}}
	implementation := &componentIntegrationResource{api: fake}
	testSchema := integrationTestSchema(t, implementation)
	state := integrationStateModel("grafana", integrationTestVersion1, 2)
	response := resource.DeleteResponse{State: tfsdk.State{Schema: testSchema}}
	implementation.Delete(context.Background(), resource.DeleteRequest{State: integrationTestState(t, testSchema, state)}, &response)
	if response.Diagnostics.HasError() || !called {
		t.Fatalf("delete diagnostics=%v called=%v", response.Diagnostics, called)
	}
}

func integrationMetadata(provider client.IntegrationProvider, version string, revision int64) client.ComponentIntegration {
	return client.ComponentIntegration{
		ComponentID:         integrationTestComponentID,
		Provider:            provider,
		Endpoint:            "https://pulse.example.com/webhooks/components/" + integrationTestComponentID + "/" + string(provider),
		LifecycleOwner:      client.IntegrationLifecycleOwnerAutomation,
		Status:              client.IntegrationStatusActive,
		CredentialVersionID: version,
		Revision:            revision,
	}
}

func integrationMutation(provider client.IntegrationProvider, version string, revision int64) client.ComponentIntegrationMutation {
	return client.ComponentIntegrationMutation{
		Integration: integrationMetadata(provider, version, revision),
		Secret:      &client.ComponentIntegrationSecret{Value: "one-time-secret", VersionID: version},
	}
}

func integrationTestModel(provider string) componentIntegrationResourceModel {
	return componentIntegrationResourceModel{
		ComponentID:         types.StringValue(integrationTestComponentID),
		IntegrationProvider: types.StringValue(provider),
		RotationTrigger:     types.StringValue("initial"),
		Adopt:               types.BoolValue(false),
		Endpoint:            types.StringUnknown(),
		Secret:              types.StringUnknown(),
		Version:             types.StringUnknown(),
		RotationRequired:    types.BoolUnknown(),
		LifecycleOwner:      types.StringUnknown(),
		Revision:            types.Int64Unknown(),
	}
}

func integrationStateModel(provider, version string, revision int64) componentIntegrationResourceModel {
	model := integrationTestModel(provider)
	model.Endpoint = types.StringValue("https://pulse.example.com/webhooks/components/" + integrationTestComponentID + "/" + provider)
	model.Secret = types.StringValue("current-secret")
	model.Version = types.StringValue(version)
	model.RotationRequired = types.BoolValue(false)
	model.LifecycleOwner = types.StringValue("automation")
	model.Revision = types.Int64Value(revision)
	return model
}

func integrationTestSchema(t *testing.T, implementation *componentIntegrationResource) schema.Schema {
	t.Helper()
	var response resource.SchemaResponse
	implementation.Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	return response.Schema
}

func integrationTestConfig(t *testing.T, testSchema schema.Schema, model componentIntegrationResourceModel) tfsdk.Config {
	t.Helper()
	plan := integrationTestPlan(t, testSchema, model)
	return tfsdk.Config{Schema: testSchema, Raw: plan.Raw}
}

func integrationTestPlan(t *testing.T, testSchema schema.Schema, model componentIntegrationResourceModel) tfsdk.Plan {
	t.Helper()
	value := tfsdk.Plan{Schema: testSchema}
	if diagnostics := value.Set(context.Background(), &model); diagnostics.HasError() {
		t.Fatalf("set plan: %v", diagnostics)
	}
	return value
}

func integrationTestState(t *testing.T, testSchema schema.Schema, model componentIntegrationResourceModel) tfsdk.State {
	t.Helper()
	value := tfsdk.State{Schema: testSchema}
	if diagnostics := value.Set(context.Background(), &model); diagnostics.HasError() {
		t.Fatalf("set state: %v", diagnostics)
	}
	return value
}

var _ client.IntegrationAPI = (*fakeIntegrationAPI)(nil)

func assertIntegrationNoDiagnostics(t *testing.T, diagnostics interface{ HasError() bool }) {
	t.Helper()
	if diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diagnostics)
	}
}
