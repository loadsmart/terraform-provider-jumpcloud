package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/loadsmart/terraform-provider-jumpcloud/internal/client"
)

var (
	_ resource.ResourceWithConfigure   = &userGroupMembersResource{}
	_ resource.ResourceWithImportState = &userGroupMembersResource{}
)

func NewUserGroupMembersResource() resource.Resource { return &userGroupMembersResource{} }

type userGroupMembersResource struct{ client *client.Client }

type userGroupMembersModel struct {
	ID      types.String `tfsdk:"id"`
	GroupID types.String `tfsdk:"group_id"`
	UserIDs types.Set    `tfsdk:"user_ids"`
}

func (r *userGroupMembersResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user_group_members"
}

func (r *userGroupMembersResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages every direct member of a static JumpCloud user group, like `okta_group_memberships` in the Okta provider. " +
			"It is authoritative: users not listed in `user_ids` are removed, including members added in the admin console. " +
			"Do not combine it with `jumpcloud_user_group_memberships` on the same group, or the two will keep undoing each other's changes. " +
			"Dynamic groups are not supported; use `membership_rule` on `jumpcloud_user_group` instead. " +
			"If the group becomes dynamic later, the resource keeps its last members and warns until you remove it.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Same as `group_id`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"group_id": schema.StringAttribute{
				MarkdownDescription: "ID of the static user group.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"user_ids": schema.SetAttribute{
				MarkdownDescription: "IDs of the users who should be the group's direct members. An empty set removes every member.",
				Required:            true,
				ElementType:         types.StringType,
			},
		},
	}
}

func (r *userGroupMembersResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerClient(req.ProviderData, &resp.Diagnostics)
}

func (r *userGroupMembersResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan userGroupMembersModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	want := setToStrings(ctx, plan.UserIDs, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	groupID := plan.GroupID.ValueString()
	if !r.checkStatic(ctx, groupID, &resp.Diagnostics) {
		return
	}
	current, err := r.client.UserGroupMemberIDs(ctx, groupID)
	if err != nil {
		resp.Diagnostics.AddError("Error reading JumpCloud user group members", err.Error())
		return
	}

	// Member failures are warnings: an error would taint the resource, and replacing it would
	// remove every member before adding them back. State must then match the plan; the next
	// refresh corrects it and the next apply retries.
	var memberDiags diag.Diagnostics
	r.change(ctx, groupID, without(want, current), without(current, want), &memberDiags)
	for _, d := range memberDiags {
		resp.Diagnostics.AddWarning(d.Summary(), d.Detail()+" The next apply retries this change.")
	}
	plan.ID = plan.GroupID
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *userGroupMembersResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state userGroupMembersModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	groupID := state.GroupID.ValueString()
	g, err := r.client.GetUserGroup(ctx, groupID)
	if errors.Is(err, client.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading JumpCloud user group", err.Error())
		return
	}
	if g.Dynamic() {
		// Its rule decides the members now, and Update refuses dynamic groups, so reading
		// them would plan a change no apply can make. Keep the last members instead.
		resp.Diagnostics.AddWarning("JumpCloud user group is dynamic", fmt.Sprintf(
			"User group %s now has a membership rule, so this resource no longer manages its members. "+
				"Remove this resource, and use membership_rule.include_user_ids and exclude_user_ids on the group instead.", groupID))
		return
	}
	members, err := r.client.UserGroupMemberIDs(ctx, groupID)
	if err != nil {
		resp.Diagnostics.AddError("Error reading JumpCloud user group members", err.Error())
		return
	}
	state.UserIDs = stringSet(ctx, members, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *userGroupMembersResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan userGroupMembersModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	want := setToStrings(ctx, plan.UserIDs, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	groupID := plan.GroupID.ValueString()
	if !r.checkStatic(ctx, groupID, &resp.Diagnostics) {
		return
	}
	// Compare with the live members, not state, so members added since the last refresh
	// are removed too.
	have, err := r.client.UserGroupMemberIDs(ctx, groupID)
	if err != nil {
		resp.Diagnostics.AddError("Error reading JumpCloud user group members", err.Error())
		return
	}

	// State records what JumpCloud has: failed adds are left out and failed removes stay
	// in, so the next apply retries both.
	notAdded, notRemoved := r.change(ctx, groupID, without(want, have), without(have, want), &resp.Diagnostics)
	plan.UserIDs = stringSet(ctx, append(without(want, notAdded), notRemoved...), &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *userGroupMembersResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state userGroupMembersModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	users := setToStrings(ctx, state.UserIDs, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	r.change(ctx, state.GroupID.ValueString(), nil, users, &resp.Diagnostics)
}

func (r *userGroupMembersResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("group_id"), req.ID)...)
}

// checkStatic reports whether the group exists and is static. JumpCloud accepts adding
// members to a dynamic group but ignores them, so that fails here with a clear error.
func (r *userGroupMembersResource) checkStatic(ctx context.Context, groupID string, diags *diag.Diagnostics) bool {
	g, err := r.client.GetUserGroup(ctx, groupID)
	switch {
	case errors.Is(err, client.ErrNotFound):
		diags.AddError("JumpCloud user group not found", fmt.Sprintf("User group %s does not exist.", groupID))
	case err != nil:
		diags.AddError("Error reading JumpCloud user group", err.Error())
	case g.Dynamic():
		diags.AddError("Cannot manage members of a dynamic JumpCloud user group", fmt.Sprintf(
			"User group %s is dynamic, so its membership rule decides its members. "+
				"Use membership_rule.include_user_ids and exclude_user_ids of the group instead.", groupID))
	default:
		return true
	}
	return false
}

// change applies membership changes and returns the users it failed to add or remove.
// Adding an existing member (409) or removing a missing one, or one of a deleted group,
// already matches the goal, so neither is an error.
func (r *userGroupMembersResource) change(ctx context.Context, groupID string, add, remove []string, diags *diag.Diagnostics) (notAdded, notRemoved []string) {
	for _, userID := range add {
		err := r.client.AddUserToGroup(ctx, groupID, userID)
		switch {
		case err == nil, isStatus(err, http.StatusConflict):
		case errors.Is(err, client.ErrNotFound):
			diags.AddError("Error adding user to JumpCloud user group", fmt.Sprintf("User %s does not exist.", userID))
			notAdded = append(notAdded, userID)
		case isUserGroupNotFound(err):
			diags.AddError("Error adding user to JumpCloud user group", fmt.Sprintf("User group %s does not exist.", groupID))
			notAdded = append(notAdded, userID)
		default:
			diags.AddError("Error adding user "+userID+" to JumpCloud user group", err.Error())
			notAdded = append(notAdded, userID)
		}
	}
	for _, userID := range remove {
		if err := r.client.RemoveUserFromGroup(ctx, groupID, userID); err != nil && !errors.Is(err, client.ErrNotFound) && !isUserGroupNotFound(err) {
			diags.AddError("Error removing user "+userID+" from JumpCloud user group", err.Error())
			notRemoved = append(notRemoved, userID)
		}
	}
	return notAdded, notRemoved
}
