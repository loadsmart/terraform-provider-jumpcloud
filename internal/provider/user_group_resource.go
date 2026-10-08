package provider

import (
	"context"
	"errors"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/loadsmart/terraform-provider-jumpcloud/internal/client"
)

var (
	_ resource.ResourceWithConfigure   = &userGroupResource{}
	_ resource.ResourceWithImportState = &userGroupResource{}
)

func NewUserGroupResource() resource.Resource { return &userGroupResource{} }

type userGroupResource struct{ client *client.Client }

func (r *userGroupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user_group"
}

func (r *userGroupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a JumpCloud user group. Only the name and description are managed; settings made elsewhere, such as LDAP attributes or a dynamic membership query, are kept on update.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "User group ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Group name.",
				Required:            true,
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Group description.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
			},
		},
	}
}

func (r *userGroupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerClient(req.ProviderData, &resp.Diagnostics)
}

func (r *userGroupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan userGroupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	g, err := r.client.CreateUserGroup(ctx, client.UserGroup{Name: plan.Name.ValueString(), Description: plan.Description.ValueString()})
	if err != nil {
		resp.Diagnostics.AddError("Error creating JumpCloud user group", err.Error())
		return
	}
	plan.ID = types.StringValue(g.ID)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *userGroupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state userGroupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	g, err := r.client.GetUserGroup(ctx, state.ID.ValueString())
	if errors.Is(err, client.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading JumpCloud user group", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, newUserGroupModel(*g))...)
}

func (r *userGroupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan userGroupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.UpdateUserGroup(ctx, plan.ID.ValueString(), client.UserGroup{Name: plan.Name.ValueString(), Description: plan.Description.ValueString()})
	if err != nil {
		resp.Diagnostics.AddError("Error updating JumpCloud user group", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *userGroupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state userGroupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.DeleteUserGroup(ctx, state.ID.ValueString()); err != nil && !errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Error deleting JumpCloud user group", err.Error())
	}
}

func (r *userGroupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
