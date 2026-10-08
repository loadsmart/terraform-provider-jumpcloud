package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/loadsmart/terraform-provider-jumpcloud/internal/client"
)

var (
	_ resource.ResourceWithConfigure   = &applicationAssociationResource{}
	_ resource.ResourceWithImportState = &applicationAssociationResource{}
)

func NewApplicationAssociationResource() resource.Resource { return &applicationAssociationResource{} }

type applicationAssociationResource struct{ client *client.Client }

type applicationAssociationModel struct {
	ID            types.String `tfsdk:"id"`
	ApplicationID types.String `tfsdk:"application_id"`
	Type          types.String `tfsdk:"type"`
	TargetID      types.String `tfsdk:"target_id"`
}

func (r *applicationAssociationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_application_association"
}

func (r *applicationAssociationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Gives a JumpCloud user group or user access to an application. Use one resource per group or user.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "`<application_id>/<type>/<target_id>`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"application_id": schema.StringAttribute{
				MarkdownDescription: "ID of the application.",
				Required:            true,
				PlanModifiers:       replace,
			},
			"type": schema.StringAttribute{
				MarkdownDescription: "What `target_id` refers to: `user_group` or `user`.",
				Required:            true,
				PlanModifiers:       replace,
			},
			"target_id": schema.StringAttribute{
				MarkdownDescription: "ID of the user group or user.",
				Required:            true,
				PlanModifiers:       replace,
			},
		},
	}
}

func (r *applicationAssociationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerClient(req.ProviderData, &resp.Diagnostics)
}

func (r *applicationAssociationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan applicationAssociationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// An existing association (409) already matches the goal.
	err := r.client.AddApplicationAssociation(ctx, plan.ApplicationID.ValueString(), plan.Type.ValueString(), plan.TargetID.ValueString())
	switch {
	case err == nil, isStatus(err, http.StatusConflict):
	case errors.Is(err, client.ErrNotFound):
		resp.Diagnostics.AddError("Error creating JumpCloud application association", fmt.Sprintf(
			"Application %s or %s %s does not exist.", plan.ApplicationID.ValueString(), plan.Type.ValueString(), plan.TargetID.ValueString()))
		return
	default:
		resp.Diagnostics.AddError("Error creating JumpCloud application association", err.Error())
		return
	}
	plan.ID = types.StringValue(strings.Join([]string{plan.ApplicationID.ValueString(), plan.Type.ValueString(), plan.TargetID.ValueString()}, "/"))
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *applicationAssociationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state applicationAssociationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	ids, err := r.client.ApplicationAssociationIDs(ctx, state.ApplicationID.ValueString(), state.Type.ValueString())
	if errors.Is(err, client.ErrNotFound) || (err == nil && !slices.Contains(ids, state.TargetID.ValueString())) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading JumpCloud application associations", err.Error())
	}
}

func (r *applicationAssociationResource) Update(context.Context, resource.UpdateRequest, *resource.UpdateResponse) {
	// Every argument forces replacement, so Terraform never calls Update.
}

func (r *applicationAssociationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state applicationAssociationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.RemoveApplicationAssociation(ctx, state.ApplicationID.ValueString(), state.Type.ValueString(), state.TargetID.ValueString())
	if err != nil && !errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Error deleting JumpCloud application association", err.Error())
	}
}

func (r *applicationAssociationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.Split(req.ID, "/")
	if len(parts) != 3 || slices.Contains(parts, "") {
		resp.Diagnostics.AddError("Invalid import ID", fmt.Sprintf("Expected <application_id>/<type>/<target_id>, got %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("application_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("type"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("target_id"), parts[2])...)
}
