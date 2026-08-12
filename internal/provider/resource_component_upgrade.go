package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type componentResourceV0Model struct {
	ID                    types.String `tfsdk:"id"`
	ExternalKey           types.String `tfsdk:"external_key"`
	Kind                  types.String `tfsdk:"kind"`
	Name                  types.String `tfsdk:"name"`
	ComponentTypeID       types.String `tfsdk:"component_type_id"`
	OwnerTeamID           types.String `tfsdk:"owner_team_id"`
	RelevanceTagIDs       types.Set    `tfsdk:"relevance_tag_ids"`
	FilterTagIDs          types.Set    `tfsdk:"filter_tag_ids"`
	AlertEnabled          types.Bool   `tfsdk:"alert_enabled"`
	State                 types.String `tfsdk:"state"`
	ConfigurationRevision types.Int64  `tfsdk:"configuration_revision"`
}

// UpgradeState drops v0's external/rollup discriminator. Components in v0.2
// may receive direct signals and own children at the same time.
func (r *ComponentResource) UpgradeState(context.Context) map[int64]resource.StateUpgrader {
	prior := componentResourceV0Schema()
	return map[int64]resource.StateUpgrader{
		0: {
			PriorSchema: &prior,
			StateUpgrader: func(ctx context.Context, request resource.UpgradeStateRequest, response *resource.UpgradeStateResponse) {
				var previous componentResourceV0Model
				response.Diagnostics.Append(request.State.Get(ctx, &previous)...)
				if response.Diagnostics.HasError() {
					return
				}
				upgraded := componentResourceModel{
					ID:                    previous.ID,
					ExternalKey:           previous.ExternalKey,
					Name:                  previous.Name,
					ComponentTypeID:       previous.ComponentTypeID,
					OwnerTeamID:           previous.OwnerTeamID,
					RelevanceTagIDs:       previous.RelevanceTagIDs,
					FilterTagIDs:          previous.FilterTagIDs,
					AlertEnabled:          previous.AlertEnabled,
					State:                 previous.State,
					ConfigurationRevision: previous.ConfigurationRevision,
				}
				response.Diagnostics.Append(response.State.Set(ctx, &upgraded)...)
			},
		},
	}
}

func componentResourceV0Schema() schema.Schema {
	return schema.Schema{Attributes: map[string]schema.Attribute{
		"id": schema.StringAttribute{Computed: true},
		"external_key": schema.StringAttribute{
			Required:      true,
			Validators:    []validator.String{nonBlankStringValidator{}},
			PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
		},
		"kind": schema.StringAttribute{
			Required:      true,
			PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
		},
		"name":              schema.StringAttribute{Required: true, Validators: []validator.String{nonBlankStringValidator{}}},
		"component_type_id": schema.StringAttribute{Required: true, Validators: []validator.String{nonBlankStringValidator{}}},
		"owner_team_id":     schema.StringAttribute{Optional: true, Validators: []validator.String{nonBlankStringValidator{}}},
		"relevance_tag_ids": schema.SetAttribute{
			ElementType: types.StringType,
			Optional:    true,
			Computed:    true,
			Default:     setdefault.StaticValue(types.SetValueMust(types.StringType, []attr.Value{})),
		},
		"filter_tag_ids": schema.SetAttribute{
			ElementType: types.StringType,
			Optional:    true,
			Computed:    true,
			Default:     setdefault.StaticValue(types.SetValueMust(types.StringType, []attr.Value{})),
		},
		"alert_enabled": schema.BoolAttribute{
			Optional: true,
			Computed: true,
			Default:  booldefault.StaticBool(false),
		},
		"state":                  schema.StringAttribute{Computed: true},
		"configuration_revision": schema.Int64Attribute{Computed: true},
	}}
}

var _ resource.ResourceWithUpgradeState = (*ComponentResource)(nil)
