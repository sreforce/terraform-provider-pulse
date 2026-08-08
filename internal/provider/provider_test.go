package provider

import (
	"context"
	"sort"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	providerschema "github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/sreforce/terraform-provider-pulse/internal/client"
)

func TestMetadata(t *testing.T) {
	t.Parallel()

	implementation := New("1.2.3")()
	var response provider.MetadataResponse
	implementation.Metadata(context.Background(), provider.MetadataRequest{}, &response)

	if got, want := response.TypeName, "pulse"; got != want {
		t.Fatalf("type name = %q, want %q", got, want)
	}
	if got, want := response.Version, "1.2.3"; got != want {
		t.Fatalf("version = %q, want %q", got, want)
	}
}

func TestProviderRegistersInitialContractSurface(t *testing.T) {
	t.Parallel()

	implementation := New("test")().(*PulseProvider)

	var resourceTypes []string
	for _, factory := range implementation.Resources(context.Background()) {
		var response resource.MetadataResponse
		factory().Metadata(
			context.Background(),
			resource.MetadataRequest{ProviderTypeName: "pulse"},
			&response,
		)
		resourceTypes = append(resourceTypes, response.TypeName)
	}
	sort.Strings(resourceTypes)
	assertStringsEqual(t, resourceTypes, []string{
		"pulse_component",
		"pulse_component_integration",
		"pulse_component_rollup",
	})

	var dataSourceTypes []string
	for _, factory := range implementation.DataSources(context.Background()) {
		var response datasource.MetadataResponse
		factory().Metadata(
			context.Background(),
			datasource.MetadataRequest{ProviderTypeName: "pulse"},
			&response,
		)
		dataSourceTypes = append(dataSourceTypes, response.TypeName)
	}
	sort.Strings(dataSourceTypes)
	assertStringsEqual(t, dataSourceTypes, []string{
		"pulse_component",
		"pulse_component_type",
		"pulse_current_organization",
		"pulse_tag",
		"pulse_team",
	})
}

func assertStringsEqual(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("values = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("values = %v, want %v", got, want)
		}
	}
}

func TestSchemaMarksTokenSensitive(t *testing.T) {
	t.Parallel()

	implementation := New("test")()
	var response provider.SchemaResponse
	implementation.Schema(context.Background(), provider.SchemaRequest{}, &response)

	tokenAttribute, ok := response.Schema.Attributes["token"].(providerschema.StringAttribute)
	if !ok {
		t.Fatalf("token schema type = %T, want schema.StringAttribute", response.Schema.Attributes["token"])
	}
	if !tokenAttribute.Sensitive {
		t.Fatal("token schema must be sensitive")
	}
	if !tokenAttribute.Optional {
		t.Fatal("token schema must be optional for environment-backed configuration")
	}
}

func TestResolveConfigurationUsesEnvironment(t *testing.T) {
	t.Parallel()

	environment := map[string]string{
		apiURLEnvironmentVariable: "https://pulse.example.com",
		tokenEnvironmentVariable:  "environment-token",
	}
	config, diagnostics := resolveConfiguration(
		pulseProviderModel{APIURL: types.StringNull(), Token: types.StringNull(), AllowInsecureHTTP: types.BoolNull()},
		func(name string) string { return environment[name] },
	)

	if diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diagnostics)
	}
	if got, want := config.BaseURL, "https://pulse.example.com"; got != want {
		t.Fatalf("base URL = %q, want %q", got, want)
	}
	if got, want := config.Token, "environment-token"; got != want {
		t.Fatalf("token = %q, want %q", got, want)
	}
}

func TestResolveConfigurationExplicitValuesOverrideEnvironment(t *testing.T) {
	t.Parallel()

	config, diagnostics := resolveConfiguration(
		pulseProviderModel{
			APIURL:            types.StringValue("https://configured.example.com"),
			Token:             types.StringValue("configured-token"),
			AllowInsecureHTTP: types.BoolValue(true),
		},
		func(string) string { return "environment-value" },
	)

	if diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diagnostics)
	}
	if got, want := config.BaseURL, "https://configured.example.com"; got != want {
		t.Fatalf("base URL = %q, want %q", got, want)
	}
	if got, want := config.Token, "configured-token"; got != want {
		t.Fatalf("token = %q, want %q", got, want)
	}
	if !config.AllowInsecureHTTP {
		t.Fatal("explicit allow_insecure_http was not passed to the client")
	}
}

func TestResolveConfigurationRejectsMissingValues(t *testing.T) {
	t.Parallel()

	_, diagnostics := resolveConfiguration(
		pulseProviderModel{APIURL: types.StringNull(), Token: types.StringNull(), AllowInsecureHTTP: types.BoolNull()},
		func(string) string { return "" },
	)

	if got, want := diagnostics.ErrorsCount(), 2; got != want {
		t.Fatalf("error count = %d, want %d: %v", got, want, diagnostics)
	}
}

func TestResolveConfigurationRejectsUnknownValues(t *testing.T) {
	t.Parallel()

	_, diagnostics := resolveConfiguration(
		pulseProviderModel{APIURL: types.StringUnknown(), Token: types.StringUnknown(), AllowInsecureHTTP: types.BoolNull()},
		func(string) string { return "environment-value" },
	)

	if got, want := diagnostics.ErrorsCount(), 2; got != want {
		t.Fatalf("error count = %d, want %d: %v", got, want, diagnostics)
	}
}

func TestConfigureCreatesSharedClient(t *testing.T) {
	t.Parallel()

	var captured client.Config
	implementation := &PulseProvider{
		version: "1.2.3",
		getenv:  func(string) string { return "" },
		newClient: func(config client.Config) (client.API, error) {
			captured = config
			return client.New(config)
		},
	}

	var schemaResponse provider.SchemaResponse
	implementation.Schema(context.Background(), provider.SchemaRequest{}, &schemaResponse)

	request := provider.ConfigureRequest{
		Config: tfsdk.Config{
			Raw: tftypes.NewValue(
				tftypes.Object{
					AttributeTypes: map[string]tftypes.Type{
						"allow_insecure_http": tftypes.Bool,
						"api_url":             tftypes.String,
						"token":               tftypes.String,
					},
				},
				map[string]tftypes.Value{
					"allow_insecure_http": tftypes.NewValue(tftypes.Bool, false),
					"api_url":             tftypes.NewValue(tftypes.String, "https://pulse.example.com"),
					"token":               tftypes.NewValue(tftypes.String, "automation-token"),
				},
			),
			Schema: schemaResponse.Schema,
		},
	}
	var response provider.ConfigureResponse
	implementation.Configure(context.Background(), request, &response)

	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", response.Diagnostics)
	}
	if response.ResourceData == nil || response.DataSourceData == nil {
		t.Fatal("configured client must be available to resources and data sources")
	}
	if response.ResourceData != response.DataSourceData {
		t.Fatal("resources and data sources must share one configured client")
	}
	if got, want := captured.BaseURL, "https://pulse.example.com"; got != want {
		t.Fatalf("base URL = %q, want %q", got, want)
	}
	if got, want := captured.Token, "automation-token"; got != want {
		t.Fatalf("token = %q, want %q", got, want)
	}
	if got, want := captured.UserAgent, "terraform-provider-pulse/1.2.3"; got != want {
		t.Fatalf("user agent = %q, want %q", got, want)
	}
}
