package provider

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type componentDataSource struct {
	client componentReader
}

type componentDataSourceModel struct {
	ID                    types.String `tfsdk:"id"`
	ExternalKey           types.String `tfsdk:"external_key"`
	Name                  types.String `tfsdk:"name"`
	ComponentTypeID       types.String `tfsdk:"component_type_id"`
	OwnerTeamID           types.String `tfsdk:"owner_team_id"`
	RelevanceTagIDs       types.Set    `tfsdk:"relevance_tag_ids"`
	FilterTagIDs          types.Set    `tfsdk:"filter_tag_ids"`
	AlertEnabled          types.Bool   `tfsdk:"alert_enabled"`
	State                 types.String `tfsdk:"state"`
	StateReason           types.String `tfsdk:"state_reason"`
	ConfigurationRevision types.Int64  `tfsdk:"configuration_revision"`
}

func NewComponentDataSource() datasource.DataSource {
	return &componentDataSource{}
}

func (d *componentDataSource) Metadata(
	_ context.Context,
	request datasource.MetadataRequest,
	response *datasource.MetadataResponse,
) {
	response.TypeName = request.ProviderTypeName + "_component"
}

func (d *componentDataSource) Schema(
	_ context.Context,
	_ datasource.SchemaRequest,
	response *datasource.SchemaResponse,
) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Returns one active Pulse component by UUID in the organization derived from the configured automation credential. Display names are never component identity.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Pulse component UUID. Archived or foreign-organization UUIDs are not visible.",
				Required:            true,
			},
			"external_key": schema.StringAttribute{
				MarkdownDescription: "Immutable organization-unique automation key.",
				Computed:            true,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Human-readable display name. Names may be duplicated.",
				Computed:            true,
			},
			"component_type_id": schema.StringAttribute{
				MarkdownDescription: "Component type UUID.",
				Computed:            true,
			},
			"owner_team_id": schema.StringAttribute{
				MarkdownDescription: "Owning team UUID, when assigned.",
				Computed:            true,
			},
			"relevance_tag_ids": schema.SetAttribute{
				MarkdownDescription: "Organization relevance-tag UUIDs assigned to the component.",
				ElementType:         types.StringType,
				Computed:            true,
			},
			"filter_tag_ids": schema.SetAttribute{
				MarkdownDescription: "Organization filter-tag UUIDs assigned to the component.",
				ElementType:         types.StringType,
				Computed:            true,
			},
			"alert_enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether Pulse alert-routing side effects are enabled for this component.",
				Computed:            true,
			},
			"state": schema.StringAttribute{
				MarkdownDescription: "Computed runtime state. This value is observational and is not component configuration.",
				Computed:            true,
			},
			"state_reason": schema.StringAttribute{
				MarkdownDescription: "Computed runtime state reason, when present.",
				Computed:            true,
			},
			"configuration_revision": schema.Int64Attribute{
				MarkdownDescription: "Component configuration revision.",
				Computed:            true,
			},
		},
	}
}

func (d *componentDataSource) Configure(
	_ context.Context,
	request datasource.ConfigureRequest,
	response *datasource.ConfigureResponse,
) {
	d.client = configureComponentDataSource(request.ProviderData, response)
}

func (d *componentDataSource) Read(
	ctx context.Context,
	request datasource.ReadRequest,
	response *datasource.ReadResponse,
) {
	var data componentDataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &data)...)
	if response.Diagnostics.HasError() {
		return
	}

	if data.ID.IsUnknown() || data.ID.IsNull() || strings.TrimSpace(data.ID.ValueString()) == "" {
		response.Diagnostics.AddError(
			"Invalid component UUID",
			"The component id must be a known, non-empty Pulse UUID. Components cannot be selected by display name.",
		)
		return
	}
	if d.client == nil {
		response.Diagnostics.AddError(
			"Pulse client is not configured",
			"The component data source cannot be read before the Pulse provider is configured.",
		)
		return
	}

	component, err := d.client.GetComponent(ctx, data.ID.ValueString())
	if err != nil {
		response.Diagnostics.AddError(
			"Unable to read Pulse component",
			"The Pulse automation API could not return the component in the credential's organization: "+err.Error(),
		)
		return
	}

	data.ID = types.StringValue(component.ID)
	data.ExternalKey = types.StringValue(component.ExternalKey)
	data.Name = types.StringValue(component.Name)
	data.ComponentTypeID = types.StringValue(component.ComponentTypeID)
	data.OwnerTeamID = stringValueOrNull(component.OwnerTeamID)
	data.RelevanceTagIDs = stringSetValue(ctx, component.RelevanceTagIDs, &response.Diagnostics)
	data.FilterTagIDs = stringSetValue(ctx, component.FilterTagIDs, &response.Diagnostics)
	data.AlertEnabled = types.BoolValue(component.AlertEnabled)
	data.State = types.StringValue(string(component.State))
	data.StateReason = stringValueOrNull(component.StateReason)
	data.ConfigurationRevision = types.Int64Value(component.Revision)
	if response.Diagnostics.HasError() {
		return
	}

	response.Diagnostics.Append(response.State.Set(ctx, &data)...)
}

var (
	_ datasource.DataSource              = (*componentDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*componentDataSource)(nil)
)
