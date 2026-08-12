package provider

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/sreforce/terraform-provider-pulse/internal/client"
)

// ComponentResource manages a single Pulse component's configuration. Runtime
// state remains computed so alert delivery cannot create configuration drift.
type ComponentResource struct {
	client client.ComponentAPI
}

type componentResourceModel struct {
	ID                    types.String `tfsdk:"id"`
	ExternalKey           types.String `tfsdk:"external_key"`
	Kind                  types.String `tfsdk:"kind"`
	Name                  types.String `tfsdk:"name"`
	ComponentTypeID       types.String `tfsdk:"component_type_id"`
	OwnerTeamID           types.String `tfsdk:"owner_team_id"`
	RelevanceTagIDs       types.Set    `tfsdk:"relevance_tag_ids"`
	FilterTagIDs          types.Set    `tfsdk:"filter_tag_ids"`
	AlertEnabled          types.Bool   `tfsdk:"alert_enabled"`
	State                 types.String `tfsdk:"state"`
	ConfigurationRevision types.Int64  `tfsdk:"configuration_revision"`
}

// NewComponentResource returns a pulse_component resource factory target.
func NewComponentResource() resource.Resource {
	return &ComponentResource{}
}

// Metadata sets the Terraform resource type name.
func (r *ComponentResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_component"
}

// Schema defines configuration-owned and computed component attributes.
func (r *ComponentResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Manages one organization-scoped Pulse component. Deleting the Terraform resource archives the component and preserves its operational history.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Pulse component UUID. Import existing components with this value.",
				Computed:            true,
			},
			"external_key": schema.StringAttribute{
				MarkdownDescription: "Organization-unique, immutable automation identity. Display names may be duplicated; this key may not.",
				Required:            true,
				Validators:          []validator.String{nonBlankStringValidator{}},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"kind": schema.StringAttribute{
				MarkdownDescription: "Immutable component mode. Must be `external` for a signal-receiving leaf or `rollup` for an aggregate. Changing it also requires a new `external_key`.",
				Required:            true,
				Validators:          []validator.String{componentKindValidator{}},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Human-readable component name. Names are intentionally not unique.",
				Required:            true,
				Validators:          []validator.String{nonBlankStringValidator{}},
			},
			"component_type_id": schema.StringAttribute{
				MarkdownDescription: "UUID of an existing component type in the authenticated organization.",
				Required:            true,
				Validators:          []validator.String{nonBlankStringValidator{}},
			},
			"owner_team_id": schema.StringAttribute{
				MarkdownDescription: "Optional UUID of the owning team in the authenticated organization.",
				Optional:            true,
				Validators:          []validator.String{nonBlankStringValidator{}},
			},
			"relevance_tag_ids": schema.SetAttribute{
				MarkdownDescription: "Set of organization relevance-tag UUIDs attached to the component.",
				ElementType:         types.StringType,
				Optional:            true,
				Computed:            true,
				Default:             setdefault.StaticValue(types.SetValueMust(types.StringType, []attr.Value{})),
			},
			"filter_tag_ids": schema.SetAttribute{
				MarkdownDescription: "Set of organization filter-tag UUIDs attached to the component.",
				ElementType:         types.StringType,
				Optional:            true,
				Computed:            true,
				Default:             setdefault.StaticValue(types.SetValueMust(types.StringType, []attr.Value{})),
			},
			"alert_enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether the component may initiate Pulse operational alerting. Shadow Grafana mappings should normally leave this false.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"state": schema.StringAttribute{
				MarkdownDescription: "Current computed runtime state. Signal-driven changes are observed but never treated as managed configuration.",
				Computed:            true,
			},
			"configuration_revision": schema.Int64Attribute{
				MarkdownDescription: "Current configuration revision used for optimistic concurrency. Runtime state changes do not increment it.",
				Computed:            true,
			},
		},
	}
}

// Configure receives the organization-scoped API client from the provider.
func (r *ComponentResource) Configure(_ context.Context, request resource.ConfigureRequest, response *resource.ConfigureResponse) {
	if request.ProviderData == nil {
		return
	}

	configuredClient, ok := request.ProviderData.(client.ComponentAPI)
	if !ok {
		response.Diagnostics.AddError(
			"Unexpected Pulse API client",
			fmt.Sprintf("The provider configured %T, but pulse_component requires the Pulse component automation client. This is a provider bug.", request.ProviderData),
		)
		return
	}
	r.client = configuredClient
}

// ModifyPlan prevents a kind-only replacement from attempting to restore the
// same immutable external identity with a different mode.
func (r *ComponentResource) ModifyPlan(ctx context.Context, request resource.ModifyPlanRequest, response *resource.ModifyPlanResponse) {
	if request.State.Raw.IsNull() || request.Plan.Raw.IsNull() {
		return
	}
	response.Plan = request.Plan

	var state componentResourceModel
	var plan componentResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() ||
		state.Kind.IsNull() || state.Kind.IsUnknown() ||
		plan.Kind.IsNull() || plan.Kind.IsUnknown() ||
		state.ExternalKey.IsNull() || state.ExternalKey.IsUnknown() ||
		plan.ExternalKey.IsNull() || plan.ExternalKey.IsUnknown() {
		return
	}

	sameExternalKey := state.ExternalKey.ValueString() == plan.ExternalKey.ValueString()
	sameKind := state.Kind.ValueString() == plan.Kind.ValueString()
	if !sameKind && sameExternalKey {
		response.Diagnostics.AddAttributeError(
			path.Root("kind"),
			"Pulse component kind is immutable",
			"Pulse restores an archived component when its external_key is reused, so changing kind also requires a new external_key.",
		)
		return
	}
	if sameExternalKey && sameKind && !state.ID.IsNull() && !state.ID.IsUnknown() && plan.ID.IsUnknown() {
		response.Diagnostics.Append(response.Plan.SetAttribute(ctx, path.Root("id"), state.ID)...)
	}
}

// Create creates or restores the component identified by external_key.
func (r *ComponentResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	if !r.requireClient(&response.Diagnostics) {
		return
	}

	var plan componentResourceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	createRequest, diagnostics := componentCreateRequestFromModel(ctx, plan)
	response.Diagnostics.Append(diagnostics...)
	if response.Diagnostics.HasError() {
		return
	}

	component, err := r.client.CreateComponent(ctx, createRequest, client.MutationOptions{})
	if err != nil {
		response.Diagnostics.AddError(
			"Unable to create Pulse component",
			"Pulse rejected the component configuration: "+err.Error(),
		)
		return
	}
	if !remoteComponentIdentityMatches(component, "", createRequest.ExternalKey, string(createRequest.Kind)) {
		response.Diagnostics.AddError(
			"Pulse returned an unexpected component identity",
			"The create response did not match the requested immutable external_key and kind. Terraform refused to adopt the unrelated component.",
		)
		return
	}

	state, diagnostics := componentModelFromRemote(ctx, component)
	response.Diagnostics.Append(diagnostics...)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

// Read refreshes both managed configuration and computed runtime state.
func (r *ComponentResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	if !r.requireClient(&response.Diagnostics) {
		return
	}

	var state componentResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	componentID, idDiagnostics := componentIDFromState(state)
	response.Diagnostics.Append(idDiagnostics...)
	if response.Diagnostics.HasError() {
		return
	}

	component, err := r.client.GetComponent(ctx, componentID)
	if err != nil {
		if client.IsErrorCode(err, client.ErrorCodeNotFound) {
			response.State.RemoveResource(ctx)
			return
		}
		response.Diagnostics.AddError(
			"Unable to read Pulse component",
			"Pulse could not refresh the component configuration: "+err.Error(),
		)
		return
	}
	if component.ArchivedAt != nil {
		response.State.RemoveResource(ctx)
		return
	}
	expectedExternalKey := ""
	if !state.ExternalKey.IsNull() && !state.ExternalKey.IsUnknown() {
		expectedExternalKey = state.ExternalKey.ValueString()
	}
	expectedKind := ""
	if !state.Kind.IsNull() && !state.Kind.IsUnknown() {
		expectedKind = state.Kind.ValueString()
	}
	if !remoteComponentIdentityMatches(component, componentID, expectedExternalKey, expectedKind) {
		response.Diagnostics.AddError(
			"Pulse returned an unexpected component identity",
			"The read response did not match the requested component UUID or its previously observed immutable identity.",
		)
		return
	}

	refreshed, diagnostics := componentModelFromRemote(ctx, component)
	response.Diagnostics.Append(diagnostics...)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &refreshed)...)
}

// Update replaces the complete mutable component configuration with an
// optimistic-concurrency precondition from the last observed revision.
func (r *ComponentResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	if !r.requireClient(&response.Diagnostics) {
		return
	}

	var plan componentResourceModel
	var state componentResourceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	updateRequest, diagnostics := componentUpdateRequestFromModel(ctx, plan)
	response.Diagnostics.Append(diagnostics...)
	if response.Diagnostics.HasError() {
		return
	}

	componentID, externalKey, kind, identityDiagnostics := componentStoredIdentityFromState(state)
	response.Diagnostics.Append(identityDiagnostics...)
	revision, revisionDiagnostics := componentRevisionFromState(state)
	response.Diagnostics.Append(revisionDiagnostics...)
	if response.Diagnostics.HasError() {
		return
	}
	component, err := r.client.UpdateComponent(ctx, componentID, updateRequest, client.MutationOptions{Revision: revision})
	if err != nil {
		if client.IsErrorCode(err, client.ErrorCodeNotFound) {
			response.Diagnostics.AddError(
				"Pulse component no longer exists",
				"The component was removed or archived after planning. Refresh the Terraform state and plan again.",
			)
			return
		}
		if client.IsErrorCode(err, client.ErrorCodeStaleRevision) {
			response.Diagnostics.AddError(
				"Pulse component changed concurrently",
				"The component configuration changed after Terraform planned this update. Refresh and review a new plan before applying.",
			)
			return
		}
		response.Diagnostics.AddError(
			"Unable to update Pulse component",
			"Pulse rejected the component configuration update: "+err.Error(),
		)
		return
	}
	if !remoteComponentIdentityMatches(component, componentID, externalKey, kind) {
		response.Diagnostics.AddError(
			"Pulse returned an unexpected component identity",
			"The update response did not match the component UUID, external_key, and kind that Terraform updated.",
		)
		return
	}

	updated, diagnostics := componentModelFromRemote(ctx, component)
	response.Diagnostics.Append(diagnostics...)
	if response.Diagnostics.HasError() {
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &updated)...)
}

// Delete archives the component rather than deleting operational history.
func (r *ComponentResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	if !r.requireClient(&response.Diagnostics) {
		return
	}

	var state componentResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	componentID, idDiagnostics := componentIDFromState(state)
	response.Diagnostics.Append(idDiagnostics...)
	revision, revisionDiagnostics := componentRevisionFromState(state)
	response.Diagnostics.Append(revisionDiagnostics...)
	if response.Diagnostics.HasError() {
		return
	}
	err := r.client.ArchiveComponent(ctx, componentID, client.MutationOptions{Revision: revision})
	if err == nil || client.IsErrorCode(err, client.ErrorCodeNotFound) {
		return
	}
	if client.IsErrorCode(err, client.ErrorCodeStaleRevision) {
		response.Diagnostics.AddError(
			"Pulse component changed concurrently",
			"The component configuration changed after Terraform planned this archival. Refresh and review a new plan before applying.",
		)
		return
	}
	response.Diagnostics.AddError(
		"Unable to archive Pulse component",
		"Pulse could not archive the component while preserving its operational history: "+err.Error(),
	)
}

// ImportState adopts an existing component by its Pulse UUID.
func (r *ComponentResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
}

func (r *ComponentResource) requireClient(diagnostics *diag.Diagnostics) bool {
	if r.client != nil {
		return true
	}
	diagnostics.AddError(
		"Pulse API client is not configured",
		"Configure the Pulse provider before managing pulse_component resources. This is a provider bug if configuration already succeeded.",
	)
	return false
}

func componentCreateRequestFromModel(ctx context.Context, model componentResourceModel) (client.ComponentCreateRequest, diag.Diagnostics) {
	diagnostics := validateComponentPlan(model, true)
	relevanceTagIDs, filterTagIDs, ownerTeamID, referenceDiagnostics := componentReferencesFromModel(ctx, model)
	diagnostics.Append(referenceDiagnostics...)
	if diagnostics.HasError() {
		return client.ComponentCreateRequest{}, diagnostics
	}

	return client.ComponentCreateRequest{
		ExternalKey:     model.ExternalKey.ValueString(),
		Kind:            client.ComponentKind(model.Kind.ValueString()),
		Name:            model.Name.ValueString(),
		ComponentTypeID: model.ComponentTypeID.ValueString(),
		OwnerTeamID:     ownerTeamID,
		RelevanceTagIDs: relevanceTagIDs,
		FilterTagIDs:    filterTagIDs,
		AlertEnabled:    model.AlertEnabled.ValueBool(),
	}, diagnostics
}

func componentUpdateRequestFromModel(ctx context.Context, model componentResourceModel) (client.ComponentUpdateRequest, diag.Diagnostics) {
	diagnostics := validateComponentPlan(model, false)
	relevanceTagIDs, filterTagIDs, ownerTeamID, referenceDiagnostics := componentReferencesFromModel(ctx, model)
	diagnostics.Append(referenceDiagnostics...)
	if diagnostics.HasError() {
		return client.ComponentUpdateRequest{}, diagnostics
	}

	return client.ComponentUpdateRequest{
		Name:            model.Name.ValueString(),
		ComponentTypeID: model.ComponentTypeID.ValueString(),
		OwnerTeamID:     ownerTeamID,
		RelevanceTagIDs: relevanceTagIDs,
		FilterTagIDs:    filterTagIDs,
		AlertEnabled:    model.AlertEnabled.ValueBool(),
	}, diagnostics
}

func componentReferencesFromModel(ctx context.Context, model componentResourceModel) ([]string, []string, *string, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	var relevanceTagIDs []string
	diagnostics.Append(model.RelevanceTagIDs.ElementsAs(ctx, &relevanceTagIDs, false)...)
	sort.Strings(relevanceTagIDs)
	var filterTagIDs []string
	diagnostics.Append(model.FilterTagIDs.ElementsAs(ctx, &filterTagIDs, false)...)
	sort.Strings(filterTagIDs)

	var ownerTeamID *string
	if !model.OwnerTeamID.IsNull() && !model.OwnerTeamID.IsUnknown() {
		value := model.OwnerTeamID.ValueString()
		ownerTeamID = &value
	}
	return relevanceTagIDs, filterTagIDs, ownerTeamID, diagnostics
}

func validateComponentPlan(model componentResourceModel, includeIdentity bool) diag.Diagnostics {
	var diagnostics diag.Diagnostics
	knownString := func(attributePath path.Path, value types.String) {
		if value.IsNull() || value.IsUnknown() {
			diagnostics.AddAttributeError(
				attributePath,
				"Pulse component value is not known",
				"This value must be known before Pulse can apply the component configuration.",
			)
		}
	}

	if includeIdentity {
		knownString(path.Root("external_key"), model.ExternalKey)
		knownString(path.Root("kind"), model.Kind)
	}
	knownString(path.Root("name"), model.Name)
	knownString(path.Root("component_type_id"), model.ComponentTypeID)
	if model.OwnerTeamID.IsUnknown() {
		diagnostics.AddAttributeError(
			path.Root("owner_team_id"),
			"Pulse component owner is not known",
			"The owner team must be known or null before Pulse can apply the component configuration.",
		)
	}
	if model.RelevanceTagIDs.IsNull() || model.RelevanceTagIDs.IsUnknown() {
		diagnostics.AddAttributeError(
			path.Root("relevance_tag_ids"),
			"Pulse component relevance tags are not known",
			"The relevance tag set must be known before Pulse can apply the component configuration.",
		)
	}
	if model.FilterTagIDs.IsNull() || model.FilterTagIDs.IsUnknown() {
		diagnostics.AddAttributeError(
			path.Root("filter_tag_ids"),
			"Pulse component filter tags are not known",
			"The filter tag set must be known before Pulse can apply the component configuration.",
		)
	}
	if model.AlertEnabled.IsNull() || model.AlertEnabled.IsUnknown() {
		diagnostics.AddAttributeError(
			path.Root("alert_enabled"),
			"Pulse alert setting is not known",
			"The alert_enabled value must be known before Pulse can apply the component configuration.",
		)
	}
	return diagnostics
}

func componentRevisionFromState(model componentResourceModel) (int64, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	if model.ConfigurationRevision.IsNull() || model.ConfigurationRevision.IsUnknown() || model.ConfigurationRevision.ValueInt64() < 1 {
		diagnostics.AddAttributeError(
			path.Root("configuration_revision"),
			"Pulse component revision is not available",
			"Refresh the component before applying a configuration update or archival so Terraform can enforce optimistic concurrency.",
		)
		return 0, diagnostics
	}
	return model.ConfigurationRevision.ValueInt64(), diagnostics
}

func componentIDFromState(model componentResourceModel) (string, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	if model.ID.IsNull() || model.ID.IsUnknown() || strings.TrimSpace(model.ID.ValueString()) == "" {
		diagnostics.AddAttributeError(
			path.Root("id"),
			"Pulse component UUID is not available",
			"Refresh or import the component before Terraform performs this operation.",
		)
		return "", diagnostics
	}
	return model.ID.ValueString(), diagnostics
}

func componentStoredIdentityFromState(model componentResourceModel) (string, string, string, diag.Diagnostics) {
	componentID, diagnostics := componentIDFromState(model)
	if model.ExternalKey.IsNull() || model.ExternalKey.IsUnknown() || strings.TrimSpace(model.ExternalKey.ValueString()) == "" {
		diagnostics.AddAttributeError(
			path.Root("external_key"),
			"Pulse component external identity is not available",
			"Refresh the component before applying an update so Terraform can verify immutable identity.",
		)
	}
	if model.Kind.IsNull() || model.Kind.IsUnknown() || strings.TrimSpace(model.Kind.ValueString()) == "" {
		diagnostics.AddAttributeError(
			path.Root("kind"),
			"Pulse component kind is not available",
			"Refresh the component before applying an update so Terraform can verify immutable identity.",
		)
	}
	return componentID, model.ExternalKey.ValueString(), model.Kind.ValueString(), diagnostics
}

func componentModelFromRemote(ctx context.Context, component client.Component) (componentResourceModel, diag.Diagnostics) {
	var diagnostics diag.Diagnostics
	if strings.TrimSpace(component.ID) == "" ||
		strings.TrimSpace(component.ExternalKey) == "" ||
		strings.TrimSpace(string(component.Kind)) == "" ||
		strings.TrimSpace(component.Name) == "" ||
		strings.TrimSpace(component.ComponentTypeID) == "" ||
		strings.TrimSpace(string(component.State)) == "" ||
		component.Revision < 1 ||
		component.ArchivedAt != nil {
		diagnostics.AddError(
			"Pulse returned an invalid component",
			"The automation API response was archived or omitted required component identity, configuration, runtime state, or revision data.",
		)
		return componentResourceModel{}, diagnostics
	}
	relevanceTags := component.RelevanceTagIDs
	if relevanceTags == nil {
		relevanceTags = []string{}
	}
	filterTags := component.FilterTagIDs
	if filterTags == nil {
		filterTags = []string{}
	}
	relevanceTagIDs, relevanceTagDiagnostics := types.SetValueFrom(ctx, types.StringType, relevanceTags)
	diagnostics.Append(relevanceTagDiagnostics...)
	filterTagIDs, filterTagDiagnostics := types.SetValueFrom(ctx, types.StringType, filterTags)
	diagnostics.Append(filterTagDiagnostics...)

	ownerTeamID := types.StringNull()
	if component.OwnerTeamID != nil {
		ownerTeamID = types.StringValue(*component.OwnerTeamID)
	}

	return componentResourceModel{
		ID:                    types.StringValue(component.ID),
		ExternalKey:           types.StringValue(component.ExternalKey),
		Kind:                  types.StringValue(string(component.Kind)),
		Name:                  types.StringValue(component.Name),
		ComponentTypeID:       types.StringValue(component.ComponentTypeID),
		OwnerTeamID:           ownerTeamID,
		RelevanceTagIDs:       relevanceTagIDs,
		FilterTagIDs:          filterTagIDs,
		AlertEnabled:          types.BoolValue(component.AlertEnabled),
		State:                 types.StringValue(string(component.State)),
		ConfigurationRevision: types.Int64Value(component.Revision),
	}, diagnostics
}

func remoteComponentIdentityMatches(component client.Component, expectedID, expectedExternalKey, expectedKind string) bool {
	if expectedID != "" && component.ID != expectedID {
		return false
	}
	if expectedExternalKey != "" && component.ExternalKey != expectedExternalKey {
		return false
	}
	return expectedKind == "" || string(component.Kind) == expectedKind
}

type componentKindValidator struct{}

func (componentKindValidator) Description(context.Context) string {
	return "value must be external or rollup"
}

func (componentKindValidator) MarkdownDescription(context.Context) string {
	return "value must be `external` or `rollup`"
}

func (componentKindValidator) ValidateString(_ context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	if value := request.ConfigValue.ValueString(); value != "external" && value != "rollup" {
		response.Diagnostics.AddAttributeError(
			request.Path,
			"Invalid Pulse component kind",
			fmt.Sprintf("Kind must be either \"external\" or \"rollup\", not %q.", value),
		)
	}
}

type nonBlankStringValidator struct{}

func (nonBlankStringValidator) Description(context.Context) string {
	return "value must contain non-whitespace characters and no surrounding whitespace"
}

func (nonBlankStringValidator) MarkdownDescription(context.Context) string {
	return "value must contain non-whitespace characters and no surrounding whitespace"
}

func (nonBlankStringValidator) ValidateString(_ context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	value := request.ConfigValue.ValueString()
	if strings.TrimSpace(value) == "" || value != strings.TrimSpace(value) {
		response.Diagnostics.AddAttributeError(
			request.Path,
			"Invalid Pulse component value",
			"The value must contain non-whitespace characters and must not contain surrounding whitespace.",
		)
	}
}

var (
	_ resource.Resource                = (*ComponentResource)(nil)
	_ resource.ResourceWithConfigure   = (*ComponentResource)(nil)
	_ resource.ResourceWithImportState = (*ComponentResource)(nil)
	_ resource.ResourceWithModifyPlan  = (*ComponentResource)(nil)
	_ validator.String                 = componentKindValidator{}
	_ validator.String                 = nonBlankStringValidator{}
)
