package provider

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/sreforce/terraform-provider-pulse/internal/client"
)

const (
	apiURLEnvironmentVariable = "PULSE_API_URL"
	tokenEnvironmentVariable  = "PULSE_API_TOKEN"
)

type clientFactory func(client.Config) (client.API, error)

// PulseProvider implements the Pulse Terraform provider.
type PulseProvider struct {
	version   string
	getenv    func(string) string
	newClient clientFactory
}

type pulseProviderModel struct {
	APIURL types.String `tfsdk:"api_url"`
	Token  types.String `tfsdk:"token"`
}

// Metadata returns provider type and version metadata.
func (p *PulseProvider) Metadata(_ context.Context, _ provider.MetadataRequest, response *provider.MetadataResponse) {
	response.TypeName = "pulse"
	response.Version = p.version
}

// Schema returns provider configuration schema.
func (p *PulseProvider) Schema(_ context.Context, _ provider.SchemaRequest, response *provider.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "The Pulse provider manages organization-scoped Pulse configuration.",
		Attributes: map[string]schema.Attribute{
			"api_url": schema.StringAttribute{
				MarkdownDescription: "Base URL of the Pulse API. May also be set with `PULSE_API_URL`.",
				Optional:            true,
			},
			"token": schema.StringAttribute{
				MarkdownDescription: "Organization-scoped Pulse automation token. May also be set with `PULSE_API_TOKEN`.",
				Optional:            true,
				Sensitive:           true,
			},
		},
	}
}

// Configure resolves environment-backed configuration and creates the shared API client.
func (p *PulseProvider) Configure(ctx context.Context, request provider.ConfigureRequest, response *provider.ConfigureResponse) {
	var data pulseProviderModel
	response.Diagnostics.Append(request.Config.Get(ctx, &data)...)
	if response.Diagnostics.HasError() {
		return
	}

	config, diagnostics := resolveConfiguration(data, p.getenv)
	response.Diagnostics.Append(diagnostics...)
	if response.Diagnostics.HasError() {
		return
	}

	config.UserAgent = fmt.Sprintf("terraform-provider-pulse/%s", p.version)
	configuredClient, err := p.newClient(config)
	if err != nil {
		response.Diagnostics.AddError(
			"Unable to create Pulse API client",
			"The provider could not create a Pulse API client from the supplied configuration: "+err.Error(),
		)
		return
	}

	tflog.Info(ctx, "Configured Pulse API client", map[string]any{"provider_version": p.version})
	response.DataSourceData = configuredClient
	response.ResourceData = configuredClient
}

// Resources intentionally remains empty until stable automation API contracts exist.
func (p *PulseProvider) Resources(context.Context) []func() resource.Resource {
	return nil
}

// DataSources intentionally remains empty until stable automation API contracts exist.
func (p *PulseProvider) DataSources(context.Context) []func() datasource.DataSource {
	return nil
}

func resolveConfiguration(data pulseProviderModel, getenv func(string) string) (client.Config, diag.Diagnostics) {
	var diagnostics diag.Diagnostics

	apiURL := getenv(apiURLEnvironmentVariable)
	if data.APIURL.IsUnknown() {
		diagnostics.AddAttributeError(
			path.Root("api_url"),
			"Unknown Pulse API URL",
			"The Pulse API URL must be known while configuring the provider. Set it directly or use "+apiURLEnvironmentVariable+".",
		)
	} else if !data.APIURL.IsNull() {
		apiURL = data.APIURL.ValueString()
	}

	token := getenv(tokenEnvironmentVariable)
	if data.Token.IsUnknown() {
		diagnostics.AddAttributeError(
			path.Root("token"),
			"Unknown Pulse API token",
			"The Pulse API token must be known while configuring the provider. Set it directly or use "+tokenEnvironmentVariable+".",
		)
	} else if !data.Token.IsNull() {
		token = data.Token.ValueString()
	}

	if strings.TrimSpace(apiURL) == "" {
		diagnostics.AddAttributeError(
			path.Root("api_url"),
			"Missing Pulse API URL",
			"Set the Pulse API URL in the provider configuration or with "+apiURLEnvironmentVariable+".",
		)
	}
	if strings.TrimSpace(token) == "" {
		diagnostics.AddAttributeError(
			path.Root("token"),
			"Missing Pulse API token",
			"Set an organization-scoped automation token in the provider configuration or with "+tokenEnvironmentVariable+".",
		)
	}

	return client.Config{BaseURL: apiURL, Token: token}, diagnostics
}

// New returns a protocol-compatible provider factory.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &PulseProvider{
			version: version,
			getenv:  os.Getenv,
			newClient: func(config client.Config) (client.API, error) {
				return client.New(config)
			},
		}
	}
}

var _ provider.Provider = (*PulseProvider)(nil)
