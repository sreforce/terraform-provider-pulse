package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/sreforce/terraform-provider-pulse/internal/client"
)

const (
	componentIntegrationResourceType = "pulse_component_integration"
	grafanaIntegrationSource         = "grafana"
	maxSecretReissueAttempts         = 3
)

var sourceKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,127}$`)

// componentIntegrationResource manages the one Grafana ingestion binding for
// an external component. The automation credential configures the binding; the
// returned ingestion secret can only submit signals to that binding.
type componentIntegrationResource struct {
	api client.IntegrationAPI
}

type componentIntegrationResourceModel struct {
	ID               types.String `tfsdk:"id"`
	ComponentID      types.String `tfsdk:"component_id"`
	Source           types.String `tfsdk:"source"`
	SourceKey        types.String `tfsdk:"source_key"`
	RotationTrigger  types.String `tfsdk:"rotation_trigger"`
	Adopt            types.Bool   `tfsdk:"adopt"`
	Endpoint         types.String `tfsdk:"endpoint"`
	Secret           types.String `tfsdk:"secret"`
	ObservedVersion  types.String `tfsdk:"observed_version"`
	RotationRequired types.Bool   `tfsdk:"rotation_required"`
	LifecycleOwner   types.String `tfsdk:"lifecycle_owner"`
	Revision         types.Int64  `tfsdk:"revision"`
	Status           types.String `tfsdk:"status"`
}

func newComponentIntegrationResource() resource.Resource {
	return &componentIntegrationResource{}
}

func (r *componentIntegrationResource) Metadata(_ context.Context, _ resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = componentIntegrationResourceType
}

func (r *componentIntegrationResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Manages the component-bound Grafana ingestion integration for one external Pulse component. The one-time secret is stored in Terraform state and must be treated as sensitive.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Pulse component-integration UUID.",
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"component_id": schema.StringAttribute{
				MarkdownDescription: "UUID of the external Pulse component receiving this integration. Rollup components cannot receive integrations.",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"source": schema.StringAttribute{
				MarkdownDescription: "Integration source. Version 0.1 supports only `grafana`.",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"source_key": schema.StringAttribute{
				MarkdownDescription: "Immutable Grafana mapping identity expected in the `pulse_alert_key` payload field.",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"rotation_trigger": schema.StringAttribute{
				MarkdownDescription: "Non-secret caller-controlled value. Changing it rotates the component-bound ingestion credential.",
				Required:            true,
			},
			"adopt": schema.BoolAttribute{
				MarkdownDescription: "Explicitly permit this resource to adopt a human-owned integration. Adoption transfers lifecycle ownership to automation and rotates its credential atomically.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"endpoint": schema.StringAttribute{
				MarkdownDescription: "Component-bound Grafana webhook endpoint.",
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"secret": schema.StringAttribute{
				MarkdownDescription: "One-time Grafana ingestion secret. Pulse never returns an existing secret again. Import therefore requires an explicit rotation.",
				Computed:            true,
				Sensitive:           true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"observed_version": schema.StringAttribute{
				MarkdownDescription: "Credential-version UUID associated with `secret`.",
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"rotation_required": schema.BoolAttribute{
				MarkdownDescription: "Whether Terraform must deliberately rotate before it can use this integration secret. This is true after import or out-of-band credential-version drift.",
				Computed:            true,
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"lifecycle_owner": schema.StringAttribute{
				MarkdownDescription: "Lifecycle owner reported by Pulse: `human` or `automation`.",
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"revision": schema.Int64Attribute{
				MarkdownDescription: "Pulse configuration revision used for optimistic concurrency.",
				Computed:            true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"status": schema.StringAttribute{
				MarkdownDescription: "Integration lifecycle status reported by Pulse.",
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *componentIntegrationResource) Configure(_ context.Context, request resource.ConfigureRequest, response *resource.ConfigureResponse) {
	if request.ProviderData == nil {
		return
	}

	configuredAPI, ok := request.ProviderData.(client.IntegrationAPI)
	if !ok {
		response.Diagnostics.AddError(
			"Unexpected Pulse API client",
			fmt.Sprintf("Expected a Pulse integration API client, got %T. This is a provider bug.", request.ProviderData),
		)
		return
	}
	r.api = configuredAPI
}

func (r *componentIntegrationResource) ValidateConfig(ctx context.Context, request resource.ValidateConfigRequest, response *resource.ValidateConfigResponse) {
	var config componentIntegrationResourceModel
	response.Diagnostics.Append(request.Config.Get(ctx, &config)...)
	if response.Diagnostics.HasError() {
		return
	}

	if !config.ComponentID.IsNull() && !config.ComponentID.IsUnknown() && !isCanonicalUUID(config.ComponentID.ValueString()) {
		response.Diagnostics.AddAttributeError(
			path.Root("component_id"),
			"Invalid component UUID",
			"component_id must be a canonical UUID in 8-4-4-4-12 form.",
		)
	}
	if !config.Source.IsNull() && !config.Source.IsUnknown() && config.Source.ValueString() != grafanaIntegrationSource {
		response.Diagnostics.AddAttributeError(
			path.Root("source"),
			"Unsupported integration source",
			"source must be `grafana` for this provider version.",
		)
	}
	if !config.SourceKey.IsNull() && !config.SourceKey.IsUnknown() && !sourceKeyPattern.MatchString(config.SourceKey.ValueString()) {
		response.Diagnostics.AddAttributeError(
			path.Root("source_key"),
			"Invalid Grafana source key",
			"source_key must start with a lowercase letter or digit and contain only lowercase letters, digits, dots, underscores, or hyphens (maximum 128 characters).",
		)
	}
	if !config.RotationTrigger.IsNull() && !config.RotationTrigger.IsUnknown() {
		trigger := config.RotationTrigger.ValueString()
		if strings.TrimSpace(trigger) == "" {
			response.Diagnostics.AddAttributeError(
				path.Root("rotation_trigger"),
				"Invalid rotation trigger",
				"rotation_trigger must contain at least one non-whitespace character.",
			)
		} else if len(trigger) > 256 {
			response.Diagnostics.AddAttributeError(
				path.Root("rotation_trigger"),
				"Rotation trigger is too long",
				"rotation_trigger must be 256 bytes or fewer.",
			)
		} else if strings.ContainsAny(trigger, "\r\n") {
			response.Diagnostics.AddAttributeError(
				path.Root("rotation_trigger"),
				"Invalid rotation trigger",
				"rotation_trigger must not contain line breaks.",
			)
		}
	}
}

func (r *componentIntegrationResource) ModifyPlan(ctx context.Context, request resource.ModifyPlanRequest, response *resource.ModifyPlanResponse) {
	response.Plan = request.Plan
	if request.State.Raw.IsNull() || request.Plan.Raw.IsNull() {
		return
	}

	var state componentIntegrationResourceModel
	var plan componentIntegrationResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	if !state.ComponentID.IsNull() && !state.ComponentID.IsUnknown() && !plan.ComponentID.IsNull() && !plan.ComponentID.IsUnknown() &&
		state.ComponentID.ValueString() == plan.ComponentID.ValueString() && rotationTriggerChanged(state.SourceKey, plan.SourceKey) {
		response.Diagnostics.AddAttributeError(
			path.Root("source_key"),
			"Grafana source key requires a new alert leaf",
			"source_key is the immutable identity of one Grafana alert mapping. Create a new Pulse leaf component and bind the new source_key there instead of changing it on this component.",
		)
		return
	}

	humanOwned := !state.LifecycleOwner.IsNull() && !state.LifecycleOwner.IsUnknown() && state.LifecycleOwner.ValueString() == "human"
	adoptionApproved := !plan.Adopt.IsNull() && !plan.Adopt.IsUnknown() && plan.Adopt.ValueBool()
	if humanOwned && !plan.Adopt.IsUnknown() && !adoptionApproved {
		response.Diagnostics.AddAttributeError(
			path.Root("adopt"),
			"Explicit integration adoption required",
			"This integration is human-owned. Set adopt = true to transfer lifecycle ownership to Terraform and rotate the ingestion credential atomically.",
		)
		return
	}

	rotationChanged := rotationTriggerChanged(state.RotationTrigger, plan.RotationTrigger)
	rotationRequired := !state.RotationRequired.IsNull() && !state.RotationRequired.IsUnknown() && state.RotationRequired.ValueBool()
	if rotationRequired && !rotationChanged {
		response.Diagnostics.AddAttributeError(
			path.Root("rotation_trigger"),
			"Pulse integration credential rotation required",
			"Terraform does not have the plaintext credential for the version currently active in Pulse. Change rotation_trigger to a new non-secret value to rotate deliberately; the existing plaintext cannot be recovered.",
		)
		return
	}
	secretUnavailable := state.Secret.IsNull()
	if !rotationChanged && !secretUnavailable && !(humanOwned && adoptionApproved) {
		return
	}

	// A rotate or adopt call returns these values. They must remain unknown in
	// the plan or Terraform would require the provider to return stale values.
	for _, attribute := range []string{"secret", "observed_version", "rotation_required", "revision", "lifecycle_owner", "status"} {
		response.Diagnostics.Append(response.Plan.SetAttribute(ctx, path.Root(attribute), unknownValueFor(attribute))...)
	}
}

func (r *componentIntegrationResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	if r.api == nil {
		response.Diagnostics.AddError("Pulse API client is not configured", "The provider did not configure the Pulse API client. This is a provider bug.")
		return
	}

	var plan componentIntegrationResourceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	componentID := plan.ComponentID.ValueString()
	mutation, err := r.api.CreateComponentIntegration(
		ctx,
		componentID,
		client.ComponentIntegrationCreateRequest{
			Source:    client.IntegrationSource(plan.Source.ValueString()),
			SourceKey: plan.SourceKey.ValueString(),
		},
		client.MutationOptions{},
	)
	if err != nil {
		if metadata, ok := client.SecretReissueMetadataFromError(err); ok {
			mutation, err = r.reissueSecret(ctx, componentID, metadata)
		} else if plan.Adopt.ValueBool() && isOwnershipConflict(err) {
			mutation, err = r.adoptExisting(ctx, componentID, plan.SourceKey.ValueString())
		}
	}
	if err != nil {
		addIntegrationMutationError(&response.Diagnostics, "create", err)
		return
	}

	updated, err := stateFromMutation(plan, mutation)
	if err != nil {
		response.Diagnostics.AddError("Invalid Pulse integration response", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &updated)...)
}

func (r *componentIntegrationResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	if r.api == nil {
		response.Diagnostics.AddError("Pulse API client is not configured", "The provider did not configure the Pulse API client. This is a provider bug.")
		return
	}

	var state componentIntegrationResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if state.ComponentID.IsNull() || state.ComponentID.IsUnknown() || !isCanonicalUUID(state.ComponentID.ValueString()) {
		response.Diagnostics.AddAttributeError(
			path.Root("component_id"),
			"Component UUID is unavailable",
			"The integration cannot be refreshed without a canonical component_id. Import this resource with the component UUID.",
		)
		return
	}

	remote, err := r.api.GetComponentIntegration(ctx, state.ComponentID.ValueString())
	if err != nil {
		if isNotFound(err) {
			response.State.RemoveResource(ctx)
			return
		}
		addIntegrationReadError(&response.Diagnostics, err)
		return
	}
	if strings.EqualFold(string(remote.Status), "archived") || remote.ArchivedAt != nil {
		response.State.RemoveResource(ctx)
		return
	}
	expectedSourceKey := ""
	if !state.SourceKey.IsNull() && !state.SourceKey.IsUnknown() {
		expectedSourceKey = state.SourceKey.ValueString()
	}
	if err := validateRemoteIntegration(remote, state.ComponentID.ValueString(), expectedSourceKey); err != nil {
		response.Diagnostics.AddError("Invalid Pulse integration response", err.Error())
		return
	}

	previousVersionKnown := !state.ObservedVersion.IsNull() && !state.ObservedVersion.IsUnknown()
	versionChanged := previousVersionKnown && state.ObservedVersion.ValueString() != remote.CredentialVersionID
	secretUnavailable := state.Secret.IsNull() || state.Secret.IsUnknown()
	state = mergeRemoteIntegration(state, remote)
	if versionChanged || secretUnavailable {
		state.Secret = types.StringNull()
		state.RotationRequired = types.BoolValue(true)
		response.Diagnostics.Append(response.State.Set(ctx, &state)...)
		title := "Pulse integration secret is unavailable after import"
		if versionChanged {
			title = "Pulse integration credential changed outside Terraform"
		}
		response.Diagnostics.AddWarning(
			title,
			"Terraform will not adopt or fabricate an unavailable plaintext credential. Change rotation_trigger to a new non-secret value; planning remains blocked until that deliberate rotation is configured.",
		)
		return
	}
	state.RotationRequired = types.BoolValue(false)

	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func (r *componentIntegrationResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	if r.api == nil {
		response.Diagnostics.AddError("Pulse API client is not configured", "The provider did not configure the Pulse API client. This is a provider bug.")
		return
	}

	var state componentIntegrationResourceModel
	var plan componentIntegrationResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}

	humanOwned := state.LifecycleOwner.ValueString() == "human"
	rotationRequired := rotationTriggerChanged(state.RotationTrigger, plan.RotationTrigger) || state.Secret.IsNull() || state.RotationRequired.ValueBool()
	var (
		mutation  client.ComponentIntegrationMutation
		mutated   bool
		operation = "update"
		err       error
	)

	if humanOwned {
		if plan.Adopt.IsNull() || plan.Adopt.IsUnknown() || !plan.Adopt.ValueBool() {
			response.Diagnostics.AddAttributeError(
				path.Root("adopt"),
				"Explicit integration adoption required",
				"This integration is human-owned. Set adopt = true to transfer lifecycle ownership to Terraform and rotate the ingestion credential atomically.",
			)
			return
		}
		operation = "adopt"
		mutation, err = r.api.AdoptComponentIntegration(
			ctx,
			plan.ComponentID.ValueString(),
			client.MutationOptions{Revision: state.Revision.ValueInt64()},
		)
		mutated = true
	} else if rotationRequired {
		operation = "rotate"
		mutation, err = r.api.RotateComponentIntegration(
			ctx,
			plan.ComponentID.ValueString(),
			client.MutationOptions{Revision: state.Revision.ValueInt64()},
		)
		mutated = true
	}

	if err != nil {
		if metadata, ok := client.SecretReissueMetadataFromError(err); ok {
			mutation, err = r.reissueSecret(ctx, plan.ComponentID.ValueString(), metadata)
		}
	}
	if err != nil {
		addIntegrationMutationError(&response.Diagnostics, operation, err)
		return
	}

	if !mutated {
		plan.ID = state.ID
		plan.Endpoint = state.Endpoint
		plan.Secret = state.Secret
		plan.ObservedVersion = state.ObservedVersion
		plan.RotationRequired = state.RotationRequired
		plan.LifecycleOwner = state.LifecycleOwner
		plan.Revision = state.Revision
		plan.Status = state.Status
		response.Diagnostics.Append(response.State.Set(ctx, &plan)...)
		return
	}

	updated, err := stateFromMutation(plan, mutation)
	if err != nil {
		response.Diagnostics.AddError("Invalid Pulse integration response", err.Error())
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, &updated)...)
}

func (r *componentIntegrationResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	if r.api == nil {
		response.Diagnostics.AddError("Pulse API client is not configured", "The provider did not configure the Pulse API client. This is a provider bug.")
		return
	}

	var state componentIntegrationResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}

	err := r.api.DeleteComponentIntegration(
		ctx,
		state.ComponentID.ValueString(),
		client.MutationOptions{Revision: state.Revision.ValueInt64()},
	)
	if err != nil && !isNotFound(err) {
		addIntegrationMutationError(&response.Diagnostics, "archive", err)
	}
}

func (r *componentIntegrationResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	if !isCanonicalUUID(request.ID) {
		response.Diagnostics.AddError(
			"Invalid component integration import ID",
			"Import pulse_component_integration with the bound component UUID in canonical 8-4-4-4-12 form.",
		)
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("component_id"), request, response)
}

func (r *componentIntegrationResource) adoptExisting(ctx context.Context, componentID, expectedSourceKey string) (client.ComponentIntegrationMutation, error) {
	existing, err := r.api.GetComponentIntegration(ctx, componentID)
	if err != nil {
		return client.ComponentIntegrationMutation{}, err
	}
	if err := validateRemoteIntegration(existing, componentID, expectedSourceKey); err != nil {
		return client.ComponentIntegrationMutation{}, err
	}
	return r.api.AdoptComponentIntegration(
		ctx,
		componentID,
		client.MutationOptions{Revision: existing.Revision},
	)
}

func (r *componentIntegrationResource) reissueSecret(ctx context.Context, componentID string, metadata client.SecretReissueMetadata) (client.ComponentIntegrationMutation, error) {
	for attempt := 0; attempt < maxSecretReissueAttempts; attempt++ {
		mutation, err := r.api.RotateComponentIntegration(
			ctx,
			componentID,
			client.MutationOptions{
				Revision:                     metadata.Revision,
				RevokePredecessorImmediately: true,
			},
		)
		if err == nil {
			return mutation, nil
		}
		next, ok := client.SecretReissueMetadataFromError(err)
		if !ok {
			return client.ComponentIntegrationMutation{}, err
		}
		metadata = next
	}
	return client.ComponentIntegrationMutation{}, errors.New("Pulse could not return a new component-integration secret after bounded reissue attempts")
}

func stateFromMutation(base componentIntegrationResourceModel, mutation client.ComponentIntegrationMutation) (componentIntegrationResourceModel, error) {
	if err := validateRemoteIntegration(mutation.Integration, base.ComponentID.ValueString(), base.SourceKey.ValueString()); err != nil {
		return componentIntegrationResourceModel{}, err
	}
	if mutation.Secret == nil || strings.TrimSpace(mutation.Secret.Value) == "" {
		return componentIntegrationResourceModel{}, errors.New("Pulse did not return the one-time component-integration secret")
	}
	if mutation.Secret.VersionID == "" || mutation.Secret.VersionID != mutation.Integration.CredentialVersionID {
		return componentIntegrationResourceModel{}, errors.New("Pulse returned inconsistent component-integration credential version metadata")
	}

	base = mergeRemoteIntegration(base, mutation.Integration)
	base.Secret = types.StringValue(mutation.Secret.Value)
	base.ObservedVersion = types.StringValue(mutation.Secret.VersionID)
	base.RotationRequired = types.BoolValue(false)
	return base, nil
}

func mergeRemoteIntegration(state componentIntegrationResourceModel, remote client.ComponentIntegration) componentIntegrationResourceModel {
	state.ID = types.StringValue(remote.ID)
	state.ComponentID = types.StringValue(remote.ComponentID)
	state.Source = types.StringValue(string(remote.Source))
	state.SourceKey = types.StringValue(remote.SourceKey)
	state.Endpoint = types.StringValue(remote.Endpoint)
	state.ObservedVersion = types.StringValue(remote.CredentialVersionID)
	state.LifecycleOwner = types.StringValue(string(remote.LifecycleOwner))
	state.Revision = types.Int64Value(remote.Revision)
	state.Status = types.StringValue(string(remote.Status))
	return state
}

func validateRemoteIntegration(remote client.ComponentIntegration, expectedComponentID, expectedSourceKey string) error {
	if !isCanonicalUUID(remote.ID) {
		return errors.New("Pulse returned an invalid component-integration UUID")
	}
	if remote.ComponentID != expectedComponentID {
		return errors.New("Pulse returned a component integration bound to an unexpected component")
	}
	if string(remote.Source) != grafanaIntegrationSource {
		return errors.New("Pulse returned an unsupported component-integration source")
	}
	if !sourceKeyPattern.MatchString(remote.SourceKey) {
		return errors.New("Pulse returned an invalid component-integration source key")
	}
	if expectedSourceKey != "" && remote.SourceKey != expectedSourceKey {
		return errors.New("Pulse returned a component integration with an unexpected source key")
	}
	if strings.TrimSpace(remote.Endpoint) == "" {
		return errors.New("Pulse returned an empty component-integration endpoint")
	}
	if !isCanonicalUUID(remote.CredentialVersionID) {
		return errors.New("Pulse returned an invalid component-integration credential version")
	}
	if string(remote.LifecycleOwner) != "human" && string(remote.LifecycleOwner) != "automation" {
		return errors.New("Pulse returned an unsupported component-integration lifecycle owner")
	}
	if remote.Revision < 1 {
		return errors.New("Pulse returned an invalid component-integration revision")
	}
	if remote.Status != client.IntegrationStatusActive {
		return errors.New("Pulse returned an unsupported component-integration status")
	}
	return nil
}

func rotationTriggerChanged(previous, planned types.String) bool {
	if planned.IsNull() || planned.IsUnknown() {
		return false
	}
	if previous.IsNull() || previous.IsUnknown() {
		return true
	}
	return previous.ValueString() != planned.ValueString()
}

func isCanonicalUUID(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return false
	}
	for index, character := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if !((character >= '0' && character <= '9') || (character >= 'a' && character <= 'f') || (character >= 'A' && character <= 'F')) {
			return false
		}
	}
	return true
}

func isNotFound(err error) bool {
	var responseError *client.ResponseError
	return errors.As(err, &responseError) && responseError.StatusCode == http.StatusNotFound
}

func isOwnershipConflict(err error) bool {
	var responseError *client.ResponseError
	if !errors.As(err, &responseError) {
		return false
	}
	return responseError.Code == "ownership_conflict" || responseError.Code == "already_exists"
}

func addIntegrationReadError(diagnostics interface {
	AddError(string, string)
}, err error) {
	diagnostics.AddError(
		"Unable to read Pulse component integration",
		"The provider could not read the component-bound integration. "+safeClientError(err),
	)
}

func addIntegrationMutationError(diagnostics interface {
	AddError(string, string)
}, operation string, err error) {
	detail := "The provider could not " + operation + " the component-bound integration. " + safeClientError(err)
	var responseError *client.ResponseError
	if errors.As(err, &responseError) && responseError.Code == "stale_revision" {
		detail = "Pulse rejected the mutation because the integration changed after Terraform refreshed it. Refresh the plan and retry; do not bypass the revision guard."
	}
	diagnostics.AddError("Unable to "+operation+" Pulse component integration", detail)
}

func safeClientError(err error) string {
	var responseError *client.ResponseError
	if errors.As(err, &responseError) {
		if responseError.Code != "" {
			return fmt.Sprintf("Pulse returned HTTP %d with error code %q.", responseError.StatusCode, responseError.Code)
		}
		return fmt.Sprintf("Pulse returned HTTP %d.", responseError.StatusCode)
	}
	return err.Error()
}

func unknownValueFor(attribute string) any {
	if attribute == "revision" {
		return types.Int64Unknown()
	}
	if attribute == "rotation_required" {
		return types.BoolUnknown()
	}
	return types.StringUnknown()
}

var (
	_ resource.Resource                   = (*componentIntegrationResource)(nil)
	_ resource.ResourceWithConfigure      = (*componentIntegrationResource)(nil)
	_ resource.ResourceWithImportState    = (*componentIntegrationResource)(nil)
	_ resource.ResourceWithModifyPlan     = (*componentIntegrationResource)(nil)
	_ resource.ResourceWithValidateConfig = (*componentIntegrationResource)(nil)
)
