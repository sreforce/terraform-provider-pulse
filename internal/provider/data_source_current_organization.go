package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type currentOrganizationDataSource struct {
	client catalogReader
}

type currentOrganizationDataSourceModel struct {
	ID   types.String `tfsdk:"id"`
	Name types.String `tfsdk:"name"`
	Slug types.String `tfsdk:"slug"`
}

func NewCurrentOrganizationDataSource() datasource.DataSource {
	return &currentOrganizationDataSource{}
}

func (d *currentOrganizationDataSource) Metadata(
	_ context.Context,
	request datasource.MetadataRequest,
	response *datasource.MetadataResponse,
) {
	response.TypeName = request.ProviderTypeName + "_current_organization"
}

func (d *currentOrganizationDataSource) Schema(
	_ context.Context,
	_ datasource.SchemaRequest,
	response *datasource.SchemaResponse,
) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Returns the Pulse organization derived from the configured automation credential. The caller cannot select another organization.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Pulse organization UUID.",
				Computed:            true,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Organization display name.",
				Computed:            true,
			},
			"slug": schema.StringAttribute{
				MarkdownDescription: "Organization slug.",
				Computed:            true,
			},
		},
	}
}

func (d *currentOrganizationDataSource) Configure(
	_ context.Context,
	request datasource.ConfigureRequest,
	response *datasource.ConfigureResponse,
) {
	d.client = configureCatalogDataSource(request.ProviderData, response)
}

func (d *currentOrganizationDataSource) Read(
	ctx context.Context,
	_ datasource.ReadRequest,
	response *datasource.ReadResponse,
) {
	if d.client == nil {
		response.Diagnostics.AddError(
			"Pulse client is not configured",
			"The current organization data source cannot be read before the Pulse provider is configured.",
		)
		return
	}

	organization, err := d.client.CurrentOrganization(ctx)
	if err != nil {
		response.Diagnostics.AddError(
			"Unable to read Pulse organization",
			"The Pulse automation API could not return the credential's organization: "+err.Error(),
		)
		return
	}

	state := currentOrganizationDataSourceModel{
		ID:   types.StringValue(organization.ID),
		Name: types.StringValue(organization.Name),
		Slug: types.StringValue(organization.Slug),
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

var (
	_ datasource.DataSource              = (*currentOrganizationDataSource)(nil)
	_ datasource.DataSourceWithConfigure = (*currentOrganizationDataSource)(nil)
)
