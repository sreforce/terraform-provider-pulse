package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/sreforce/terraform-provider-pulse/internal/client"
)

type TeamResource struct{ client client.CatalogAPI }

type teamResourceModel struct {
	ID               types.String `tfsdk:"id"`
	Name             types.String `tfsdk:"name"`
	SettingsPriority types.Int64  `tfsdk:"settings_priority"`
	Revision         types.Int64  `tfsdk:"revision"`
}

func NewTeamResource() resource.Resource { return &TeamResource{} }

func (r *TeamResource) Metadata(_ context.Context, request resource.MetadataRequest, response *resource.MetadataResponse) {
	response.TypeName = request.ProviderTypeName + "_team"
}

func (r *TeamResource) Schema(_ context.Context, _ resource.SchemaRequest, response *resource.SchemaResponse) {
	response.Schema = schema.Schema{
		MarkdownDescription: "Manages one organization-scoped Pulse team without managing users or membership. Deletion is rejected while the team is referenced.",
		Attributes: map[string]schema.Attribute{
			"id":                schema.StringAttribute{Computed: true, MarkdownDescription: "Pulse team UUID."},
			"name":              catalogRequiredString("Organization-unique team name."),
			"settings_priority": schema.Int64Attribute{Required: true, MarkdownDescription: "Priority used when resolving team-scoped settings."},
			"revision":          schema.Int64Attribute{Computed: true, MarkdownDescription: "Configuration revision used for optimistic concurrency."},
		},
	}
}

func (r *TeamResource) Configure(_ context.Context, request resource.ConfigureRequest, response *resource.ConfigureResponse) {
	r.client = configureCatalogResource(request.ProviderData, &response.Diagnostics, "pulse_team")
}

func (r *TeamResource) Create(ctx context.Context, request resource.CreateRequest, response *resource.CreateResponse) {
	if !requireCatalogClient(r.client, &response.Diagnostics, "pulse_team") {
		return
	}
	var plan teamResourceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	if response.Diagnostics.HasError() {
		return
	}
	item, err := r.client.CreateTeam(ctx, teamWrite(plan), client.MutationOptions{})
	if err != nil {
		addCatalogMutationError(&response.Diagnostics, "create", "team", err)
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, teamState(item))...)
}

func (r *TeamResource) Read(ctx context.Context, request resource.ReadRequest, response *resource.ReadResponse) {
	if !requireCatalogClient(r.client, &response.Diagnostics, "pulse_team") {
		return
	}
	var state teamResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	item, err := r.client.GetTeam(ctx, state.ID.ValueString())
	if client.IsErrorCode(err, client.ErrorCodeNotFound) {
		response.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		addCatalogMutationError(&response.Diagnostics, "read", "team", err)
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, teamState(item))...)
}

func (r *TeamResource) Update(ctx context.Context, request resource.UpdateRequest, response *resource.UpdateResponse) {
	if !requireCatalogClient(r.client, &response.Diagnostics, "pulse_team") {
		return
	}
	var plan, state teamResourceModel
	response.Diagnostics.Append(request.Plan.Get(ctx, &plan)...)
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	item, err := r.client.UpdateTeam(ctx, state.ID.ValueString(), teamWrite(plan), client.MutationOptions{Revision: state.Revision.ValueInt64()})
	if err != nil {
		addCatalogMutationError(&response.Diagnostics, "update", "team", err)
		return
	}
	response.Diagnostics.Append(response.State.Set(ctx, teamState(item))...)
}

func (r *TeamResource) Delete(ctx context.Context, request resource.DeleteRequest, response *resource.DeleteResponse) {
	if !requireCatalogClient(r.client, &response.Diagnostics, "pulse_team") {
		return
	}
	var state teamResourceModel
	response.Diagnostics.Append(request.State.Get(ctx, &state)...)
	if response.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteTeam(ctx, state.ID.ValueString(), client.MutationOptions{Revision: state.Revision.ValueInt64()})
	if err != nil && !client.IsErrorCode(err, client.ErrorCodeNotFound) {
		addCatalogMutationError(&response.Diagnostics, "delete", "team", err)
	}
}

func (r *TeamResource) ImportState(ctx context.Context, request resource.ImportStateRequest, response *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), request, response)
}

func teamWrite(model teamResourceModel) client.TeamWriteRequest {
	return client.TeamWriteRequest{Name: model.Name.ValueString(), SettingsPriority: model.SettingsPriority.ValueInt64()}
}

func teamState(item client.Team) teamResourceModel {
	return teamResourceModel{ID: types.StringValue(item.ID), Name: types.StringValue(item.Name), SettingsPriority: types.Int64Value(item.SettingsPriority), Revision: types.Int64Value(item.Revision)}
}

var (
	_ resource.Resource                = (*TeamResource)(nil)
	_ resource.ResourceWithConfigure   = (*TeamResource)(nil)
	_ resource.ResourceWithImportState = (*TeamResource)(nil)
)
