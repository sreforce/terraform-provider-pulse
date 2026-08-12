package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/sreforce/terraform-provider-pulse/internal/client"
)

type componentTypeDataSource struct {
	client catalogReader
}

type componentTypeDataSourceModel struct {
	ID           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	GreenLabel   types.String `tfsdk:"green_label"`
	YellowLabel  types.String `tfsdk:"yellow_label"`
	RedLabel     types.String `tfsdk:"red_label"`
	UnknownLabel types.String `tfsdk:"unknown_label"`
}

func NewComponentTypeDataSource() datasource.DataSource {
	return &componentTypeDataSource{}
}

func (d *componentTypeDataSource) Metadata(
	_ context.Context,
	request datasource.MetadataRequest,
	response *datasource.MetadataResponse,
) {
	response.TypeName = request.ProviderTypeName + "_component_type"
}

func (d *componentTypeDataSource) Schema(
	_ context.Context,
	_ datasource.SchemaRequest,
	response *datasource.SchemaResponse,
) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Resolves one component type in the organization derived from the configured automation credential. Configure exactly one of `id` or exact `name`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Component type UUID. Configure this or `name`.",
				Optional:            true,
				Computed:            true,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Exact component type name. Configure this or `id`.",
				Optional:            true,
				Computed:            true,
			},
			"green_label": schema.StringAttribute{
				MarkdownDescription: "Label used for green component state.",
				Computed:            true,
			},
			"yellow_label": schema.StringAttribute{
				MarkdownDescription: "Label used for yellow component state.",
				Computed:            true,
			},
			"red_label": schema.StringAttribute{
				MarkdownDescription: "Label used for red component state.",
				Computed:            true,
			},
			"unknown_label": schema.StringAttribute{
				MarkdownDescription: "Label used for unknown component state.",
				Computed:            true,
			},
		},
	}
}

func (d *componentTypeDataSource) Configure(
	_ context.Context,
	request datasource.ConfigureRequest,
	response *datasource.ConfigureResponse,
) {
	d.client = configureCatalogDataSource(request.ProviderData, response)
}

func (d *componentTypeDataSource) ValidateConfig(
	ctx context.Context,
	request datasource.ValidateConfigRequest,
	response *datasource.ValidateConfigResponse,
) {
	var data componentTypeDataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &data)...)
	if response.Diagnostics.HasError() {
		return
	}
	validateIDOrNameSelectorConfig(data.ID, data.Name, &response.Diagnostics)
}

func (d *componentTypeDataSource) Read(
	ctx context.Context,
	request datasource.ReadRequest,
	response *datasource.ReadResponse,
) {
	var data componentTypeDataSourceModel
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
			"The component type data source cannot be read before the Pulse provider is configured.",
		)
		return
	}

	item, err := lookupCatalogItem(
		ctx,
		"component type",
		selector,
		d.client.ListComponentTypes,
		func(item client.ComponentType) string { return item.ID },
		func(item client.ComponentType) string { return item.Name },
		nil,
	)
	if err != nil {
		addCatalogLookupDiagnostic("component type", err, &response.Diagnostics)
		return
	}

	data.ID = types.StringValue(item.ID)
	data.Name = types.StringValue(item.Name)
	data.GreenLabel = types.StringValue(item.GreenLabel)
	data.YellowLabel = types.StringValue(item.YellowLabel)
	data.RedLabel = types.StringValue(item.RedLabel)
	data.UnknownLabel = types.StringValue(item.UnknownLabel)
	response.Diagnostics.Append(response.State.Set(ctx, &data)...)
}

var (
	_ datasource.DataSource                   = (*componentTypeDataSource)(nil)
	_ datasource.DataSourceWithConfigure      = (*componentTypeDataSource)(nil)
	_ datasource.DataSourceWithValidateConfig = (*componentTypeDataSource)(nil)
)
