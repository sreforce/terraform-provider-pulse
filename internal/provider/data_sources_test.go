package provider

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	datasourceschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/sreforce/terraform-provider-pulse/internal/client"
)

type fakeDataSourceClient struct {
	organization       client.Organization
	organizationError  error
	component          client.Component
	componentError     error
	componentID        string
	componentTypePages map[string]client.Page[client.ComponentType]
	teamPages          map[string]client.Page[client.Team]
	tagPages           map[string]client.Page[client.Tag]
}

func (f *fakeDataSourceClient) CurrentOrganization(context.Context) (client.Organization, error) {
	return f.organization, f.organizationError
}

func (f *fakeDataSourceClient) GetComponent(_ context.Context, id string) (client.Component, error) {
	f.componentID = id
	return f.component, f.componentError
}

func (f *fakeDataSourceClient) ListComponentTypes(
	_ context.Context,
	options client.ListOptions,
) (client.Page[client.ComponentType], error) {
	return f.componentTypePages[options.Cursor], nil
}

func (f *fakeDataSourceClient) ListTeams(
	_ context.Context,
	options client.ListOptions,
) (client.Page[client.Team], error) {
	return f.teamPages[options.Cursor], nil
}

func (f *fakeDataSourceClient) ListTags(
	_ context.Context,
	options client.ListOptions,
) (client.Page[client.Tag], error) {
	return f.tagPages[options.Cursor], nil
}

func TestDataSourceMetadata(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		dataSource datasource.DataSource
		wantType   string
	}{
		{name: "current organization", dataSource: NewCurrentOrganizationDataSource(), wantType: "pulse_current_organization"},
		{name: "component", dataSource: NewComponentDataSource(), wantType: "pulse_component"},
		{name: "component type", dataSource: NewComponentTypeDataSource(), wantType: "pulse_component_type"},
		{name: "team", dataSource: NewTeamDataSource(), wantType: "pulse_team"},
		{name: "tag", dataSource: NewTagDataSource(), wantType: "pulse_tag"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var response datasource.MetadataResponse
			test.dataSource.Metadata(
				context.Background(),
				datasource.MetadataRequest{ProviderTypeName: "pulse"},
				&response,
			)
			if got := response.TypeName; got != test.wantType {
				t.Fatalf("type name = %q, want %q", got, test.wantType)
			}
		})
	}
}

func TestComponentDataSourceRequiresUUIDAndDoesNotExposeNameSelector(t *testing.T) {
	t.Parallel()

	implementation := NewComponentDataSource()
	var response datasource.SchemaResponse
	implementation.Schema(context.Background(), datasource.SchemaRequest{}, &response)

	id, ok := response.Schema.Attributes["id"].(datasourceschema.StringAttribute)
	if !ok || !id.Required {
		t.Fatalf("id schema = %#v, want required string", response.Schema.Attributes["id"])
	}
	name, ok := response.Schema.Attributes["name"].(datasourceschema.StringAttribute)
	if !ok || !name.Computed || name.Optional || name.Required {
		t.Fatalf("name schema = %#v, want computed-only string", response.Schema.Attributes["name"])
	}
	externalKey, ok := response.Schema.Attributes["external_key"].(datasourceschema.StringAttribute)
	if !ok || !externalKey.Computed || externalKey.Optional || externalKey.Required {
		t.Fatalf("external_key schema = %#v, want computed-only string", response.Schema.Attributes["external_key"])
	}
}

func TestCurrentOrganizationDataSourceReadsCredentialOrganization(t *testing.T) {
	t.Parallel()

	configuredClient := &fakeDataSourceClient{
		organization: client.Organization{ID: "org-1", Name: "Chainway", Slug: "chainway"},
	}
	implementation := &currentOrganizationDataSource{client: configuredClient}
	request, response := dataSourceReadRequest(t, implementation, nil)
	implementation.Read(context.Background(), request, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", response.Diagnostics)
	}

	var state currentOrganizationDataSourceModel
	response.Diagnostics.Append(response.State.Get(context.Background(), &state)...)
	if response.Diagnostics.HasError() {
		t.Fatalf("unable to read state: %v", response.Diagnostics)
	}
	if got, want := state.ID.ValueString(), "org-1"; got != want {
		t.Fatalf("id = %q, want %q", got, want)
	}
	if got, want := state.Name.ValueString(), "Chainway"; got != want {
		t.Fatalf("name = %q, want %q", got, want)
	}
	if got, want := state.Slug.ValueString(), "chainway"; got != want {
		t.Fatalf("slug = %q, want %q", got, want)
	}
}

func TestComponentDataSourceReadsByUUIDAndPreservesComputedIdentity(t *testing.T) {
	t.Parallel()

	ownerTeamID := "team-1"
	stateReason := "awaiting first signal"
	configuredClient := &fakeDataSourceClient{
		component: client.Component{
			ID:              "component-1",
			ExternalKey:     "main-net/core/sequencer",
			Kind:            "external",
			Name:            "Sequencer",
			ComponentTypeID: "type-1",
			OwnerTeamID:     &ownerTeamID,
			RelevanceTagIDs: []string{"tag-relevance"},
			FilterTagIDs:    []string{"tag-filter"},
			AlertEnabled:    false,
			State:           "unknown",
			StateReason:     &stateReason,
			Revision:        7,
		},
	}
	implementation := &componentDataSource{client: configuredClient}
	request, response := dataSourceReadRequest(t, implementation, map[string]string{"id": "component-1"})
	implementation.Read(context.Background(), request, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", response.Diagnostics)
	}
	if got, want := configuredClient.componentID, "component-1"; got != want {
		t.Fatalf("requested component id = %q, want %q", got, want)
	}

	var state componentDataSourceModel
	response.Diagnostics.Append(response.State.Get(context.Background(), &state)...)
	if response.Diagnostics.HasError() {
		t.Fatalf("unable to read state: %v", response.Diagnostics)
	}
	if got, want := state.ExternalKey.ValueString(), "main-net/core/sequencer"; got != want {
		t.Fatalf("external key = %q, want %q", got, want)
	}
	if got, want := state.Name.ValueString(), "Sequencer"; got != want {
		t.Fatalf("name = %q, want %q", got, want)
	}
	if got, want := state.ConfigurationRevision.ValueInt64(), int64(7); got != want {
		t.Fatalf("revision = %d, want %d", got, want)
	}
	if got, want := len(state.RelevanceTagIDs.Elements()), 1; got != want {
		t.Fatalf("relevance tag count = %d, want %d", got, want)
	}
}

func TestTagDataSourceUsesPurposeAndNameTogether(t *testing.T) {
	t.Parallel()

	displayLabel := "Network"
	configuredClient := &fakeDataSourceClient{
		tagPages: map[string]client.Page[client.Tag]{
			"": {Items: []client.Tag{
				{ID: "tag-filter", Name: "network", Purpose: "filter", DisplayLabel: &displayLabel},
				{ID: "tag-relevance", Name: "network", Purpose: "relevance"},
			}},
		},
	}
	implementation := &tagDataSource{client: configuredClient}
	request, response := dataSourceReadRequest(t, implementation, map[string]string{
		"name":    "network",
		"purpose": "filter",
	})
	implementation.Read(context.Background(), request, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", response.Diagnostics)
	}

	var state tagDataSourceModel
	response.Diagnostics.Append(response.State.Get(context.Background(), &state)...)
	if response.Diagnostics.HasError() {
		t.Fatalf("unable to read state: %v", response.Diagnostics)
	}
	if got, want := state.ID.ValueString(), "tag-filter"; got != want {
		t.Fatalf("id = %q, want %q", got, want)
	}
	if got, want := state.DisplayLabel.ValueString(), "Network"; got != want {
		t.Fatalf("display label = %q, want %q", got, want)
	}
}

func dataSourceReadRequest(
	t *testing.T,
	implementation datasource.DataSource,
	configuredStrings map[string]string,
) (datasource.ReadRequest, datasource.ReadResponse) {
	t.Helper()

	ctx := context.Background()
	var schemaResponse datasource.SchemaResponse
	implementation.Schema(ctx, datasource.SchemaRequest{}, &schemaResponse)
	terraformType := schemaResponse.Schema.Type().TerraformType(ctx)
	objectType, ok := terraformType.(tftypes.Object)
	if !ok {
		t.Fatalf("terraform type = %T, want tftypes.Object", terraformType)
	}

	values := make(map[string]tftypes.Value, len(objectType.AttributeTypes))
	for name, attributeType := range objectType.AttributeTypes {
		if configured, exists := configuredStrings[name]; exists {
			values[name] = tftypes.NewValue(attributeType, configured)
			continue
		}
		values[name] = tftypes.NewValue(attributeType, nil)
	}

	return datasource.ReadRequest{
			Config: tfsdk.Config{
				Raw:    tftypes.NewValue(terraformType, values),
				Schema: schemaResponse.Schema,
			},
		}, datasource.ReadResponse{
			State: tfsdk.State{Schema: schemaResponse.Schema},
		}
}
