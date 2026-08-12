package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestComponentV0StateUpgradeDropsKindWithoutLosingState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	implementation := &ComponentResource{}
	currentSchema := componentTestSchema(t, implementation)
	upgrader := implementation.UpgradeState(ctx)[0]
	prior := componentResourceV0Model{
		ID:                    types.StringValue("00000000-0000-4000-8000-000000000101"),
		ExternalKey:           types.StringValue("production/platform/example-service"),
		Kind:                  types.StringValue("rollup"),
		Name:                  types.StringValue("Example service"),
		ComponentTypeID:       types.StringValue("00000000-0000-4000-8000-000000000201"),
		OwnerTeamID:           types.StringNull(),
		RelevanceTagIDs:       types.SetValueMust(types.StringType, []attr.Value{}),
		FilterTagIDs:          types.SetValueMust(types.StringType, []attr.Value{}),
		AlertEnabled:          types.BoolValue(false),
		State:                 types.StringValue("yellow"),
		ConfigurationRevision: types.Int64Value(7),
	}
	priorState := tfsdk.State{Schema: *upgrader.PriorSchema}
	if diagnostics := priorState.Set(ctx, &prior); diagnostics.HasError() {
		t.Fatalf("set v0 component state: %v", diagnostics)
	}
	response := resource.UpgradeStateResponse{State: tfsdk.State{Schema: currentSchema}}
	upgrader.StateUpgrader(ctx, resource.UpgradeStateRequest{State: &priorState}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("upgrade component state: %v", response.Diagnostics)
	}
	var upgraded componentResourceModel
	if diagnostics := response.State.Get(ctx, &upgraded); diagnostics.HasError() {
		t.Fatalf("read upgraded component state: %v", diagnostics)
	}
	if upgraded.ExternalKey.ValueString() != prior.ExternalKey.ValueString() || upgraded.State.ValueString() != "yellow" || upgraded.ConfigurationRevision.ValueInt64() != 7 {
		t.Fatalf("upgraded component = %#v", upgraded)
	}
}

func TestIntegrationV0StateUpgradeUsesNaturalIdentityAndPreservesSecret(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	implementation := &componentIntegrationResource{}
	currentSchema := integrationTestSchema(t, implementation)
	upgrader := implementation.UpgradeState(ctx)[0]
	prior := componentIntegrationResourceV0Model{
		ID:               types.StringValue("00000000-0000-4000-8000-000000000301"),
		ComponentID:      types.StringValue(integrationTestComponentID),
		Source:           types.StringValue("grafana"),
		SourceKey:        types.StringValue("production/platform/example-alert"),
		RotationTrigger:  types.StringValue("initial"),
		Adopt:            types.BoolValue(false),
		Endpoint:         types.StringValue("https://pulse.example.com/webhooks/component-integrations/00000000-0000-4000-8000-000000000301/grafana"),
		Secret:           types.StringValue("still-valid-secret"),
		ObservedVersion:  types.StringValue(integrationTestVersion1),
		RotationRequired: types.BoolValue(false),
		LifecycleOwner:   types.StringValue("automation"),
		Revision:         types.Int64Value(4),
		Status:           types.StringValue("active"),
	}
	priorState := tfsdk.State{Schema: *upgrader.PriorSchema}
	if diagnostics := priorState.Set(ctx, &prior); diagnostics.HasError() {
		t.Fatalf("set v0 integration state: %v", diagnostics)
	}
	response := resource.UpgradeStateResponse{State: tfsdk.State{Schema: currentSchema}}
	upgrader.StateUpgrader(ctx, resource.UpgradeStateRequest{State: &priorState}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("upgrade integration state: %v", response.Diagnostics)
	}
	var upgraded componentIntegrationResourceModel
	if diagnostics := response.State.Get(ctx, &upgraded); diagnostics.HasError() {
		t.Fatalf("read upgraded integration state: %v", diagnostics)
	}
	if upgraded.ComponentID.ValueString() != integrationTestComponentID || upgraded.IntegrationProvider.ValueString() != "grafana" {
		t.Fatalf("upgraded natural identity = %q/%q", upgraded.ComponentID.ValueString(), upgraded.IntegrationProvider.ValueString())
	}
	if upgraded.Secret.ValueString() != "still-valid-secret" || upgraded.Version.ValueString() != integrationTestVersion1 || upgraded.Revision.ValueInt64() != 4 {
		t.Fatalf("upgraded secret metadata = %#v", upgraded)
	}
}
