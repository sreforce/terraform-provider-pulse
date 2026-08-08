package provider

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	resourceschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/sreforce/terraform-provider-pulse/internal/client"
)

const (
	testRollupParentID = "11111111-1111-4111-8111-111111111111"
	testRollupChildAID = "22222222-2222-4222-8222-222222222222"
	testRollupChildBID = "33333333-3333-4333-8333-333333333333"
	testRollupChildCID = "44444444-4444-4444-8444-444444444444"
)

type fakeComponentRollupClient struct {
	get     func(context.Context, string) (client.ComponentRollup, error)
	replace func(context.Context, string, client.ComponentRollupReplaceRequest, client.MutationOptions) (client.ComponentRollup, error)
	delete  func(context.Context, string, client.MutationOptions) error
}

func (f *fakeComponentRollupClient) GetComponentRollup(ctx context.Context, parentID string) (client.ComponentRollup, error) {
	if f.get == nil {
		return client.ComponentRollup{}, errors.New("unexpected GetComponentRollup call")
	}
	return f.get(ctx, parentID)
}

func (f *fakeComponentRollupClient) ReplaceComponentRollup(ctx context.Context, parentID string, request client.ComponentRollupReplaceRequest, options client.MutationOptions) (client.ComponentRollup, error) {
	if f.replace == nil {
		return client.ComponentRollup{}, errors.New("unexpected ReplaceComponentRollup call")
	}
	return f.replace(ctx, parentID, request, options)
}

func (f *fakeComponentRollupClient) DeleteComponentRollup(ctx context.Context, parentID string, options client.MutationOptions) error {
	if f.delete == nil {
		return errors.New("unexpected DeleteComponentRollup call")
	}
	return f.delete(ctx, parentID, options)
}

func TestComponentRollupResourceSchema(t *testing.T) {
	t.Parallel()

	implementation := NewComponentRollupResource()
	var metadata resource.MetadataResponse
	implementation.Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "pulse"}, &metadata)
	if got, want := metadata.TypeName, "pulse_component_rollup"; got != want {
		t.Fatalf("resource type = %q, want %q", got, want)
	}

	var response resource.SchemaResponse
	implementation.Schema(context.Background(), resource.SchemaRequest{}, &response)

	parent, ok := response.Schema.Attributes["parent_component_id"].(resourceschema.StringAttribute)
	if !ok || !parent.Required || len(parent.PlanModifiers) == 0 {
		t.Fatalf("parent_component_id schema = %#v, want required replacement identity", response.Schema.Attributes["parent_component_id"])
	}
	rules, ok := response.Schema.Attributes["rules"].(resourceschema.ListNestedAttribute)
	if !ok || !rules.Required {
		t.Fatalf("rules schema = %#v, want required ordered nested list", response.Schema.Attributes["rules"])
	}
	revision, ok := response.Schema.Attributes["revision"].(resourceschema.Int64Attribute)
	if !ok || !revision.Computed {
		t.Fatalf("revision schema = %#v, want computed optimistic-concurrency revision", response.Schema.Attributes["revision"])
	}
}

func TestValidateComponentRollupModelAcceptsEmptyCompleteRuleset(t *testing.T) {
	t.Parallel()

	model := componentRollupResourceModel{
		ParentComponentID: types.StringValue(testRollupParentID),
		Rules:             mustComponentRollupRulesValue(t, nil),
		Revision:          types.Int64Unknown(),
	}
	if diagnostics := validateComponentRollupModel(context.Background(), model); diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diagnostics)
	}
}

func TestValidateComponentRollupModelDefersEntirelyUnknownRule(t *testing.T) {
	t.Parallel()

	rules := types.ListValueMust(
		types.ObjectType{AttrTypes: componentRollupRuleAttributeTypes},
		[]attr.Value{types.ObjectUnknown(componentRollupRuleAttributeTypes)},
	)
	model := componentRollupResourceModel{
		ParentComponentID: types.StringValue(testRollupParentID),
		Rules:             rules,
		Revision:          types.Int64Unknown(),
	}
	if diagnostics := validateComponentRollupModel(context.Background(), model); diagnostics.HasError() {
		t.Fatalf("unknown nested rule must defer validation: %v", diagnostics)
	}
}

func TestValidateComponentRollupModelRejectsUnsafeRules(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		rules       []componentRollupRuleResourceModel
		wantSummary string
	}{
		"empty child set": {
			rules: []componentRollupRuleResourceModel{{
				ChildComponentIDs: mustStringSet(t, nil),
				WhenChildYellow:   types.StringValue("yellow"),
				WhenChildRed:      types.StringValue("red"),
			}},
			wantSummary: "Empty Pulse rollup rule",
		},
		"invalid effect": {
			rules: []componentRollupRuleResourceModel{{
				ChildComponentIDs: mustStringSet(t, []string{testRollupChildAID}),
				WhenChildYellow:   types.StringValue("critical"),
				WhenChildRed:      types.StringValue("red"),
			}},
			wantSummary: "Invalid Pulse rollup effect",
		},
		"self child": {
			rules: []componentRollupRuleResourceModel{{
				ChildComponentIDs: mustStringSet(t, []string{testRollupParentID}),
				WhenChildYellow:   types.StringValue("yellow"),
				WhenChildRed:      types.StringValue("red"),
			}},
			wantSummary: "Rollup cannot include itself",
		},
		"duplicate child across rules": {
			rules: []componentRollupRuleResourceModel{
				{
					ChildComponentIDs: mustStringSet(t, []string{testRollupChildAID}),
					WhenChildYellow:   types.StringValue("yellow"),
					WhenChildRed:      types.StringValue("red"),
				},
				{
					ChildComponentIDs: mustStringSet(t, []string{testRollupChildAID}),
					WhenChildYellow:   types.StringValue("none"),
					WhenChildRed:      types.StringValue("yellow"),
				},
			},
			wantSummary: "Duplicate Pulse rollup child",
		},
	}

	for name, testCase := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			model := componentRollupResourceModel{
				ParentComponentID: types.StringValue(testRollupParentID),
				Rules:             mustComponentRollupRulesValue(t, testCase.rules),
				Revision:          types.Int64Unknown(),
			}
			diagnostics := validateComponentRollupModel(context.Background(), model)
			if !diagnostics.HasError() {
				t.Fatalf("expected %q validation error", testCase.wantSummary)
			}
			found := false
			for _, diagnostic := range diagnostics {
				if diagnostic.Summary() == testCase.wantSummary {
					found = true
				}
			}
			if !found {
				t.Fatalf("diagnostics = %v, want summary %q", diagnostics, testCase.wantSummary)
			}
		})
	}
}

func TestComponentRollupRulesFromTerraformPreservesRuleOrderAndCanonicalizesChildren(t *testing.T) {
	t.Parallel()

	value := mustComponentRollupRulesValue(t, []componentRollupRuleResourceModel{
		{
			ChildComponentIDs: mustStringSet(t, []string{testRollupChildBID, testRollupChildAID}),
			WhenChildYellow:   types.StringValue("yellow"),
			WhenChildRed:      types.StringValue("red"),
		},
		{
			ChildComponentIDs: mustStringSet(t, []string{testRollupChildCID}),
			WhenChildYellow:   types.StringValue("none"),
			WhenChildRed:      types.StringValue("yellow"),
		},
	})

	rules, diagnostics := componentRollupRulesFromTerraform(context.Background(), value)
	if diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diagnostics)
	}
	want := []client.RollupRule{
		{
			ChildComponentIDs: []string{testRollupChildAID, testRollupChildBID},
			WhenChildYellow:   client.RollupEffect("yellow"),
			WhenChildRed:      client.RollupEffect("red"),
		},
		{
			ChildComponentIDs: []string{testRollupChildCID},
			WhenChildYellow:   client.RollupEffect("none"),
			WhenChildRed:      client.RollupEffect("yellow"),
		},
	}
	if !reflect.DeepEqual(rules, want) {
		t.Fatalf("rules = %#v, want %#v", rules, want)
	}
}

func TestComponentRollupCreateUsesCreatePreconditionAndCanonicalState(t *testing.T) {
	t.Parallel()

	rules := []client.RollupRule{{
		ChildComponentIDs: []string{testRollupChildBID, testRollupChildAID},
		WhenChildYellow:   client.RollupEffect("yellow"),
		WhenChildRed:      client.RollupEffect("red"),
	}}
	plan := mustComponentRollupModel(t, testRollupParentID, rules, 0)
	plan.Revision = types.Int64Unknown()
	schemaValue := componentRollupTestSchema(t)
	requestPlan := tfsdk.Plan{Schema: schemaValue}
	assertNoDiagnostics(t, requestPlan.Set(context.Background(), &plan))

	var captured client.MutationOptions
	implementation := &componentRollupResource{client: &fakeComponentRollupClient{
		replace: func(_ context.Context, parentID string, request client.ComponentRollupReplaceRequest, options client.MutationOptions) (client.ComponentRollup, error) {
			if parentID != testRollupParentID {
				t.Fatalf("parent ID = %q", parentID)
			}
			captured = options
			return client.ComponentRollup{ParentComponentID: parentID, Rules: request.Rules, Revision: 1}, nil
		},
	}}
	response := resource.CreateResponse{State: tfsdk.State{Schema: schemaValue, Raw: requestPlan.Raw}}
	implementation.Create(context.Background(), resource.CreateRequest{Plan: requestPlan}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", response.Diagnostics)
	}
	if captured.Revision != 0 {
		t.Fatalf("create revision = %d, want 0/If-None-Match semantics", captured.Revision)
	}

	var state componentRollupResourceModel
	assertNoDiagnostics(t, response.State.Get(context.Background(), &state))
	if got, want := state.Revision.ValueInt64(), int64(1); got != want {
		t.Fatalf("state revision = %d, want %d", got, want)
	}
	stateRules, diagnostics := componentRollupRulesFromTerraform(context.Background(), state.Rules)
	assertNoDiagnostics(t, diagnostics)
	wantStateRules := []client.RollupRule{{
		ChildComponentIDs: []string{testRollupChildAID, testRollupChildBID},
		WhenChildYellow:   client.RollupEffect("yellow"),
		WhenChildRed:      client.RollupEffect("red"),
	}}
	if !reflect.DeepEqual(stateRules, wantStateRules) {
		t.Fatalf("state rules = %#v, want %#v", stateRules, wantStateRules)
	}
}

func TestComponentRollupUpdateCarriesPriorRevision(t *testing.T) {
	t.Parallel()

	prior := mustComponentRollupModel(t, testRollupParentID, []client.RollupRule{{
		ChildComponentIDs: []string{testRollupChildAID},
		WhenChildYellow:   client.RollupEffect("yellow"),
		WhenChildRed:      client.RollupEffect("red"),
	}}, 7)
	planned := mustComponentRollupModel(t, testRollupParentID, []client.RollupRule{{
		ChildComponentIDs: []string{testRollupChildAID, testRollupChildBID},
		WhenChildYellow:   client.RollupEffect("yellow"),
		WhenChildRed:      client.RollupEffect("red"),
	}}, 7)

	schemaValue := componentRollupTestSchema(t)
	priorState := tfsdk.State{Schema: schemaValue}
	assertNoDiagnostics(t, priorState.Set(context.Background(), &prior))
	plan := tfsdk.Plan{Schema: schemaValue}
	assertNoDiagnostics(t, plan.Set(context.Background(), &planned))

	implementation := &componentRollupResource{client: &fakeComponentRollupClient{
		replace: func(_ context.Context, parentID string, request client.ComponentRollupReplaceRequest, options client.MutationOptions) (client.ComponentRollup, error) {
			if got, want := options.Revision, int64(7); got != want {
				t.Fatalf("update revision = %d, want %d", got, want)
			}
			return client.ComponentRollup{ParentComponentID: parentID, Rules: request.Rules, Revision: 8}, nil
		},
	}}
	response := resource.UpdateResponse{State: priorState}
	implementation.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: priorState}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", response.Diagnostics)
	}

	var state componentRollupResourceModel
	assertNoDiagnostics(t, response.State.Get(context.Background(), &state))
	if got, want := state.Revision.ValueInt64(), int64(8); got != want {
		t.Fatalf("state revision = %d, want %d", got, want)
	}
}

func TestComponentRollupCreateConflictNeverImplicitlyAdopts(t *testing.T) {
	t.Parallel()

	planModel := mustComponentRollupModel(t, testRollupParentID, nil, 0)
	schemaValue := componentRollupTestSchema(t)
	plan := tfsdk.Plan{Schema: schemaValue}
	assertNoDiagnostics(t, plan.Set(context.Background(), &planModel))

	readCalled := false
	implementation := &componentRollupResource{client: &fakeComponentRollupClient{
		replace: func(context.Context, string, client.ComponentRollupReplaceRequest, client.MutationOptions) (client.ComponentRollup, error) {
			return client.ComponentRollup{}, &client.ResponseError{
				StatusCode: http.StatusConflict,
				Code:       client.ErrorCodeAlreadyExists,
			}
		},
		get: func(context.Context, string) (client.ComponentRollup, error) {
			readCalled = true
			return client.ComponentRollup{}, nil
		},
	}}
	response := resource.CreateResponse{State: tfsdk.State{Schema: schemaValue, Raw: plan.Raw}}
	implementation.Create(context.Background(), resource.CreateRequest{Plan: plan}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected explicit import diagnostic for an existing rollup")
	}
	if readCalled {
		t.Fatal("create conflict implicitly read/adopted an existing rollup")
	}
	if got, want := response.Diagnostics[0].Summary(), "Pulse rollup already exists"; got != want {
		t.Fatalf("diagnostic summary = %q, want %q", got, want)
	}
}

func TestComponentRollupReadIsCanonicalAndStableOnSecondRefresh(t *testing.T) {
	t.Parallel()

	initial := mustComponentRollupModel(t, testRollupParentID, []client.RollupRule{{
		ChildComponentIDs: []string{testRollupChildAID, testRollupChildBID},
		WhenChildYellow:   client.RollupEffect("yellow"),
		WhenChildRed:      client.RollupEffect("red"),
	}}, 5)
	schemaValue := componentRollupTestSchema(t)
	initialState := tfsdk.State{Schema: schemaValue}
	assertNoDiagnostics(t, initialState.Set(context.Background(), &initial))

	remote := client.ComponentRollup{
		ParentComponentID: testRollupParentID,
		Rules: []client.RollupRule{{
			ChildComponentIDs: []string{testRollupChildBID, testRollupChildAID},
			WhenChildYellow:   client.RollupEffect("yellow"),
			WhenChildRed:      client.RollupEffect("red"),
		}},
		Revision: 5,
	}
	implementation := &componentRollupResource{client: &fakeComponentRollupClient{
		get: func(context.Context, string) (client.ComponentRollup, error) { return remote, nil },
	}}

	first := resource.ReadResponse{State: initialState}
	implementation.Read(context.Background(), resource.ReadRequest{State: initialState}, &first)
	if first.Diagnostics.HasError() {
		t.Fatalf("first refresh diagnostics: %v", first.Diagnostics)
	}
	second := resource.ReadResponse{State: first.State}
	implementation.Read(context.Background(), resource.ReadRequest{State: first.State}, &second)
	if second.Diagnostics.HasError() {
		t.Fatalf("second refresh diagnostics: %v", second.Diagnostics)
	}
	if !first.State.Raw.Equal(second.State.Raw) {
		t.Fatalf("second refresh changed canonical state:\nfirst=%#v\nsecond=%#v", first.State.Raw, second.State.Raw)
	}
}

func TestComponentRollupReadRemovesMissingRulesetFromState(t *testing.T) {
	t.Parallel()

	initial := mustComponentRollupModel(t, testRollupParentID, nil, 5)
	schemaValue := componentRollupTestSchema(t)
	initialState := tfsdk.State{Schema: schemaValue}
	assertNoDiagnostics(t, initialState.Set(context.Background(), &initial))

	implementation := &componentRollupResource{client: &fakeComponentRollupClient{
		get: func(context.Context, string) (client.ComponentRollup, error) {
			return client.ComponentRollup{}, &client.ResponseError{
				StatusCode: http.StatusNotFound,
				Code:       client.ErrorCodeNotFound,
			}
		},
	}}
	response := resource.ReadResponse{State: initialState}
	implementation.Read(context.Background(), resource.ReadRequest{State: initialState}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", response.Diagnostics)
	}
	if !response.State.Raw.IsNull() {
		t.Fatalf("missing remote rollup remained in state: %#v", response.State.Raw)
	}
}

func TestComponentRollupReadPreservesConfiguredEmptyRuleset(t *testing.T) {
	t.Parallel()

	initial := mustComponentRollupModel(t, testRollupParentID, []client.RollupRule{{
		ChildComponentIDs: []string{testRollupChildAID},
		WhenChildYellow:   client.RollupEffect("yellow"),
		WhenChildRed:      client.RollupEffect("red"),
	}}, 5)
	schemaValue := componentRollupTestSchema(t)
	initialState := tfsdk.State{Schema: schemaValue}
	assertNoDiagnostics(t, initialState.Set(context.Background(), &initial))

	implementation := &componentRollupResource{client: &fakeComponentRollupClient{
		get: func(context.Context, string) (client.ComponentRollup, error) {
			return client.ComponentRollup{
				ParentComponentID: testRollupParentID,
				Rules:             []client.RollupRule{},
				Revision:          6,
			}, nil
		},
	}}
	response := resource.ReadResponse{State: initialState}
	implementation.Read(context.Background(), resource.ReadRequest{State: initialState}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", response.Diagnostics)
	}
	if response.State.Raw.IsNull() {
		t.Fatal("configured empty rollup was mistaken for an absent resource")
	}

	var state componentRollupResourceModel
	assertNoDiagnostics(t, response.State.Get(context.Background(), &state))
	if state.Rules.IsNull() || state.Rules.IsUnknown() || len(state.Rules.Elements()) != 0 {
		t.Fatalf("configured empty rules state = %#v, want known empty list", state.Rules)
	}
	if got, want := state.Revision.ValueInt64(), int64(6); got != want {
		t.Fatalf("configured empty revision = %d, want %d", got, want)
	}
}

func TestComponentRollupStaleRevisionDiagnosticIsActionable(t *testing.T) {
	t.Parallel()

	var diagnostics diag.Diagnostics
	addComponentRollupError(&diagnostics, "update", &client.ResponseError{
		StatusCode: http.StatusConflict,
		Code:       client.ErrorCodeStaleRevision,
	})
	if got, want := diagnostics.ErrorsCount(), 1; got != want {
		t.Fatalf("diagnostic count = %d, want %d: %v", got, want, diagnostics)
	}
	if got, want := diagnostics[0].Summary(), "Pulse rollup changed outside Terraform"; got != want {
		t.Fatalf("diagnostic summary = %q, want %q", got, want)
	}
}

func TestComponentRollupDeleteCarriesRevision(t *testing.T) {
	t.Parallel()

	stateModel := mustComponentRollupModel(t, testRollupParentID, nil, 11)
	schemaValue := componentRollupTestSchema(t)
	state := tfsdk.State{Schema: schemaValue}
	assertNoDiagnostics(t, state.Set(context.Background(), &stateModel))

	called := false
	implementation := &componentRollupResource{client: &fakeComponentRollupClient{
		delete: func(_ context.Context, parentID string, options client.MutationOptions) error {
			called = true
			if parentID != testRollupParentID || options.Revision != 11 {
				t.Fatalf("delete parent/options = %q/%#v", parentID, options)
			}
			return nil
		},
	}}
	var response resource.DeleteResponse
	implementation.Delete(context.Background(), resource.DeleteRequest{State: state}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", response.Diagnostics)
	}
	if !called {
		t.Fatal("DeleteComponentRollup was not called")
	}
}

func TestComponentRollupDeleteRejectsMissingRevisionBeforeCallingAPI(t *testing.T) {
	t.Parallel()

	stateModel := mustComponentRollupModel(t, testRollupParentID, nil, 1)
	stateModel.Revision = types.Int64Unknown()
	schemaValue := componentRollupTestSchema(t)
	state := tfsdk.State{Schema: schemaValue}
	assertNoDiagnostics(t, state.Set(context.Background(), &stateModel))

	called := false
	implementation := &componentRollupResource{client: &fakeComponentRollupClient{
		delete: func(context.Context, string, client.MutationOptions) error {
			called = true
			return nil
		},
	}}
	var response resource.DeleteResponse
	implementation.Delete(context.Background(), resource.DeleteRequest{State: state}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("expected missing revision diagnostic")
	}
	if called {
		t.Fatal("delete API was called without a known positive revision")
	}
}

func TestComponentRollupImportValidatesParentUUID(t *testing.T) {
	t.Parallel()

	implementation := &componentRollupResource{}
	schemaValue := componentRollupTestSchema(t)
	initial := tfsdk.State{Schema: schemaValue}
	assertNoDiagnostics(t, initial.Set(context.Background(), &componentRollupResourceModel{
		ParentComponentID: types.StringNull(),
		Rules:             types.ListNull(types.ObjectType{AttrTypes: componentRollupRuleAttributeTypes}),
		Revision:          types.Int64Null(),
	}))

	valid := resource.ImportStateResponse{State: initial}
	implementation.ImportState(context.Background(), resource.ImportStateRequest{ID: testRollupParentID}, &valid)
	if valid.Diagnostics.HasError() {
		t.Fatalf("valid import diagnostics: %v", valid.Diagnostics)
	}
	var importedParent types.String
	assertNoDiagnostics(t, valid.State.GetAttribute(context.Background(), path.Root("parent_component_id"), &importedParent))
	if got := importedParent.ValueString(); got != testRollupParentID {
		t.Fatalf("imported parent ID = %q, want %q", got, testRollupParentID)
	}

	invalid := resource.ImportStateResponse{State: initial}
	implementation.ImportState(context.Background(), resource.ImportStateRequest{ID: "NOT-A-UUID"}, &invalid)
	if !invalid.Diagnostics.HasError() {
		t.Fatal("expected invalid import identifier diagnostic")
	}
}

func TestComponentRollupModelFromAPIRejectsInvalidRemoteContract(t *testing.T) {
	t.Parallel()

	tests := map[string]client.ComponentRollup{
		"mismatched parent": {
			ParentComponentID: testRollupChildAID,
			Rules:             []client.RollupRule{},
			Revision:          1,
		},
		"non-positive revision": {
			ParentComponentID: testRollupParentID,
			Rules:             []client.RollupRule{},
			Revision:          0,
		},
		"invalid effect": {
			ParentComponentID: testRollupParentID,
			Rules: []client.RollupRule{{
				ChildComponentIDs: []string{testRollupChildAID},
				WhenChildYellow:   client.RollupEffect("critical"),
				WhenChildRed:      client.RollupEffect("red"),
			}},
			Revision: 1,
		},
		"self child": {
			ParentComponentID: testRollupParentID,
			Rules: []client.RollupRule{{
				ChildComponentIDs: []string{testRollupParentID},
				WhenChildYellow:   client.RollupEffect("yellow"),
				WhenChildRed:      client.RollupEffect("red"),
			}},
			Revision: 1,
		},
	}

	for name, remote := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, diagnostics := componentRollupModelFromAPI(context.Background(), testRollupParentID, remote)
			if !diagnostics.HasError() {
				t.Fatal("expected invalid remote contract diagnostic")
			}
		})
	}
}

func componentRollupTestSchema(t *testing.T) resourceschema.Schema {
	t.Helper()
	var response resource.SchemaResponse
	NewComponentRollupResource().Schema(context.Background(), resource.SchemaRequest{}, &response)
	if response.Diagnostics.HasError() {
		t.Fatalf("schema diagnostics: %v", response.Diagnostics)
	}
	return response.Schema
}

func mustComponentRollupModel(t *testing.T, parentID string, rules []client.RollupRule, revision int64) componentRollupResourceModel {
	t.Helper()
	model, diagnostics := componentRollupModelFromAPI(context.Background(), parentID, client.ComponentRollup{
		ParentComponentID: parentID,
		Rules:             rules,
		Revision:          revision,
	})
	if revision == 0 {
		// The API never returns revision zero; this helper uses it only while
		// constructing Terraform's pre-create plan.
		model = componentRollupResourceModel{
			ParentComponentID: types.StringValue(parentID),
			Rules:             mustAPIRulesValue(t, rules),
			Revision:          types.Int64Unknown(),
		}
		return model
	}
	assertNoDiagnostics(t, diagnostics)
	return model
}

func mustAPIRulesValue(t *testing.T, rules []client.RollupRule) types.List {
	t.Helper()
	models := make([]componentRollupRuleResourceModel, 0, len(rules))
	for _, rule := range rules {
		models = append(models, componentRollupRuleResourceModel{
			ChildComponentIDs: mustStringSet(t, rule.ChildComponentIDs),
			WhenChildYellow:   types.StringValue(string(rule.WhenChildYellow)),
			WhenChildRed:      types.StringValue(string(rule.WhenChildRed)),
		})
	}
	return mustComponentRollupRulesValue(t, models)
}

func mustComponentRollupRulesValue(t *testing.T, rules []componentRollupRuleResourceModel) types.List {
	t.Helper()
	if rules == nil {
		rules = make([]componentRollupRuleResourceModel, 0)
	}
	value, diagnostics := types.ListValueFrom(context.Background(), types.ObjectType{AttrTypes: componentRollupRuleAttributeTypes}, rules)
	assertNoDiagnostics(t, diagnostics)
	return value
}

func mustStringSet(t *testing.T, values []string) types.Set {
	t.Helper()
	if values == nil {
		values = make([]string, 0)
	}
	value, diagnostics := types.SetValueFrom(context.Background(), types.StringType, values)
	assertNoDiagnostics(t, diagnostics)
	return value
}

func assertNoDiagnostics(t *testing.T, diagnostics interface {
	HasError() bool
}) {
	t.Helper()
	if diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diagnostics)
	}
}
