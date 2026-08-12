package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
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
	maxSecretReissueAttempts         = 3
)

// componentIntegrationResource manages the natural (component, provider)
// ingestion binding. Its one-time secret is retained only in sensitive state.
type componentIntegrationResource struct {
	api client.IntegrationAPI
}

type componentIntegrationResourceModel struct {
	ComponentID         types.String `tfsdk:"component_id"`
	IntegrationProvider types.String `tfsdk:"integration_provider"`
	RotationTrigger     types.String `tfsdk:"rotation_trigger"`
	Adopt               types.Bool   `tfsdk:"adopt"`
	Endpoint            types.String `tfsdk:"endpoint"`
	Secret              types.String `tfsdk:"secret"`
	Version             types.String `tfsdk:"version"`
	RotationRequired    types.Bool   `tfsdk:"rotation_required"`
	LifecycleOwner      types.String `tfsdk:"lifecycle_owner"`
	Revision            types.Int64  `tfsdk:"revision"`
}

func newComponentIntegrationResource() resource.Resource {
	return &componentIntegrationResource{}
}

func (r *componentIntegrationResource) Metadata(_ context.Context, _ resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = componentIntegrationResourceType
}

func (r *componentIntegrationResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		Version:             1,
		MarkdownDescription: "Manages one provider-specific ingestion integration bound to a Pulse component. Its one-time secret is stored in Terraform state and must be treated as sensitive.",
		Attributes: map[string]schema.Attribute{
			"component_id": schema.StringAttribute{
				MarkdownDescription: "UUID of the Pulse component that receives signals. Components may also own children and rollup rules.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"integration_provider": schema.StringAttribute{
				MarkdownDescription: "Signal adapter: `grafana`, `pagerduty`, or `pulse`. Together with component_id this is the immutable integration identity.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"rotation_trigger": schema.StringAttribute{
				MarkdownDescription: "Non-secret caller-controlled value. Changing it rotates the component-bound ingestion credential.",
				Required:            true,
			},
			"adopt": schema.BoolAttribute{
				MarkdownDescription: "Explicitly permit Terraform to adopt a human-owned integration. Adoption transfers lifecycle ownership and rotates the credential atomically.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"endpoint": schema.StringAttribute{
				MarkdownDescription: "Provider-specific component webhook endpoint.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"secret": schema.StringAttribute{
				MarkdownDescription: "One-time ingestion secret. Pulse never returns an existing secret again; import therefore requires an explicit rotation.",
				Computed:            true,
				Sensitive:           true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"version": schema.StringAttribute{
				MarkdownDescription: "Credential-version UUID associated with secret.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"rotation_required": schema.BoolAttribute{
				MarkdownDescription: "Whether Terraform must deliberately rotate because the active plaintext secret is unavailable or changed outside Terraform.",
				Computed:            true,
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"lifecycle_owner": schema.StringAttribute{
				MarkdownDescription: "Lifecycle owner reported by Pulse: `human` or `automation`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"revision": schema.Int64Attribute{
				MarkdownDescription: "Pulse configuration revision used for optimistic concurrency.",
				Computed:            true,
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
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
		response.Diagnostics.AddError("Unexpected Pulse API client", fmt.Sprintf("Expected a Pulse integration API client, got %T. This is a provider bug.", request.ProviderData))
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
		response.Diagnostics.AddAttributeError(path.Root("component_id"), "Invalid component UUID", "component_id must be a canonical UUID in 8-4-4-4-12 form.")
	}
	if !config.IntegrationProvider.IsNull() && !config.IntegrationProvider.IsUnknown() && !supportedIntegrationProvider(config.IntegrationProvider.ValueString()) {
		response.Diagnostics.AddAttributeError(path.Root("integration_provider"), "Unsupported integration provider", "integration_provider must be `grafana`, `pagerduty`, or `pulse`.")
	}
	if !config.RotationTrigger.IsNull() && !config.RotationTrigger.IsUnknown() {
		trigger := config.RotationTrigger.ValueString()
		switch {
		case strings.TrimSpace(trigger) == "":
			response.Diagnostics.AddAttributeError(path.Root("rotation_trigger"), "Invalid rotation trigger", "rotation_trigger must contain at least one non-whitespace character.")
		case len(trigger) > 256:
			response.Diagnostics.AddAttributeError(path.Root("rotation_trigger"), "Rotation trigger is too long", "rotation_trigger must be 256 bytes or fewer.")
		case strings.ContainsAny(trigger, "\r\n"):
			response.Diagnostics.AddAttributeError(path.Root("rotation_trigger"), "Invalid rotation trigger", "rotation_trigger must not contain line breaks.")
		}
	}
}

func (r *componentIntegrationResource) ModifyPlan(ctx context.Context, request resource.ModifyPlanRequest, response *resource.ModifyPlanResponse) {
	response.Plan = request.Plan
	if request.State.Raw.IsNull() {
		return
	}
	var state componentIntegrationResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if request.Plan.Raw.IsNull() {
		if state.LifecycleOwner.IsNull() || state.LifecycleOwner.IsUnknown() || state.LifecycleOwner.ValueString() != "automation" {
			response.Diagnostics.AddAttributeError(path.Root("lifecycle_owner"), "Explicit integration adoption required before destroy", "Terraform can destroy only an automation-owned integration. Set adopt = true and apply the ownership transfer before planning this destroy.")
		}
		return
	}

	var plan componentIntegrationResourceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	humanOwned := knownStringEquals(state.LifecycleOwner, "human")
	adoptionApproved := !plan.Adopt.IsNull() && !plan.Adopt.IsUnknown() && plan.Adopt.ValueBool()
	if humanOwned && !plan.Adopt.IsUnknown() && !adoptionApproved {
		response.Diagnostics.AddAttributeError(path.Root("adopt"), "Explicit integration adoption required", "This integration is human-owned. Set adopt = true to transfer lifecycle ownership to Terraform and rotate the ingestion credential atomically.")
		return
	}

	rotationChanged := rotationTriggerChanged(state.RotationTrigger, plan.RotationTrigger)
	rotationRequired := !state.RotationRequired.IsNull() && !state.RotationRequired.IsUnknown() && state.RotationRequired.ValueBool()
	if rotationRequired && !rotationChanged && !(humanOwned && adoptionApproved) {
		response.Diagnostics.AddAttributeError(path.Root("rotation_trigger"), "Pulse integration credential rotation required", "Terraform does not have the plaintext credential for the active Pulse version. Change rotation_trigger to a new non-secret value to rotate deliberately.")
		return
	}
	secretUnavailable := state.Secret.IsNull() || state.Secret.IsUnknown()
	if !rotationChanged && !secretUnavailable && !(humanOwned && adoptionApproved) {
		return
	}
	for _, attribute := range []string{"secret", "version", "rotation_required", "revision", "lifecycle_owner"} {
		response.Diagnostics.Append(response.Plan.SetAttribute(ctx, path.Root(attribute), unknownValueFor(attribute))...)
	}
}

func (r *componentIntegrationResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	if !r.requireAPI(&response.Diagnostics) {
		return
	}
	var plan componentIntegrationResourceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	componentID := plan.ComponentID.ValueString()
	provider := client.IntegrationProvider(plan.IntegrationProvider.ValueString())
	mutation, err := r.api.UpsertComponentIntegration(ctx, componentID, provider, client.ComponentIntegrationUpsertRequest{}, client.MutationOptions{})
	if err != nil {
		if metadata, ok := client.SecretReissueMetadataFromError(err); ok {
			mutation, err = r.reissueSecret(ctx, componentID, provider, metadata)
		} else if plan.Adopt.ValueBool() && isOwnershipConflict(err) {
			mutation, err = r.adoptExisting(ctx, componentID, provider)
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

func (r *componentIntegrationResource) adoptExisting(ctx context.Context, componentID string, provider client.IntegrationProvider) (client.ComponentIntegrationMutation, error) {
	existing, err := r.api.GetComponentIntegration(ctx, componentID, provider)
	if err != nil {
		return client.ComponentIntegrationMutation{}, err
	}
	if err := validateRemoteIntegration(existing, componentID, provider); err != nil {
		return client.ComponentIntegrationMutation{}, err
	}
	return r.api.UpsertComponentIntegration(ctx, componentID, provider, client.ComponentIntegrationUpsertRequest{Adopt: true}, client.MutationOptions{Revision: existing.Revision})
}

func (r *componentIntegrationResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	if !r.requireAPI(&response.Diagnostics) {
		return
	}
	var state componentIntegrationResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	componentID, provider, ok := integrationNaturalIdentity(state, &response.Diagnostics)
	if !ok {
		return
	}
	remote, err := r.api.GetComponentIntegration(ctx, componentID, provider)
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
	if err := validateRemoteIntegration(remote, componentID, provider); err != nil {
		response.Diagnostics.AddError("Invalid Pulse integration response", err.Error())
		return
	}
	previousVersionKnown := !state.Version.IsNull() && !state.Version.IsUnknown()
	versionChanged := previousVersionKnown && state.Version.ValueString() != remote.CredentialVersionID
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
		response.Diagnostics.AddWarning(title, "Terraform will not recover or fabricate unavailable plaintext. Change rotation_trigger to a new non-secret value to rotate deliberately.")
		return
	}
	state.RotationRequired = types.BoolValue(false)
	response.Diagnostics.Append(response.State.Set(ctx, &state)...)
}

func (r *componentIntegrationResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	if !r.requireAPI(&response.Diagnostics) {
		return
	}
	var state, plan componentIntegrationResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	componentID := plan.ComponentID.ValueString()
	provider := client.IntegrationProvider(plan.IntegrationProvider.ValueString())
	humanOwned := knownStringEquals(state.LifecycleOwner, "human")
	rotationRequired := rotationTriggerChanged(state.RotationTrigger, plan.RotationTrigger) || state.Secret.IsNull() || state.Secret.IsUnknown() || knownBool(state.RotationRequired)
	var (
		mutation client.ComponentIntegrationMutation
		mutated  bool
		err      error
	)
	operation := "update"
	if humanOwned {
		if plan.Adopt.IsNull() || plan.Adopt.IsUnknown() || !plan.Adopt.ValueBool() {
			response.Diagnostics.AddAttributeError(path.Root("adopt"), "Explicit integration adoption required", "Set adopt = true to transfer lifecycle ownership to Terraform and rotate the credential atomically.")
			return
		}
		operation = "adopt"
		mutation, err = r.api.UpsertComponentIntegration(ctx, componentID, provider, client.ComponentIntegrationUpsertRequest{Adopt: true}, client.MutationOptions{Revision: state.Revision.ValueInt64()})
		mutated = true
	} else if rotationRequired {
		operation = "rotate"
		mutation, err = r.api.RotateComponentIntegration(ctx, componentID, provider, client.MutationOptions{Revision: state.Revision.ValueInt64()})
		mutated = true
	}
	if err != nil {
		if metadata, ok := client.SecretReissueMetadataFromError(err); ok {
			mutation, err = r.reissueSecret(ctx, componentID, provider, metadata)
		}
	}
	if err != nil {
		addIntegrationMutationError(&response.Diagnostics, operation, err)
		return
	}
	if !mutated {
		plan.Endpoint = state.Endpoint
		plan.Secret = state.Secret
		plan.Version = state.Version
		plan.RotationRequired = state.RotationRequired
		plan.LifecycleOwner = state.LifecycleOwner
		plan.Revision = state.Revision
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
	if !r.requireAPI(&response.Diagnostics) {
		return
	}
	var state componentIntegrationResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	if !knownStringEquals(state.LifecycleOwner, "automation") {
		response.Diagnostics.AddAttributeError(path.Root("lifecycle_owner"), "Explicit integration adoption required before archive", "Terraform can archive only an automation-owned integration. Set adopt = true and apply before destroying this resource.")
		return
	}
	err := r.api.DeleteComponentIntegration(ctx, state.ComponentID.ValueString(), client.IntegrationProvider(state.IntegrationProvider.ValueString()), client.MutationOptions{Revision: state.Revision.ValueInt64()})
	if err != nil && !isNotFound(err) {
		addIntegrationMutationError(&response.Diagnostics, "archive", err)
	}
}

func (r *componentIntegrationResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	componentID, provider, ok := parseIntegrationImportID(request.ID)
	if !ok {
		response.Diagnostics.AddError("Invalid component integration import ID", "Import pulse_component_integration as `{component_uuid}/{provider}`, where provider is grafana, pagerduty, or pulse.")
		return
	}
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("component_id"), componentID)...)
	response.Diagnostics.Append(response.State.SetAttribute(ctx, path.Root("integration_provider"), provider)...)
}

func (r *componentIntegrationResource) reissueSecret(ctx context.Context, componentID string, provider client.IntegrationProvider, metadata client.SecretReissueMetadata) (client.ComponentIntegrationMutation, error) {
	for attempt := 0; attempt < maxSecretReissueAttempts; attempt++ {
		mutation, err := r.api.RotateComponentIntegration(ctx, componentID, provider, client.MutationOptions{Revision: metadata.Revision, RevokePredecessorImmediately: true})
		if err == nil {
			return mutation, nil
		}
		next, ok := client.SecretReissueMetadataFromError(err)
		if !ok {
			return client.ComponentIntegrationMutation{}, err
		}
		metadata = next
	}
	return client.ComponentIntegrationMutation{}, errors.New("pulse could not return a new component-integration secret after bounded reissue attempts")
}

func stateFromMutation(base componentIntegrationResourceModel, mutation client.ComponentIntegrationMutation) (componentIntegrationResourceModel, error) {
	provider := client.IntegrationProvider(base.IntegrationProvider.ValueString())
	if err := validateRemoteIntegration(mutation.Integration, base.ComponentID.ValueString(), provider); err != nil {
		return componentIntegrationResourceModel{}, err
	}
	if mutation.Secret == nil || strings.TrimSpace(mutation.Secret.Value) == "" {
		return componentIntegrationResourceModel{}, errors.New("pulse did not return the one-time component-integration secret")
	}
	if mutation.Secret.VersionID == "" || mutation.Secret.VersionID != mutation.Integration.CredentialVersionID {
		return componentIntegrationResourceModel{}, errors.New("pulse returned inconsistent component-integration credential version metadata")
	}
	base = mergeRemoteIntegration(base, mutation.Integration)
	base.Secret = types.StringValue(mutation.Secret.Value)
	base.Version = types.StringValue(mutation.Secret.VersionID)
	base.RotationRequired = types.BoolValue(false)
	return base, nil
}

func mergeRemoteIntegration(state componentIntegrationResourceModel, remote client.ComponentIntegration) componentIntegrationResourceModel {
	state.ComponentID = types.StringValue(remote.ComponentID)
	state.IntegrationProvider = types.StringValue(string(remote.Provider))
	state.Endpoint = types.StringValue(remote.Endpoint)
	state.Version = types.StringValue(remote.CredentialVersionID)
	state.LifecycleOwner = types.StringValue(string(remote.LifecycleOwner))
	state.Revision = types.Int64Value(remote.Revision)
	return state
}

func validateRemoteIntegration(remote client.ComponentIntegration, expectedComponentID string, expectedProvider client.IntegrationProvider) error {
	if remote.ComponentID != expectedComponentID {
		return errors.New("pulse returned a component integration bound to an unexpected component")
	}
	if remote.Provider != expectedProvider || !supportedIntegrationProvider(string(remote.Provider)) {
		return errors.New("pulse returned an unexpected component-integration provider")
	}
	if strings.TrimSpace(remote.Endpoint) == "" {
		return errors.New("pulse returned an empty component-integration endpoint")
	}
	if !isCanonicalUUID(remote.CredentialVersionID) {
		return errors.New("pulse returned an invalid component-integration credential version")
	}
	if remote.LifecycleOwner != client.IntegrationLifecycleOwnerHuman && remote.LifecycleOwner != client.IntegrationLifecycleOwnerAutomation {
		return errors.New("pulse returned an unsupported component-integration lifecycle owner")
	}
	if remote.Revision < 1 || remote.Status != client.IntegrationStatusActive {
		return errors.New("pulse returned invalid active component-integration metadata")
	}
	return nil
}

func integrationNaturalIdentity(state componentIntegrationResourceModel, diagnostics interface {
	AddAttributeError(path.Path, string, string)
}) (string, client.IntegrationProvider, bool) {
	if state.ComponentID.IsNull() || state.ComponentID.IsUnknown() || !isCanonicalUUID(state.ComponentID.ValueString()) {
		diagnostics.AddAttributeError(path.Root("component_id"), "Component UUID is unavailable", "The integration cannot be refreshed without a canonical component_id. Import it as `{component_uuid}/{provider}`.")
		return "", "", false
	}
	if state.IntegrationProvider.IsNull() || state.IntegrationProvider.IsUnknown() || !supportedIntegrationProvider(state.IntegrationProvider.ValueString()) {
		diagnostics.AddAttributeError(path.Root("integration_provider"), "Integration provider is unavailable", "The integration cannot be refreshed without grafana, pagerduty, or pulse provider identity.")
		return "", "", false
	}
	return state.ComponentID.ValueString(), client.IntegrationProvider(state.IntegrationProvider.ValueString()), true
}

func parseIntegrationImportID(value string) (string, string, bool) {
	parts := strings.Split(value, "/")
	if len(parts) != 2 || !isCanonicalUUID(parts[0]) || !supportedIntegrationProvider(parts[1]) {
		return "", "", false
	}
	return parts[0], parts[1], true
}

func supportedIntegrationProvider(value string) bool {
	return value == "grafana" || value == "pagerduty" || value == "pulse"
}

func knownStringEquals(value types.String, expected string) bool {
	return !value.IsNull() && !value.IsUnknown() && value.ValueString() == expected
}

func knownBool(value types.Bool) bool {
	return !value.IsNull() && !value.IsUnknown() && value.ValueBool()
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
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') && (character < 'A' || character > 'F') {
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
	return responseError.Code == client.ErrorCodeOwnershipConflict || responseError.Code == client.ErrorCodeAlreadyExists
}

func addIntegrationReadError(diagnostics interface{ AddError(string, string) }, err error) {
	diagnostics.AddError("Unable to read Pulse component integration", "The provider could not read the component-bound integration. "+safeClientError(err))
}

func addIntegrationMutationError(diagnostics interface{ AddError(string, string) }, operation string, err error) {
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

func (r *componentIntegrationResource) requireAPI(diagnostics interface{ AddError(string, string) }) bool {
	if r.api != nil {
		return true
	}
	diagnostics.AddError("Pulse API client is not configured", "The provider did not configure the Pulse API client. This is a provider bug.")
	return false
}

var (
	_ resource.Resource                   = (*componentIntegrationResource)(nil)
	_ resource.ResourceWithConfigure      = (*componentIntegrationResource)(nil)
	_ resource.ResourceWithImportState    = (*componentIntegrationResource)(nil)
	_ resource.ResourceWithModifyPlan     = (*componentIntegrationResource)(nil)
	_ resource.ResourceWithValidateConfig = (*componentIntegrationResource)(nil)
)
