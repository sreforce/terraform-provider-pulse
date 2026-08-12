package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/sreforce/terraform-provider-pulse/internal/client"
)

type teamDataSource struct {
	client catalogReader
}

type teamDataSourceModel struct {
	ID               types.String `tfsdk:"id"`
	Name             types.String `tfsdk:"name"`
	SettingsPriority types.Int64  `tfsdk:"settings_priority"`
	Revision         types.Int64  `tfsdk:"revision"`
}

func NewTeamDataSource() datasource.DataSource {
	return &teamDataSource{}
}

func (d *teamDataSource) Metadata(
	_ context.Context,
	request datasource.MetadataRequest,
	response *datasource.MetadataResponse,
) {
	response.TypeName = request.ProviderTypeName + "_team"
}

func (d *teamDataSource) Schema(
	_ context.Context,
	_ datasource.SchemaRequest,
	response *datasource.SchemaResponse,
) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Resolves one team in the organization derived from the configured automation credential. Configure exactly one of `id` or exact `name`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Team UUID. Configure this or `name`.",
				Optional:            true,
				Computed:            true,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Exact team name. Configure this or `id`.",
				Optional:            true,
				Computed:            true,
			},
			"settings_priority": schema.Int64Attribute{
				MarkdownDescription: "Priority used when resolving team-scoped settings.",
				Computed:            true,
			},
			"revision": schema.Int64Attribute{
				MarkdownDescription: "Current team configuration revision.",
				Computed:            true,
			},
		},
	}
}

func (d *teamDataSource) Configure(
	_ context.Context,
	request datasource.ConfigureRequest,
	response *datasource.ConfigureResponse,
) {
	d.client = configureCatalogDataSource(request.ProviderData, response)
}

func (d *teamDataSource) ValidateConfig(
	ctx context.Context,
	request datasource.ValidateConfigRequest,
	response *datasource.ValidateConfigResponse,
) {
	var data teamDataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &data)...)
	if response.Diagnostics.HasError() {
		return
	}
	validateIDOrNameSelectorConfig(data.ID, data.Name, &response.Diagnostics)
}

func (d *teamDataSource) Read(
	ctx context.Context,
	request datasource.ReadRequest,
	response *datasource.ReadResponse,
) {
	var data teamDataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &data)...)
	if response.Diagnostics.HasError() {
		return
	}

	selector, valid := idOrNameSelector(data.ID, data.Name, &response.Diagnostics)
	if !valid {
		return
	}
	if d.client == nil {
		response.Diagnostics.AddError(
			"Pulse client is not configured",
			"The team data source cannot be read before the Pulse provider is configured.",
		)
		return
	}

	item, err := lookupCatalogItem(
		ctx,
		"team",
		selector,
		d.client.ListTeams,
		func(item client.Team) string { return item.ID },
		func(item client.Team) string { return item.Name },
		nil,
	)
	if err != nil {
		addCatalogLookupDiagnostic("team", err, &response.Diagnostics)
		return
	}

	data.ID = types.StringValue(item.ID)
	data.Name = types.StringValue(item.Name)
	data.SettingsPriority = types.Int64Value(item.SettingsPriority)
	data.Revision = types.Int64Value(item.Revision)
	response.Diagnostics.Append(response.State.Set(ctx, &data)...)
}

var (
	_ datasource.DataSource                   = (*teamDataSource)(nil)
	_ datasource.DataSourceWithConfigure      = (*teamDataSource)(nil)
	_ datasource.DataSourceWithValidateConfig = (*teamDataSource)(nil)
)
