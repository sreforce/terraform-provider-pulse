package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type componentIntegrationResourceV0Model struct {
	ID               types.String `tfsdk:"id"`
	ComponentID      types.String `tfsdk:"component_id"`
	Source           types.String `tfsdk:"source"`
	SourceKey        types.String `tfsdk:"source_key"`
	RotationTrigger  types.String `tfsdk:"rotation_trigger"`
	Adopt            types.Bool   `tfsdk:"adopt"`
	Endpoint         types.String `tfsdk:"endpoint"`
	Secret           types.String `tfsdk:"secret"`
	ObservedVersion  types.String `tfsdk:"observed_version"`
	RotationRequired types.Bool   `tfsdk:"rotation_required"`
	LifecycleOwner   types.String `tfsdk:"lifecycle_owner"`
	Revision         types.Int64  `tfsdk:"revision"`
	Status           types.String `tfsdk:"status"`
}

// UpgradeState converts v0's Grafana-only integration UUID/source/source_key
// state into the natural (component, provider) identity without discarding a
// still-valid one-time secret.
func (r *componentIntegrationResource) UpgradeState(context.Context) map[int64]resource.StateUpgrader {
	prior := componentIntegrationResourceV0Schema()
	return map[int64]resource.StateUpgrader{
		0: {
			PriorSchema: &prior,
			StateUpgrader: func(ctx context.Context, request resource.UpgradeStateRequest, response *resource.UpgradeStateResponse) {
				var previous componentIntegrationResourceV0Model
				response.Diagnostics.Append(request.State.Get(ctx, &previous)...)
				if response.Diagnostics.HasError() {
					return
				}
				provider := previous.Source
				if provider.IsNull() || provider.IsUnknown() || provider.ValueString() == "" {
					provider = types.StringValue("grafana")
				}
				upgraded := componentIntegrationResourceModel{
					ComponentID:         previous.ComponentID,
					IntegrationProvider: provider,
					RotationTrigger:     previous.RotationTrigger,
					Adopt:               previous.Adopt,
					Endpoint:            previous.Endpoint,
					Secret:              previous.Secret,
					Version:             previous.ObservedVersion,
					RotationRequired:    previous.RotationRequired,
					LifecycleOwner:      previous.LifecycleOwner,
					Revision:            previous.Revision,
				}
				response.Diagnostics.Append(response.State.Set(ctx, &upgraded)...)
			},
		},
	}
}

func componentIntegrationResourceV0Schema() schema.Schema {
	useStringState := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	return schema.Schema{Attributes: map[string]schema.Attribute{
		"id": schema.StringAttribute{Computed: true, PlanModifiers: useStringState},
		"component_id": schema.StringAttribute{
			Required:      true,
			PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
		},
		"source": schema.StringAttribute{
			Required:      true,
			PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
		},
		"source_key": schema.StringAttribute{
			Required:      true,
			PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
		},
		"rotation_trigger": schema.StringAttribute{Required: true},
		"adopt": schema.BoolAttribute{
			Optional: true,
			Computed: true,
			Default:  booldefault.StaticBool(false),
		},
		"endpoint":         schema.StringAttribute{Computed: true, PlanModifiers: useStringState},
		"secret":           schema.StringAttribute{Computed: true, Sensitive: true, PlanModifiers: useStringState},
		"observed_version": schema.StringAttribute{Computed: true, PlanModifiers: useStringState},
		"rotation_required": schema.BoolAttribute{
			Computed:      true,
			PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
		},
		"lifecycle_owner": schema.StringAttribute{Computed: true, PlanModifiers: useStringState},
		"revision": schema.Int64Attribute{
			Computed:      true,
			PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
		},
		"status": schema.StringAttribute{Computed: true, PlanModifiers: useStringState},
	}}
}

var _ resource.ResourceWithUpgradeState = (*componentIntegrationResource)(nil)
