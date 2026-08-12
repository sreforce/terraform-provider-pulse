package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	providerschema "github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/sreforce/terraform-provider-pulse/internal/client"
	"github.com/sreforce/terraform-provider-pulse/internal/client/clienttest"
)

const providerProtocolToken = "provider-protocol-automation-token"

func TestTFProtocol6ProviderSchemaAndConfigure(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mock := clienttest.NewServer(t, providerProtocolToken)
	implementation := &PulseProvider{
		version: "0.1.0-test",
		getenv:  func(string) string { return "" },
		newClient: func(config client.Config) (client.API, error) {
			config.HTTPClient = mock.HTTPClient()
			return client.New(config)
		},
	}
	server := providerserver.NewProtocol6(implementation)()

	schemaResponse, err := server.GetProviderSchema(ctx, &tfprotov6.GetProviderSchemaRequest{})
	if err != nil {
		t.Fatalf("protocol 6 GetProviderSchema: %v", err)
	}
	assertNoProtocolErrors(t, schemaResponse.Diagnostics)
	if schemaResponse.Provider == nil {
		t.Fatal("protocol 6 returned no provider schema")
	}
	resourceTypes := sortedMapKeys(schemaResponse.ResourceSchemas)
	assertStringsEqual(t, resourceTypes, []string{
		"pulse_component",
		"pulse_component_integration",
		"pulse_component_rollup",
		"pulse_component_type",
		"pulse_tag",
		"pulse_team",
	})
	dataSourceTypes := sortedMapKeys(schemaResponse.DataSourceSchemas)
	assertStringsEqual(t, dataSourceTypes, []string{
		"pulse_component",
		"pulse_component_type",
		"pulse_current_organization",
		"pulse_tag",
		"pulse_team",
	})

	providerType := schemaResponse.Provider.ValueType()
	configValue := tftypes.NewValue(providerType, map[string]tftypes.Value{
		"allow_insecure_http": tftypes.NewValue(tftypes.Bool, true),
		"api_url":             tftypes.NewValue(tftypes.String, mock.URL()),
		"token":               tftypes.NewValue(tftypes.String, providerProtocolToken),
	})
	dynamicConfig, err := tfprotov6.NewDynamicValue(providerType, configValue)
	if err != nil {
		t.Fatalf("encode protocol 6 provider configuration: %v", err)
	}
	validateResponse, err := server.ValidateProviderConfig(ctx, &tfprotov6.ValidateProviderConfigRequest{Config: &dynamicConfig})
	if err != nil {
		t.Fatalf("protocol 6 ValidateProviderConfig: %v", err)
	}
	assertNoProtocolErrors(t, validateResponse.Diagnostics)
	configureResponse, err := server.ConfigureProvider(ctx, &tfprotov6.ConfigureProviderRequest{
		TerraformVersion: "1.11.0-test",
		Config:           &dynamicConfig,
	})
	if err != nil {
		t.Fatalf("protocol 6 ConfigureProvider: %v", err)
	}
	assertNoProtocolErrors(t, configureResponse.Diagnostics)
}

func TestProviderProtocolConfigureAndSchemaSurface(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	mock := clienttest.NewServer(t, providerProtocolToken)
	implementation := &PulseProvider{
		version: "0.1.0-test",
		getenv:  func(string) string { return "" },
		newClient: func(config client.Config) (client.API, error) {
			config.HTTPClient = mock.HTTPClient()
			return client.New(config)
		},
	}

	var schemaResponse provider.SchemaResponse
	implementation.Schema(ctx, provider.SchemaRequest{}, &schemaResponse)
	if diagnostics := schemaResponse.Schema.ValidateImplementation(ctx); diagnostics.HasError() {
		t.Fatalf("provider schema diagnostics: %v", diagnostics)
	}

	request := provider.ConfigureRequest{Config: providerProtocolConfig(t, schemaResponse.Schema, mock.URL())}
	var response provider.ConfigureResponse
	implementation.Configure(ctx, request, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("provider configure diagnostics: %v", response.Diagnostics)
	}
	if response.ResourceData == nil || response.ResourceData != response.DataSourceData {
		t.Fatal("provider must share one configured automation client with resources and data sources")
	}

	var resourceTypes []string
	for _, factory := range implementation.Resources(ctx) {
		providerResource := factory()
		var metadata resource.MetadataResponse
		providerResource.Metadata(ctx, resource.MetadataRequest{ProviderTypeName: "pulse"}, &metadata)
		resourceTypes = append(resourceTypes, metadata.TypeName)

		var resourceSchema resource.SchemaResponse
		providerResource.Schema(ctx, resource.SchemaRequest{}, &resourceSchema)
		if diagnostics := resourceSchema.Schema.ValidateImplementation(ctx); diagnostics.HasError() {
			t.Fatalf("%s schema diagnostics: %v", metadata.TypeName, diagnostics)
		}
		configurable, ok := providerResource.(resource.ResourceWithConfigure)
		if !ok {
			t.Fatalf("%s does not accept provider configuration", metadata.TypeName)
		}
		var configureResponse resource.ConfigureResponse
		configurable.Configure(ctx, resource.ConfigureRequest{ProviderData: response.ResourceData}, &configureResponse)
		if configureResponse.Diagnostics.HasError() {
			t.Fatalf("%s configure diagnostics: %v", metadata.TypeName, configureResponse.Diagnostics)
		}
	}
	sort.Strings(resourceTypes)
	assertStringsEqual(t, resourceTypes, []string{
		"pulse_component",
		"pulse_component_integration",
		"pulse_component_rollup",
		"pulse_component_type",
		"pulse_tag",
		"pulse_team",
	})

	var dataSourceTypes []string
	for _, factory := range implementation.DataSources(ctx) {
		providerDataSource := factory()
		var metadata datasource.MetadataResponse
		providerDataSource.Metadata(ctx, datasource.MetadataRequest{ProviderTypeName: "pulse"}, &metadata)
		dataSourceTypes = append(dataSourceTypes, metadata.TypeName)

		var dataSourceSchema datasource.SchemaResponse
		providerDataSource.Schema(ctx, datasource.SchemaRequest{}, &dataSourceSchema)
		if diagnostics := dataSourceSchema.Schema.ValidateImplementation(ctx); diagnostics.HasError() {
			t.Fatalf("%s schema diagnostics: %v", metadata.TypeName, diagnostics)
		}
		configurable, ok := providerDataSource.(datasource.DataSourceWithConfigure)
		if !ok {
			t.Fatalf("%s does not accept provider configuration", metadata.TypeName)
		}
		var configureResponse datasource.ConfigureResponse
		configurable.Configure(ctx, datasource.ConfigureRequest{ProviderData: response.DataSourceData}, &configureResponse)
		if configureResponse.Diagnostics.HasError() {
			t.Fatalf("%s configure diagnostics: %v", metadata.TypeName, configureResponse.Diagnostics)
		}
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

func TestRollupProtocolConfiguredEmptyReplaceDeleteAndImport(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := "/api/automation/v1/components/" + testRollupParentID + "/rollup"
	empty := client.ComponentRollup{ParentComponentID: testRollupParentID, Rules: []client.RollupRule{}, Revision: 1}
	rules := []client.RollupRule{{
		ChildComponentIDs: []string{testRollupChildAID, testRollupChildBID},
		WhenChildYellow:   client.RollupEffectYellow,
		WhenChildRed:      client.RollupEffectRed,
	}}
	replaced := client.ComponentRollup{ParentComponentID: testRollupParentID, Rules: rules, Revision: 2}
	imported := client.ComponentRollup{ParentComponentID: testRollupParentID, Rules: rules, Revision: 7}

	api := newProviderProtocolClient(t,
		clienttest.Expectation{
			Method:                http.MethodPut,
			RequestURI:            path,
			RequireIdempotencyKey: true,
			IfNoneMatch:           "*",
			RequestBody:           providerProtocolRequestBody(t, client.ComponentRollupReplaceRequest{Rules: []client.RollupRule{}}),
			StatusCode:            http.StatusCreated,
			ResponseBody:          providerProtocolJSON(t, empty),
		},
		clienttest.Expectation{
			Method:       http.MethodGet,
			RequestURI:   path,
			ResponseBody: providerProtocolJSON(t, empty),
		},
		clienttest.Expectation{
			Method:                http.MethodPut,
			RequestURI:            path,
			RequireIdempotencyKey: true,
			IfMatch:               `"1"`,
			RequestBody:           providerProtocolRequestBody(t, client.ComponentRollupReplaceRequest{Rules: rules}),
			ResponseBody:          providerProtocolJSON(t, replaced),
		},
		clienttest.Expectation{
			Method:                http.MethodDelete,
			RequestURI:            path,
			RequireIdempotencyKey: true,
			IfMatch:               `"2"`,
			StatusCode:            http.StatusNoContent,
		},
		clienttest.Expectation{
			Method:       http.MethodGet,
			RequestURI:   path,
			ResponseBody: providerProtocolJSON(t, imported),
		},
	)
	implementation := &componentRollupResource{client: api}
	schemaValue := componentRollupTestSchema(t)

	createModel := componentRollupResourceModel{
		ParentComponentID: types.StringValue(testRollupParentID),
		Rules:             mustComponentRollupRulesValue(t, nil),
		Revision:          types.Int64Unknown(),
	}
	createPlan := tfsdk.Plan{Schema: schemaValue}
	assertNoDiagnostics(t, createPlan.Set(ctx, &createModel))
	createResponse := resource.CreateResponse{State: tfsdk.State{Schema: schemaValue}}
	implementation.Create(ctx, resource.CreateRequest{Plan: createPlan}, &createResponse)
	assertNoDiagnostics(t, createResponse.Diagnostics)

	readResponse := resource.ReadResponse{State: createResponse.State}
	implementation.Read(ctx, resource.ReadRequest{State: createResponse.State}, &readResponse)
	assertNoDiagnostics(t, readResponse.Diagnostics)
	var emptyState componentRollupResourceModel
	assertNoDiagnostics(t, readResponse.State.Get(ctx, &emptyState))
	if emptyState.Rules.IsNull() || emptyState.Rules.IsUnknown() || len(emptyState.Rules.Elements()) != 0 {
		t.Fatalf("configured empty ruleset was not preserved: %#v", emptyState.Rules)
	}

	updateModel := mustComponentRollupModel(t, rules, 2)
	updateModel.Revision = types.Int64Unknown()
	updatePlan := tfsdk.Plan{Schema: schemaValue}
	assertNoDiagnostics(t, updatePlan.Set(ctx, &updateModel))
	updateResponse := resource.UpdateResponse{State: readResponse.State}
	implementation.Update(ctx, resource.UpdateRequest{Plan: updatePlan, State: readResponse.State}, &updateResponse)
	assertNoDiagnostics(t, updateResponse.Diagnostics)

	var replacedState componentRollupResourceModel
	assertNoDiagnostics(t, updateResponse.State.Get(ctx, &replacedState))
	if got, want := replacedState.Revision.ValueInt64(), int64(2); got != want {
		t.Fatalf("replaced rollup revision = %d, want %d", got, want)
	}
	deleteResponse := resource.DeleteResponse{State: updateResponse.State}
	implementation.Delete(ctx, resource.DeleteRequest{State: updateResponse.State}, &deleteResponse)
	assertNoDiagnostics(t, deleteResponse.Diagnostics)

	importState := tfsdk.State{Schema: schemaValue, Raw: tftypes.NewValue(schemaValue.Type().TerraformType(ctx), nil)}
	importResponse := resource.ImportStateResponse{State: importState}
	implementation.ImportState(ctx, resource.ImportStateRequest{ID: testRollupParentID}, &importResponse)
	assertNoDiagnostics(t, importResponse.Diagnostics)
	importRead := resource.ReadResponse{State: importResponse.State}
	implementation.Read(ctx, resource.ReadRequest{State: importResponse.State}, &importRead)
	assertNoDiagnostics(t, importRead.Diagnostics)
	var importedState componentRollupResourceModel
	assertNoDiagnostics(t, importRead.State.Get(ctx, &importedState))
	if got, want := importedState.Revision.ValueInt64(), int64(7); got != want {
		t.Fatalf("imported rollup revision = %d, want %d", got, want)
	}
}

func TestIntegrationProtocolCreateReadRotateAndArchive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := "/api/automation/v1/components/" + integrationTestComponentID + "/integrations/grafana"
	versionOne := providerProtocolIntegration(integrationTestVersion1, 1, client.IntegrationLifecycleOwnerAutomation)
	versionTwo := providerProtocolIntegration(integrationTestVersion2, 2, client.IntegrationLifecycleOwnerAutomation)

	api := newProviderProtocolClient(t,
		clienttest.Expectation{
			Method:                http.MethodPut,
			RequestURI:            path,
			RequireIdempotencyKey: true,
			RequestBody:           providerProtocolRequestBody(t, client.ComponentIntegrationUpsertRequest{}),
			StatusCode:            http.StatusCreated,
			ResponseBody:          providerProtocolJSON(t, providerProtocolIntegrationMutation(versionOne, "created-secret")),
		},
		clienttest.Expectation{
			Method:       http.MethodGet,
			RequestURI:   path,
			ResponseBody: providerProtocolJSON(t, versionOne),
		},
		clienttest.Expectation{
			Method:                http.MethodPost,
			RequestURI:            path + "/rotate",
			RequireIdempotencyKey: true,
			IfMatch:               `"1"`,
			RequestBody:           []byte("{}\n"),
			ResponseBody:          providerProtocolJSON(t, providerProtocolIntegrationMutation(versionTwo, "rotated-secret")),
		},
		clienttest.Expectation{
			Method:                http.MethodDelete,
			RequestURI:            path,
			RequireIdempotencyKey: true,
			IfMatch:               `"2"`,
			StatusCode:            http.StatusNoContent,
		},
	)
	implementation := &componentIntegrationResource{api: api}
	schemaValue := integrationTestSchema(t, implementation)
	createPlan := integrationTestPlan(t, schemaValue, integrationTestModel("grafana"))
	createResponse := resource.CreateResponse{State: tfsdk.State{Schema: schemaValue}}
	implementation.Create(ctx, resource.CreateRequest{Plan: createPlan}, &createResponse)
	assertIntegrationNoDiagnostics(t, createResponse.Diagnostics)

	readResponse := resource.ReadResponse{State: createResponse.State}
	implementation.Read(ctx, resource.ReadRequest{State: createResponse.State}, &readResponse)
	assertIntegrationNoDiagnostics(t, readResponse.Diagnostics)
	var current componentIntegrationResourceModel
	assertIntegrationNoDiagnostics(t, readResponse.State.Get(ctx, &current))
	if got, want := current.Secret.ValueString(), "created-secret"; got != want {
		t.Fatalf("preserved secret = %q, want %q", got, want)
	}

	planned := current
	planned.RotationTrigger = types.StringValue("rotation-2")
	planned.Secret = types.StringUnknown()
	planned.Version = types.StringUnknown()
	planned.RotationRequired = types.BoolUnknown()
	planned.Revision = types.Int64Unknown()
	updateResponse := resource.UpdateResponse{State: readResponse.State}
	implementation.Update(ctx, resource.UpdateRequest{
		State: readResponse.State,
		Plan:  integrationTestPlan(t, schemaValue, planned),
	}, &updateResponse)
	assertIntegrationNoDiagnostics(t, updateResponse.Diagnostics)
	var rotated componentIntegrationResourceModel
	assertIntegrationNoDiagnostics(t, updateResponse.State.Get(ctx, &rotated))
	if got, want := rotated.Secret.ValueString(), "rotated-secret"; got != want {
		t.Fatalf("rotated secret = %q, want %q", got, want)
	}

	deleteResponse := resource.DeleteResponse{State: updateResponse.State}
	implementation.Delete(ctx, resource.DeleteRequest{State: updateResponse.State}, &deleteResponse)
	assertIntegrationNoDiagnostics(t, deleteResponse.Diagnostics)
}

func TestIntegrationProtocolImportAdoptAndArchive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := "/api/automation/v1/components/" + integrationTestComponentID + "/integrations/grafana"
	human := providerProtocolIntegration(integrationTestVersion1, 5, client.IntegrationLifecycleOwnerHuman)
	automation := providerProtocolIntegration(integrationTestVersion2, 6, client.IntegrationLifecycleOwnerAutomation)

	api := newProviderProtocolClient(t,
		clienttest.Expectation{
			Method:       http.MethodGet,
			RequestURI:   path,
			ResponseBody: providerProtocolJSON(t, human),
		},
		clienttest.Expectation{
			Method:                http.MethodPut,
			RequestURI:            path,
			RequireIdempotencyKey: true,
			IfMatch:               `"5"`,
			RequestBody:           []byte("{\"adopt\":true}\n"),
			ResponseBody:          providerProtocolJSON(t, providerProtocolIntegrationMutation(automation, "adopted-secret")),
		},
		clienttest.Expectation{
			Method:                http.MethodDelete,
			RequestURI:            path,
			RequireIdempotencyKey: true,
			IfMatch:               `"6"`,
			StatusCode:            http.StatusNoContent,
		},
	)
	implementation := &componentIntegrationResource{api: api}
	schemaValue := integrationTestSchema(t, implementation)
	importResponse := resource.ImportStateResponse{State: tfsdk.State{
		Schema: schemaValue,
		Raw:    tftypes.NewValue(schemaValue.Type().TerraformType(ctx), nil),
	}}
	implementation.ImportState(ctx, resource.ImportStateRequest{ID: integrationTestComponentID + "/grafana"}, &importResponse)
	assertIntegrationNoDiagnostics(t, importResponse.Diagnostics)

	readResponse := resource.ReadResponse{State: importResponse.State}
	implementation.Read(ctx, resource.ReadRequest{State: importResponse.State}, &readResponse)
	if readResponse.Diagnostics.HasError() || readResponse.Diagnostics.WarningsCount() != 1 {
		t.Fatalf("import read diagnostics = %v, want one rotation warning", readResponse.Diagnostics)
	}
	var imported componentIntegrationResourceModel
	assertIntegrationNoDiagnostics(t, readResponse.State.Get(ctx, &imported))
	if !imported.Secret.IsNull() || !imported.RotationRequired.ValueBool() || imported.LifecycleOwner.ValueString() != "human" {
		t.Fatalf("imported integration state = %#v", imported)
	}

	planned := imported
	planned.RotationTrigger = types.StringValue("adopt-1")
	planned.Adopt = types.BoolValue(true)
	planned.Secret = types.StringUnknown()
	planned.Version = types.StringUnknown()
	planned.RotationRequired = types.BoolUnknown()
	planned.LifecycleOwner = types.StringUnknown()
	planned.Revision = types.Int64Unknown()
	updateResponse := resource.UpdateResponse{State: readResponse.State}
	implementation.Update(ctx, resource.UpdateRequest{
		State: readResponse.State,
		Plan:  integrationTestPlan(t, schemaValue, planned),
	}, &updateResponse)
	assertIntegrationNoDiagnostics(t, updateResponse.Diagnostics)

	var adopted componentIntegrationResourceModel
	assertIntegrationNoDiagnostics(t, updateResponse.State.Get(ctx, &adopted))
	if adopted.LifecycleOwner.ValueString() != "automation" || adopted.Secret.ValueString() != "adopted-secret" {
		t.Fatalf("adopted integration state = %#v", adopted)
	}
	deleteResponse := resource.DeleteResponse{State: updateResponse.State}
	implementation.Delete(ctx, resource.DeleteRequest{State: updateResponse.State}, &deleteResponse)
	assertIntegrationNoDiagnostics(t, deleteResponse.Diagnostics)
}

func TestIntegrationProtocolLostSecretRecoveryAndVersionDrift(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := "/api/automation/v1/components/" + integrationTestComponentID + "/integrations/grafana"
	recovered := providerProtocolIntegration(integrationTestVersion2, 2, client.IntegrationLifecycleOwnerAutomation)
	driftedVersion := "55555555-5555-4555-8555-555555555555"
	drifted := providerProtocolIntegration(driftedVersion, 3, client.IntegrationLifecycleOwnerAutomation)

	api := newProviderProtocolClient(t,
		clienttest.Expectation{
			Method:                http.MethodPut,
			RequestURI:            path,
			RequireIdempotencyKey: true,
			RequestBody:           providerProtocolRequestBody(t, client.ComponentIntegrationUpsertRequest{}),
			StatusCode:            http.StatusConflict,
			ResponseBody:          clienttest.Fixture(t, "secret-reissue-required.json"),
		},
		clienttest.Expectation{
			Method:                http.MethodPost,
			RequestURI:            path + "/rotate",
			RequireIdempotencyKey: true,
			IfMatch:               `"1"`,
			RequestBody:           []byte("{\"revoke_predecessor_immediately\":true}\n"),
			ResponseBody:          providerProtocolJSON(t, providerProtocolIntegrationMutation(recovered, "recovered-secret")),
		},
		clienttest.Expectation{
			Method:       http.MethodGet,
			RequestURI:   path,
			ResponseBody: providerProtocolJSON(t, drifted),
		},
	)
	implementation := &componentIntegrationResource{api: api}
	schemaValue := integrationTestSchema(t, implementation)
	createResponse := resource.CreateResponse{State: tfsdk.State{Schema: schemaValue}}
	implementation.Create(ctx, resource.CreateRequest{
		Plan: integrationTestPlan(t, schemaValue, integrationTestModel("grafana")),
	}, &createResponse)
	assertIntegrationNoDiagnostics(t, createResponse.Diagnostics)

	readResponse := resource.ReadResponse{State: createResponse.State}
	implementation.Read(ctx, resource.ReadRequest{State: createResponse.State}, &readResponse)
	if readResponse.Diagnostics.HasError() || readResponse.Diagnostics.WarningsCount() != 1 {
		t.Fatalf("drift read diagnostics = %v, want one warning", readResponse.Diagnostics)
	}
	var driftedState componentIntegrationResourceModel
	assertIntegrationNoDiagnostics(t, readResponse.State.Get(ctx, &driftedState))
	if !driftedState.Secret.IsNull() || !driftedState.RotationRequired.ValueBool() {
		t.Fatalf("drifted secret state = %#v", driftedState)
	}
	if got, want := driftedState.Version.ValueString(), driftedVersion; got != want {
		t.Fatalf("drifted observed version = %q, want %q", got, want)
	}
}

func TestDataSourceProtocolWireContract(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	componentID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	component := client.Component{
		ID:              componentID,
		ExternalKey:     "production/platform/example-service",
		Name:            "Example service",
		ComponentTypeID: "type-service",
		RelevanceTagIDs: []string{},
		FilterTagIDs:    []string{},
		AlertEnabled:    false,
		State:           client.ComponentStateUnknown,
		Revision:        4,
	}
	api := newProviderProtocolClient(t,
		clienttest.Expectation{
			Method:       http.MethodGet,
			RequestURI:   "/api/automation/v1/organization",
			ResponseBody: providerProtocolJSON(t, client.Organization{ID: "org-example", Name: "Example Organization", Slug: "example-org"}),
		},
		clienttest.Expectation{
			Method:       http.MethodGet,
			RequestURI:   "/api/automation/v1/components/" + componentID,
			ResponseBody: providerProtocolJSON(t, component),
		},
		clienttest.Expectation{
			Method:     http.MethodGet,
			RequestURI: "/api/automation/v1/component-types?limit=200",
			ResponseBody: providerProtocolJSON(t, client.Page[client.ComponentType]{
				Items: []client.ComponentType{{
					ID: "type-service", Name: "Service", GreenLabel: "Operational", YellowLabel: "Degraded", RedLabel: "Major outage", UnknownLabel: "Unknown", Revision: 1,
				}},
				NextCursor: "",
			}),
		},
		clienttest.Expectation{
			Method:     http.MethodGet,
			RequestURI: "/api/automation/v1/teams?limit=200",
			ResponseBody: providerProtocolJSON(t, client.Page[client.Team]{
				Items:      []client.Team{{ID: "team-platform", Name: "Platform", Revision: 1}},
				NextCursor: "",
			}),
		},
		clienttest.Expectation{
			Method:     http.MethodGet,
			RequestURI: "/api/automation/v1/tags?limit=200",
			ResponseBody: providerProtocolJSON(t, client.Page[client.Tag]{
				Items:      []client.Tag{{ID: "tag-network", Name: "network", Purpose: "filter", DisplayOrder: 0, Revision: 1}},
				NextCursor: "",
			}),
		},
	)

	organizationSource := &currentOrganizationDataSource{}
	configureProviderDataSource(t, organizationSource, api)
	organizationRequest, organizationResponse := dataSourceReadRequest(t, organizationSource, nil)
	organizationSource.Read(ctx, organizationRequest, &organizationResponse)
	assertIntegrationNoDiagnostics(t, organizationResponse.Diagnostics)
	var organizationState currentOrganizationDataSourceModel
	assertIntegrationNoDiagnostics(t, organizationResponse.State.Get(ctx, &organizationState))
	if organizationState.Slug.ValueString() != "example-org" {
		t.Fatalf("organization slug = %q", organizationState.Slug.ValueString())
	}

	componentSource := &componentDataSource{}
	configureProviderDataSource(t, componentSource, api)
	componentRequest, componentResponse := dataSourceReadRequest(t, componentSource, map[string]string{"id": componentID})
	componentSource.Read(ctx, componentRequest, &componentResponse)
	assertIntegrationNoDiagnostics(t, componentResponse.Diagnostics)
	var componentState componentDataSourceModel
	assertIntegrationNoDiagnostics(t, componentResponse.State.Get(ctx, &componentState))
	if componentState.ExternalKey.ValueString() != component.ExternalKey {
		t.Fatalf("component external key = %q", componentState.ExternalKey.ValueString())
	}

	componentTypeSource := &componentTypeDataSource{}
	configureProviderDataSource(t, componentTypeSource, api)
	typeRequest, typeResponse := dataSourceReadRequest(t, componentTypeSource, map[string]string{"name": "Service"})
	componentTypeSource.Read(ctx, typeRequest, &typeResponse)
	assertIntegrationNoDiagnostics(t, typeResponse.Diagnostics)
	var typeState componentTypeDataSourceModel
	assertIntegrationNoDiagnostics(t, typeResponse.State.Get(ctx, &typeState))
	if typeState.ID.ValueString() != "type-service" {
		t.Fatalf("component type id = %q", typeState.ID.ValueString())
	}

	teamSource := &teamDataSource{}
	configureProviderDataSource(t, teamSource, api)
	teamRequest, teamResponse := dataSourceReadRequest(t, teamSource, map[string]string{"name": "Platform"})
	teamSource.Read(ctx, teamRequest, &teamResponse)
	assertIntegrationNoDiagnostics(t, teamResponse.Diagnostics)
	var teamState teamDataSourceModel
	assertIntegrationNoDiagnostics(t, teamResponse.State.Get(ctx, &teamState))
	if teamState.ID.ValueString() != "team-platform" {
		t.Fatalf("team id = %q", teamState.ID.ValueString())
	}

	tagSource := &tagDataSource{}
	configureProviderDataSource(t, tagSource, api)
	tagRequest, tagResponse := dataSourceReadRequest(t, tagSource, map[string]string{"purpose": "filter", "name": "network"})
	tagSource.Read(ctx, tagRequest, &tagResponse)
	assertIntegrationNoDiagnostics(t, tagResponse.Diagnostics)
	var tagState tagDataSourceModel
	assertIntegrationNoDiagnostics(t, tagResponse.State.Get(ctx, &tagState))
	if tagState.ID.ValueString() != "tag-network" || tagState.DisplayOrder.ValueInt64() != 0 {
		t.Fatalf("tag state = %#v", tagState)
	}
}

func newProviderProtocolClient(t *testing.T, expectations ...clienttest.Expectation) *client.Client {
	t.Helper()
	mock := clienttest.NewServer(t, providerProtocolToken, expectations...)
	implementation, err := client.New(client.Config{
		BaseURL:           mock.URL(),
		Token:             providerProtocolToken,
		UserAgent:         "terraform-provider-pulse/protocol-test",
		HTTPClient:        mock.HTTPClient(),
		AllowInsecureHTTP: true,
	})
	if err != nil {
		t.Fatalf("create protocol client: %v", err)
	}
	return implementation
}

func providerProtocolIntegration(
	versionID string,
	revision int64,
	owner client.IntegrationLifecycleOwner,
) client.ComponentIntegration {
	return client.ComponentIntegration{
		ComponentID:         integrationTestComponentID,
		Provider:            client.IntegrationProviderGrafana,
		Endpoint:            "https://pulse.example.test/webhooks/components/" + integrationTestComponentID + "/grafana",
		LifecycleOwner:      owner,
		Status:              client.IntegrationStatusActive,
		CredentialVersionID: versionID,
		Revision:            revision,
	}
}

func providerProtocolIntegrationMutation(integration client.ComponentIntegration, secret string) client.ComponentIntegrationMutation {
	return client.ComponentIntegrationMutation{
		Integration: integration,
		Secret: &client.ComponentIntegrationSecret{
			Value:     secret,
			VersionID: integration.CredentialVersionID,
		},
	}
}

func assertNoProtocolErrors(t *testing.T, diagnostics []*tfprotov6.Diagnostic) {
	t.Helper()
	for _, diagnostic := range diagnostics {
		if diagnostic != nil && diagnostic.Severity == tfprotov6.DiagnosticSeverityError {
			t.Fatalf("protocol diagnostic: %s: %s", diagnostic.Summary, diagnostic.Detail)
		}
	}
}

func sortedMapKeys[T any](values map[string]T) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	sort.Strings(result)
	return result
}

func providerProtocolConfig(
	t *testing.T,
	schemaValue providerschema.Schema,
	baseURL string,
) tfsdk.Config {
	t.Helper()
	terraformType := schemaValue.Type().TerraformType(context.Background())
	return tfsdk.Config{
		Raw: tftypes.NewValue(terraformType, map[string]tftypes.Value{
			"allow_insecure_http": tftypes.NewValue(tftypes.Bool, true),
			"api_url":             tftypes.NewValue(tftypes.String, baseURL),
			"token":               tftypes.NewValue(tftypes.String, providerProtocolToken),
		}),
		Schema: schemaValue,
	}
}

func configureProviderDataSource(t *testing.T, implementation datasource.DataSource, providerData any) {
	t.Helper()
	configurable, ok := implementation.(datasource.DataSourceWithConfigure)
	if !ok {
		t.Fatalf("data source %T is not configurable", implementation)
	}
	var response datasource.ConfigureResponse
	configurable.Configure(context.Background(), datasource.ConfigureRequest{ProviderData: providerData}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("configure data source %T: %v", implementation, response.Diagnostics)
	}
}

func providerProtocolRequestBody(t *testing.T, value any) []byte {
	t.Helper()
	body := providerProtocolJSON(t, value)
	return append(body, '\n')
}

func providerProtocolJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal protocol fixture: %v", err)
	}
	return encoded
}
