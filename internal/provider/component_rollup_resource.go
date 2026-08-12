package provider

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/sreforce/terraform-provider-pulse/internal/client"
)

const componentRollupResourceTypeName = "component_rollup"

var componentRollupUUIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

var componentRollupRuleAttributeTypes = map[string]attr.Type{
	"child_component_ids": types.SetType{ElemType: types.StringType},
	"when_child_yellow":   types.StringType,
	"when_child_red":      types.StringType,
}

// componentRollupClient is deliberately narrower than the provider client. It
// makes the resource's automation API dependency explicit and keeps tests from
// receiving capabilities they do not exercise.
type componentRollupClient interface {
	client.RollupAPI
}

// componentRollupResource owns the complete ordered ruleset for one parent
// component. Partial ruleset ownership is intentionally unsupported.
type componentRollupResource struct {
	client componentRollupClient
}

type componentRollupResourceModel struct {
	ParentComponentID types.String `tfsdk:"parent_component_id"`
	Rules             types.List   `tfsdk:"rules"`
	Revision          types.Int64  `tfsdk:"revision"`
}

type componentRollupRuleResourceModel struct {
	ChildComponentIDs types.Set    `tfsdk:"child_component_ids"`
	WhenChildYellow   types.String `tfsdk:"when_child_yellow"`
	WhenChildRed      types.String `tfsdk:"when_child_red"`
}

// NewComponentRollupResource returns the aggregate Pulse rollup resource.
func NewComponentRollupResource() resource.Resource {
	return &componentRollupResource{}
}

func (r *componentRollupResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_" + componentRollupResourceTypeName
}

func (r *componentRollupResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Owns the complete ordered Pulse rollup ruleset for one parent component. An empty ruleset is valid and Pulse reports the rollup as `unknown` until rules are added.",
		Attributes: map[string]schema.Attribute{
			"parent_component_id": schema.StringAttribute{
				MarkdownDescription: "Lowercase UUID of the rollup parent component. Changing the parent replaces this Terraform resource.",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"rules": schema.ListNestedAttribute{
				MarkdownDescription: "Complete ordered ruleset for the parent. Rule order is significant; child component IDs inside each rule form an unordered set.",
				Required:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"child_component_ids": schema.SetAttribute{
							MarkdownDescription: "Non-empty set of lowercase child component UUIDs. A child may occur in only one rule.",
							Required:            true,
							ElementType:         types.StringType,
						},
						"when_child_yellow": schema.StringAttribute{
							MarkdownDescription: "Parent effect when a selected child is yellow: `none`, `yellow`, or `red`.",
							Required:            true,
						},
						"when_child_red": schema.StringAttribute{
							MarkdownDescription: "Parent effect when a selected child is red: `none`, `yellow`, or `red`.",
							Required:            true,
						},
					},
				},
			},
			"revision": schema.Int64Attribute{
				MarkdownDescription: "Computed configuration revision used for optimistic concurrency. Stale writes are rejected instead of overwriting external changes.",
				Computed:            true,
			},
		},
	}
}

func (r *componentRollupResource) Configure(_ context.Context, request resource.ConfigureRequest, response *resource.ConfigureResponse) {
	if request.ProviderData == nil {
		return
	}

	configuredClient, ok := request.ProviderData.(componentRollupClient)
	if !ok {
		response.Diagnostics.AddError(
			"Unexpected Pulse client type",
			fmt.Sprintf("The provider configured %T, but pulse_component_rollup requires the Pulse rollup automation client. This is a provider bug.", request.ProviderData),
		)
		return
	}
	r.client = configuredClient
}

func (r *componentRollupResource) ValidateConfig(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	var config componentRollupResourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}

	response.Diagnostics.Append(validateComponentRollupModel(ctx, config)...)
}

func (r *componentRollupResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	if !r.requireClient(&response.Diagnostics) {
		return
	}

	var plan componentRollupResourceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	rules, diagnostics := componentRollupRulesFromTerraform(ctx, plan.Rules)
	response.Diagnostics.Append(diagnostics...)
	if response.Diagnostics.HasError() {
		return
	}

	parentID := plan.ParentComponentID.ValueString()
	mutation := client.MutationOptions{
		Revision: 0,
	}
	created, err := r.client.ReplaceComponentRollup(ctx, parentID, client.ComponentRollupReplaceRequest{Rules: rules}, mutation)
	if err != nil {
		addComponentRollupError(&response.Diagnostics, "create", err)
		return
	}

	state, diagnostics := componentRollupModelFromAPI(ctx, parentID, created)
	response.Diagnostics.Append(diagnostics...)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func (r *componentRollupResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	if !r.requireClient(&response.Diagnostics) {
		return
	}

	var state componentRollupResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	parentID := state.ParentComponentID.ValueString()
	remote, err := r.client.GetComponentRollup(ctx, parentID)
	if err != nil {
		if client.IsErrorCode(err, client.ErrorCodeNotFound) {
			response.State.RemoveResource(ctx)
			return
		}
		addComponentRollupError(&response.Diagnostics, "read", err)
		return
	}

	refreshed, diagnostics := componentRollupModelFromAPI(ctx, parentID, remote)
	response.Diagnostics.Append(diagnostics...)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &refreshed)...)
}

func (r *componentRollupResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	if !r.requireClient(&response.Diagnostics) {
		return
	}

	var plan componentRollupResourceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	var prior componentRollupResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &prior)...)
	if response.Diagnostics.HasError() {
		return
	}
	if prior.Revision.IsNull() || prior.Revision.IsUnknown() || prior.Revision.ValueInt64() <= 0 {
		response.Diagnostics.AddAttributeError(
			path.Root("revision"),
			"Missing Pulse rollup revision",
			"Pulse cannot safely update this rollup without its current revision. Refresh state and plan again.",
		)
		return
	}

	rules, diagnostics := componentRollupRulesFromTerraform(ctx, plan.Rules)
	response.Diagnostics.Append(diagnostics...)
	if response.Diagnostics.HasError() {
		return
	}

	parentID := plan.ParentComponentID.ValueString()
	revision := prior.Revision.ValueInt64()
	mutation := client.MutationOptions{
		Revision: revision,
	}
	updated, err := r.client.ReplaceComponentRollup(ctx, parentID, client.ComponentRollupReplaceRequest{Rules: rules}, mutation)
	if err != nil {
		addComponentRollupError(&response.Diagnostics, "update", err)
		return
	}

	state, diagnostics := componentRollupModelFromAPI(ctx, parentID, updated)
	response.Diagnostics.Append(diagnostics...)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func (r *componentRollupResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	if !r.requireClient(&response.Diagnostics) {
		return
	}

	var state componentRollupResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if state.Revision.IsNull() || state.Revision.IsUnknown() || state.Revision.ValueInt64() <= 0 {
		response.Diagnostics.AddAttributeError(
			path.Root("revision"),
			"Missing Pulse rollup revision",
			"Pulse cannot safely delete this rollup without its current revision. Refresh state and plan again.",
		)
		return
	}

	parentID := state.ParentComponentID.ValueString()
	revision := state.Revision.ValueInt64()
	err := r.client.DeleteComponentRollup(ctx, parentID, client.MutationOptions{
		Revision: revision,
	})
	if err != nil && !client.IsErrorCode(err, client.ErrorCodeNotFound) {
		addComponentRollupError(&response.Diagnostics, "delete", err)
	}
}

func (r *componentRollupResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	if !componentRollupUUIDPattern.MatchString(request.ID) {
		response.Diagnostics.AddAttributeError(
			path.Root("parent_component_id"),
			"Invalid Pulse rollup import identifier",
			"Import pulse_component_rollup with the lowercase UUID of its parent component.",
		)
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("parent_component_id"), request, response)
}

func (r *componentRollupResource) requireClient(diagnostics *diag.Diagnostics) bool {
	if r.client != nil {
		return true
	}
	diagnostics.AddError(
		"Pulse client is not configured",
		"The provider did not configure a Pulse rollup automation client. This is a provider bug.",
	)
	return false
}

func validateComponentRollupModel(ctx context.Context, config componentRollupResourceModel) diag.Diagnostics {
	var diagnostics diag.Diagnostics

	if !config.ParentComponentID.IsNull() && !config.ParentComponentID.IsUnknown() && !componentRollupUUIDPattern.MatchString(config.ParentComponentID.ValueString()) {
		diagnostics.AddAttributeError(
			path.Root("parent_component_id"),
			"Invalid Pulse component UUID",
			"The rollup parent must be a lowercase UUID.",
		)
	}
	if config.Rules.IsNull() || config.Rules.IsUnknown() {
		return diagnostics
	}

	seenChildren := make(map[string]int)
	parentID := config.ParentComponentID.ValueString()
	for ruleIndex, element := range config.Rules.Elements() {
		object, ok := element.(types.Object)
		if !ok {
			diagnostics.AddAttributeError(
				path.Root("rules").AtListIndex(ruleIndex),
				"Invalid Pulse rollup rule value",
				"The provider could not interpret this rollup rule object. This is a provider bug.",
			)
			continue
		}
		// Terraform can leave a whole nested object unknown while it resolves
		// references. Attribute validation runs again once the object is known.
		if object.IsNull() || object.IsUnknown() {
			continue
		}
		var rule componentRollupRuleResourceModel
		objectDiagnostics := object.As(ctx, &rule, basetypes.ObjectAsOptions{})
		diagnostics.Append(objectDiagnostics...)
		if objectDiagnostics.HasError() {
			continue
		}
		rulePath := path.Root("rules").AtListIndex(ruleIndex)
		for _, candidate := range []struct {
			attributeName string
			effect        types.String
		}{
			{attributeName: "when_child_yellow", effect: rule.WhenChildYellow},
			{attributeName: "when_child_red", effect: rule.WhenChildRed},
		} {
			attributeName := candidate.attributeName
			effect := candidate.effect
			if effect.IsNull() || effect.IsUnknown() {
				continue
			}
			if !isComponentRollupEffect(effect.ValueString()) {
				diagnostics.AddAttributeError(
					rulePath.AtName(attributeName),
					"Invalid Pulse rollup effect",
					"Rollup effects must be one of `none`, `yellow`, or `red`.",
				)
			}
		}

		if rule.ChildComponentIDs.IsNull() || rule.ChildComponentIDs.IsUnknown() {
			continue
		}
		if len(rule.ChildComponentIDs.Elements()) == 0 {
			diagnostics.AddAttributeError(
				rulePath.AtName("child_component_ids"),
				"Empty Pulse rollup rule",
				"Each rollup rule must select at least one child component. The complete rules list itself may be empty.",
			)
			continue
		}

		for _, element := range rule.ChildComponentIDs.Elements() {
			childID, ok := element.(types.String)
			if !ok || childID.IsNull() || childID.IsUnknown() {
				continue
			}
			value := childID.ValueString()
			if !componentRollupUUIDPattern.MatchString(value) {
				diagnostics.AddAttributeError(
					rulePath.AtName("child_component_ids"),
					"Invalid Pulse child component UUID",
					"Every rollup child must be a lowercase UUID.",
				)
				continue
			}
			if value == parentID {
				diagnostics.AddAttributeError(
					rulePath.AtName("child_component_ids"),
					"Rollup cannot include itself",
					"The parent component cannot also be a child of its own rollup.",
				)
			}
			if priorRule, exists := seenChildren[value]; exists {
				diagnostics.AddAttributeError(
					rulePath.AtName("child_component_ids"),
					"Duplicate Pulse rollup child",
					fmt.Sprintf("Child component %s is already selected by rules[%d]. Each child can occur in only one rule.", value, priorRule),
				)
			} else {
				seenChildren[value] = ruleIndex
			}
		}
	}

	return diagnostics
}

func componentRollupRulesFromTerraform(ctx context.Context, value types.List) ([]client.RollupRule, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	var models []componentRollupRuleResourceModel
	diagnostics.Append(value.ElementsAs(ctx, &models, false)...)
	if diagnostics.HasError() {
		return nil, diagnostics
	}

	rules := make([]client.RollupRule, 0, len(models))
	for _, model := range models {
		var childIDs []string
		diagnostics.Append(model.ChildComponentIDs.ElementsAs(ctx, &childIDs, false)...)
		if diagnostics.HasError() {
			return nil, diagnostics
		}
		sort.Strings(childIDs)
		rules = append(rules, client.RollupRule{
			ChildComponentIDs: childIDs,
			WhenChildYellow:   client.RollupEffect(model.WhenChildYellow.ValueString()),
			WhenChildRed:      client.RollupEffect(model.WhenChildRed.ValueString()),
		})
	}
	return rules, diagnostics
}

func componentRollupModelFromAPI(ctx context.Context, expectedParentID string, remote client.ComponentRollup) (componentRollupResourceModel, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	if remote.ParentComponentID != expectedParentID {
		diagnostics.AddError(
			"Pulse returned a mismatched rollup parent",
			"The Pulse API response did not identify the parent component requested by Terraform. No state was changed.",
		)
		return componentRollupResourceModel{}, diagnostics
	}
	if remote.Revision <= 0 {
		diagnostics.AddError(
			"Pulse returned an invalid rollup revision",
			"The Pulse API returned a non-positive rollup revision. Terraform cannot safely manage optimistic concurrency for this resource.",
		)
		return componentRollupResourceModel{}, diagnostics
	}

	models := make([]componentRollupRuleResourceModel, 0, len(remote.Rules))
	seenChildren := make(map[string]struct{})
	for _, rule := range remote.Rules {
		if !isComponentRollupEffect(string(rule.WhenChildYellow)) || !isComponentRollupEffect(string(rule.WhenChildRed)) {
			diagnostics.AddError(
				"Pulse returned an invalid rollup effect",
				"The Pulse API returned a rollup effect outside `none`, `yellow`, or `red`. No state was changed.",
			)
			return componentRollupResourceModel{}, diagnostics
		}
		if len(rule.ChildComponentIDs) == 0 {
			diagnostics.AddError(
				"Pulse returned an empty rollup rule",
				"The Pulse API returned a rule without child components. No state was changed.",
			)
			return componentRollupResourceModel{}, diagnostics
		}

		childIDs := append([]string(nil), rule.ChildComponentIDs...)
		sort.Strings(childIDs)
		for _, childID := range childIDs {
			if !componentRollupUUIDPattern.MatchString(childID) || childID == expectedParentID {
				diagnostics.AddError(
					"Pulse returned an invalid rollup child",
					"The Pulse API returned a non-canonical or self-referencing child component UUID. No state was changed.",
				)
				return componentRollupResourceModel{}, diagnostics
			}
			if _, exists := seenChildren[childID]; exists {
				diagnostics.AddError(
					"Pulse returned a duplicate rollup child",
					"The Pulse API returned one child component in more than one rule. No state was changed.",
				)
				return componentRollupResourceModel{}, diagnostics
			}
			seenChildren[childID] = struct{}{}
		}

		childSet, childDiagnostics := types.SetValueFrom(ctx, types.StringType, childIDs)
		diagnostics.Append(childDiagnostics...)
		models = append(models, componentRollupRuleResourceModel{
			ChildComponentIDs: childSet,
			WhenChildYellow:   types.StringValue(string(rule.WhenChildYellow)),
			WhenChildRed:      types.StringValue(string(rule.WhenChildRed)),
		})
	}
	if diagnostics.HasError() {
		return componentRollupResourceModel{}, diagnostics
	}

	rules, ruleDiagnostics := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: componentRollupRuleAttributeTypes}, models)
	diagnostics.Append(ruleDiagnostics...)
	return componentRollupResourceModel{
		ParentComponentID: types.StringValue(expectedParentID),
		Rules:             rules,
		Revision:          types.Int64Value(remote.Revision),
	}, diagnostics
}

func isComponentRollupEffect(value string) bool {
	switch value {
	case "none", "yellow", "red":
		return true
	default:
		return false
	}
}

func addComponentRollupError(diagnostics *diag.Diagnostics, operation string, err error) {
	switch {
	case client.IsErrorCode(err, client.ErrorCodeStaleRevision):
		diagnostics.AddError(
			"Pulse rollup changed outside Terraform",
			"Pulse rejected the "+operation+" because this resource's revision is stale. Refresh state, review the external change, and plan again.",
		)
	case operation == "create" && (client.IsErrorCode(err, client.ErrorCodeAlreadyExists) || client.IsErrorCode(err, client.ErrorCodeOwnershipConflict)):
		diagnostics.AddError(
			"Pulse rollup already exists",
			"Pulse rejected the create because the parent already has a managed rollup ruleset. Import the parent UUID instead of overwriting it.",
		)
	case client.IsErrorCode(err, client.ErrorCodeAlreadyExists) || client.IsErrorCode(err, client.ErrorCodeOwnershipConflict):
		diagnostics.AddError(
			"Pulse rollup ownership conflict",
			"Pulse rejected the "+operation+" because another lifecycle owner controls this rollup. Refresh state and resolve ownership explicitly before trying again.",
		)
	default:
		diagnostics.AddError(
			"Unable to "+operation+" Pulse rollup",
			"The Pulse automation API request failed safely: "+safeComponentRollupError(err),
		)
	}
}

func safeComponentRollupError(err error) string {
	if err == nil {
		return "unknown error"
	}
	message := strings.TrimSpace(err.Error())
	if message == "" {
		return "unknown error"
	}
	return message
}

var (
	_ resource.Resource                   = (*componentRollupResource)(nil)
	_ resource.ResourceWithConfigure      = (*componentRollupResource)(nil)
	_ resource.ResourceWithImportState    = (*componentRollupResource)(nil)
	_ resource.ResourceWithValidateConfig = (*componentRollupResource)(nil)
)
