package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/sreforce/terraform-provider-pulse/internal/client"
)

type TagResource struct{ client client.CatalogAPI }

type tagResourceModel struct {
	ID           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	Purpose      types.String `tfsdk:"purpose"`
	DisplayLabel types.String `tfsdk:"display_label"`
	DisplayOrder types.Int64  `tfsdk:"display_order"`
	Icon         types.String `tfsdk:"icon"`
	Revision     types.Int64  `tfsdk:"revision"`
}

func NewTagResource() resource.Resource { return &TagResource{} }

func (r *TagResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_tag"
}

func (r *TagResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Manages one organization-scoped Pulse tag. Purpose is immutable; deletion is rejected while any configuration or operational record references the tag.",
		Attributes: map[string]schema.Attribute{
			"id":   schema.StringAttribute{Computed: true, MarkdownDescription: "Pulse tag UUID."},
			"name": catalogRequiredString("Organization-unique name within the selected purpose."),
			"purpose": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Tag purpose: relevance or filter.",
				Validators:          []validator.String{tagPurposeValidator{}},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"display_label": schema.StringAttribute{Optional: true, MarkdownDescription: "Optional human-readable display label.", Validators: []validator.String{nonBlankStringValidator{}}},
			"display_order": schema.Int64Attribute{Required: true, MarkdownDescription: "Organization-defined display order."},
			"icon":          schema.StringAttribute{Optional: true, MarkdownDescription: "Optional icon identifier.", Validators: []validator.String{nonBlankStringValidator{}}},
			"revision":      schema.Int64Attribute{Computed: true, MarkdownDescription: "Configuration revision used for optimistic concurrency."},
		},
	}
}

func (r *TagResource) Configure(_ context.Context, request resource.ConfigureRequest, response *resource.ConfigureResponse) {
	r.client = configureCatalogResource(request.ProviderData, &response.Diagnostics, "pulse_tag")
}

func (r *TagResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	if !requireCatalogClient(r.client, &response.Diagnostics, "pulse_tag") {
		return
	}
	var plan tagResourceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	item, err := r.client.CreateTag(ctx, tagWrite(plan), client.MutationOptions{})
	if err != nil {
		addCatalogMutationError(&response.Diagnostics, "create", "tag", err)
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, tagState(item))...)
}

func (r *TagResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	if !requireCatalogClient(r.client, &response.Diagnostics, "pulse_tag") {
		return
	}
	var state tagResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	item, err := r.client.GetTag(ctx, state.ID.ValueString())
	if client.IsErrorCode(err, client.ErrorCodeNotFound) {
		response.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addCatalogMutationError(&response.Diagnostics, "read", "tag", err)
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, tagState(item))...)
}

func (r *TagResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	if !requireCatalogClient(r.client, &response.Diagnostics, "pulse_tag") {
		return
	}
	var plan, state tagResourceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	item, err := r.client.UpdateTag(ctx, state.ID.ValueString(), tagWrite(plan), client.MutationOptions{Revision: state.Revision.ValueInt64()})
	if err != nil {
		addCatalogMutationError(&response.Diagnostics, "update", "tag", err)
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, tagState(item))...)
}

func (r *TagResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	if !requireCatalogClient(r.client, &response.Diagnostics, "pulse_tag") {
		return
	}
	var state tagResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteTag(ctx, state.ID.ValueString(), client.MutationOptions{Revision: state.Revision.ValueInt64()})
	if err != nil && !client.IsErrorCode(err, client.ErrorCodeNotFound) {
		addCatalogMutationError(&response.Diagnostics, "delete", "tag", err)
	}
}

func (r *TagResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
}

func tagWrite(model tagResourceModel) client.TagWriteRequest {
	return client.TagWriteRequest{Name: model.Name.ValueString(), Purpose: model.Purpose.ValueString(), DisplayLabel: optionalString(model.DisplayLabel), DisplayOrder: model.DisplayOrder.ValueInt64(), Icon: optionalString(model.Icon)}
}

func optionalString(value types.String) *string {
	if value.IsNull() || value.IsUnknown() {
		return nil
	}
	result := value.ValueString()
	return &result
}

func tagState(item client.Tag) tagResourceModel {
	return tagResourceModel{ID: types.StringValue(item.ID), Name: types.StringValue(item.Name), Purpose: types.StringValue(item.Purpose), DisplayLabel: stringValueOrNull(item.DisplayLabel), DisplayOrder: types.Int64Value(item.DisplayOrder), Icon: stringValueOrNull(item.Icon), Revision: types.Int64Value(item.Revision)}
}

type tagPurposeValidator struct{}

func (tagPurposeValidator) Description(context.Context) string {
	return "value must be relevance or filter"
}
func (tagPurposeValidator) MarkdownDescription(context.Context) string {
	return "value must be `relevance` or `filter`"
}
func (tagPurposeValidator) ValidateString(_ context.Context, request validator.StringRequest, response *validator.StringResponse) {
	if request.ConfigValue.IsNull() || request.ConfigValue.IsUnknown() {
		return
	}
	value := request.ConfigValue.ValueString()
	if value != "relevance" && value != "filter" {
		response.Diagnostics.AddAttributeError(request.Path, "Invalid Pulse tag purpose", "Tag purpose must be relevance or filter.")
	}
}

var (
	_ resource.Resource                = (*TagResource)(nil)
	_ resource.ResourceWithConfigure   = (*TagResource)(nil)
	_ resource.ResourceWithImportState = (*TagResource)(nil)
	_ validator.String                 = tagPurposeValidator{}
)
