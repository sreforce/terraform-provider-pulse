package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/sreforce/terraform-provider-pulse/internal/client"
)

type ComponentTypeResource struct {
	client client.CatalogAPI
}

type componentTypeResourceModel struct {
	ID           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	GreenLabel   types.String `tfsdk:"green_label"`
	YellowLabel  types.String `tfsdk:"yellow_label"`
	RedLabel     types.String `tfsdk:"red_label"`
	UnknownLabel types.String `tfsdk:"unknown_label"`
	Revision     types.Int64  `tfsdk:"revision"`
}

func NewComponentTypeResource() resource.Resource { return &ComponentTypeResource{} }

func (r *ComponentTypeResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_component_type"
}

func (r *ComponentTypeResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Manages one organization-scoped Pulse component type. Deletion is rejected while any component references the type.",
		Attributes: map[string]schema.Attribute{
			"id":            schema.StringAttribute{Computed: true, MarkdownDescription: "Pulse component type UUID."},
			"name":          catalogRequiredString("Organization-unique component type name."),
			"green_label":   catalogRequiredString("Label rendered for green state."),
			"yellow_label":  catalogRequiredString("Label rendered for yellow state."),
			"red_label":     catalogRequiredString("Label rendered for red state."),
			"unknown_label": catalogRequiredString("Label rendered for unknown state."),
			"revision":      schema.Int64Attribute{Computed: true, MarkdownDescription: "Configuration revision used for optimistic concurrency."},
		},
	}
}

func catalogRequiredString(description string) schema.StringAttribute {
	return schema.StringAttribute{Required: true, MarkdownDescription: description, Validators: []validator.String{nonBlankStringValidator{}}}
}

func (r *ComponentTypeResource) Configure(_ context.Context, request resource.ConfigureRequest, response *resource.ConfigureResponse) {
	r.client = configureCatalogResource(request.ProviderData, &response.Diagnostics, "pulse_component_type")
}

func (r *ComponentTypeResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	if !requireCatalogClient(r.client, &response.Diagnostics, "pulse_component_type") {
		return
	}
	var plan componentTypeResourceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	item, err := r.client.CreateComponentType(ctx, componentTypeWrite(plan), client.MutationOptions{})
	if err != nil {
		addCatalogMutationError(&response.Diagnostics, "create", "component type", err)
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, componentTypeState(item))...)
}

func (r *ComponentTypeResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	if !requireCatalogClient(r.client, &response.Diagnostics, "pulse_component_type") {
		return
	}
	var state componentTypeResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	item, err := r.client.GetComponentType(ctx, state.ID.ValueString())
	if client.IsErrorCode(err, client.ErrorCodeNotFound) {
		response.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addCatalogMutationError(&response.Diagnostics, "read", "component type", err)
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, componentTypeState(item))...)
}

func (r *ComponentTypeResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	if !requireCatalogClient(r.client, &response.Diagnostics, "pulse_component_type") {
		return
	}
	var plan, state componentTypeResourceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	item, err := r.client.UpdateComponentType(ctx, state.ID.ValueString(), componentTypeWrite(plan), client.MutationOptions{Revision: state.Revision.ValueInt64()})
	if err != nil {
		addCatalogMutationError(&response.Diagnostics, "update", "component type", err)
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, componentTypeState(item))...)
}

func (r *ComponentTypeResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	if !requireCatalogClient(r.client, &response.Diagnostics, "pulse_component_type") {
		return
	}
	var state componentTypeResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteComponentType(ctx, state.ID.ValueString(), client.MutationOptions{Revision: state.Revision.ValueInt64()})
	if err != nil && !client.IsErrorCode(err, client.ErrorCodeNotFound) {
		addCatalogMutationError(&response.Diagnostics, "delete", "component type", err)
	}
}

func (r *ComponentTypeResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
}

func componentTypeWrite(model componentTypeResourceModel) client.ComponentTypeWriteRequest {
	return client.ComponentTypeWriteRequest{Name: model.Name.ValueString(), GreenLabel: model.GreenLabel.ValueString(), YellowLabel: model.YellowLabel.ValueString(), RedLabel: model.RedLabel.ValueString(), UnknownLabel: model.UnknownLabel.ValueString()}
}

func componentTypeState(item client.ComponentType) componentTypeResourceModel {
	return componentTypeResourceModel{ID: types.StringValue(item.ID), Name: types.StringValue(item.Name), GreenLabel: types.StringValue(item.GreenLabel), YellowLabel: types.StringValue(item.YellowLabel), RedLabel: types.StringValue(item.RedLabel), UnknownLabel: types.StringValue(item.UnknownLabel), Revision: types.Int64Value(item.Revision)}
}

func configureCatalogResource(providerData any, diagnostics *diag.Diagnostics, name string) client.CatalogAPI {
	if providerData == nil {
		return nil
	}
	configured, ok := providerData.(client.CatalogAPI)
	if !ok {
		diagnostics.AddError("Unexpected Pulse API client", fmt.Sprintf("The provider configured %T, but %s requires the Pulse catalog automation client. This is a provider bug.", providerData, name))
		return nil
	}
	return configured
}

func requireCatalogClient(configured client.CatalogAPI, diagnostics *diag.Diagnostics, name string) bool {
	if configured != nil {
		return true
	}
	diagnostics.AddError("Pulse API client is not configured", "Configure the Pulse provider before managing "+name+" resources.")
	return false
}

func addCatalogMutationError(diagnostics *diag.Diagnostics, operation, noun string, err error) {
	if client.IsErrorCode(err, client.ErrorCodeStaleRevision) {
		diagnostics.AddError("Pulse "+noun+" changed concurrently", "Refresh state and review a new plan before applying.")
		return
	}
	diagnostics.AddError("Unable to "+operation+" Pulse "+noun, err.Error())
}

var (
	_ resource.Resource                = (*ComponentTypeResource)(nil)
	_ resource.ResourceWithConfigure   = (*ComponentTypeResource)(nil)
	_ resource.ResourceWithImportState = (*ComponentTypeResource)(nil)
)
