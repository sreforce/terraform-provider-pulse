package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/sreforce/terraform-provider-pulse/internal/client"
)

type tagDataSource struct {
	client catalogReader
}

type tagDataSourceModel struct {
	ID           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	Purpose      types.String `tfsdk:"purpose"`
	DisplayLabel types.String `tfsdk:"display_label"`
	DisplayOrder types.Int64  `tfsdk:"display_order"`
	Icon         types.String `tfsdk:"icon"`
}

func NewTagDataSource() datasource.DataSource {
	return &tagDataSource{}
}

func (d *tagDataSource) Metadata(
	_ context.Context,
	request datasource.MetadataRequest,
	response *datasource.MetadataResponse,
) {
	response.TypeName = request.ProviderTypeName + "_tag"
}

func (d *tagDataSource) Schema(
	_ context.Context,
	_ datasource.SchemaRequest,
	response *datasource.SchemaResponse,
) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Resolves one tag in the organization derived from the configured automation credential. Configure either `id`, or the exact `purpose` and `name` pair.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Tag UUID. Configure this alone, or configure `purpose` and `name` instead.",
				Optional:            true,
				Computed:            true,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Exact tag name. Must be configured with `purpose` when `id` is omitted.",
				Optional:            true,
				Computed:            true,
			},
			"purpose": schema.StringAttribute{
				MarkdownDescription: "Tag purpose, `relevance` or `filter`. Must be configured with `name` when `id` is omitted.",
				Optional:            true,
				Computed:            true,
			},
			"display_label": schema.StringAttribute{
				MarkdownDescription: "Optional human-readable tag label.",
				Computed:            true,
			},
			"display_order": schema.Int64Attribute{
				MarkdownDescription: "Organization-defined display order for this tag.",
				Computed:            true,
			},
			"icon": schema.StringAttribute{
				MarkdownDescription: "Optional tag icon identifier.",
				Computed:            true,
			},
		},
	}
}

func (d *tagDataSource) Configure(
	_ context.Context,
	request datasource.ConfigureRequest,
	response *datasource.ConfigureResponse,
) {
	d.client = configureCatalogDataSource(request.ProviderData, response)
}

func (d *tagDataSource) ValidateConfig(
	ctx context.Context,
	request datasource.ValidateConfigRequest,
	response *datasource.ValidateConfigResponse,
) {
	var data tagDataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &data)...)
	if response.Diagnostics.HasError() {
		return
	}
	tagSelector(data.ID, data.Name, data.Purpose, &response.Diagnostics)
}

func (d *tagDataSource) Read(
	ctx context.Context,
	request datasource.ReadRequest,
	response *datasource.ReadResponse,
) {
	var data tagDataSourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &data)...)
	if response.Diagnostics.HasError() {
		return
	}

	selector, valid := tagSelector(data.ID, data.Name, data.Purpose, &response.Diagnostics)
	if !valid {
		return
	}
	if d.client == nil {
		response.Diagnostics.AddError(
			"Pulse client is not configured",
			"The tag data source cannot be read before the Pulse provider is configured.",
		)
		return
	}

	item, err := lookupCatalogItem(
		ctx,
		"tag",
		selector,
		d.client.ListTags,
		func(item client.Tag) string { return item.ID },
		func(item client.Tag) string { return item.Name },
		func(item client.Tag) string { return item.Purpose },
	)
	if err != nil {
		addCatalogLookupDiagnostic("tag", err, &response.Diagnostics)
		return
	}

	data.ID = types.StringValue(item.ID)
	data.Name = types.StringValue(item.Name)
	data.Purpose = types.StringValue(item.Purpose)
	data.DisplayLabel = stringValueOrNull(item.DisplayLabel)
	data.DisplayOrder = types.Int64Value(item.DisplayOrder)
	data.Icon = stringValueOrNull(item.Icon)
	response.Diagnostics.Append(response.State.Set(ctx, &data)...)
}

var (
	_ datasource.DataSource                   = (*tagDataSource)(nil)
	_ datasource.DataSourceWithConfigure      = (*tagDataSource)(nil)
	_ datasource.DataSourceWithValidateConfig = (*tagDataSource)(nil)
)
