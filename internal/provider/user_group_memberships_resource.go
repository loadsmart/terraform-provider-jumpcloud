package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"

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
	_ resource.ResourceWithConfigure   = &userGroupMembershipsResource{}
	_ resource.ResourceWithImportState = &userGroupMembershipsResource{}
)

func NewUserGroupMembershipsResource() resource.Resource { return &userGroupMembershipsResource{} }

type userGroupMembershipsResource struct{ client *client.Client }

type userGroupMembershipsModel struct {
	ID       types.String `tfsdk:"id"`
	UserID   types.String `tfsdk:"user_id"`
	GroupIDs types.Set    `tfsdk:"group_ids"`
}

func (r *userGroupMembershipsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user_group_memberships"
}

func (r *userGroupMembershipsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a user's direct membership in a set of JumpCloud user groups. " +
			"Memberships in groups not listed in `group_ids` are left alone, so other configurations and the admin console " +
			"can add the same user to other groups. Use one resource per user. Dynamic groups cannot be listed: " +
			"use `membership_rule.include_user_ids` on `jumpcloud_user_group` instead.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Same as `user_id`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"user_id": schema.StringAttribute{
				MarkdownDescription: "ID of the JumpCloud user.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"group_ids": schema.SetAttribute{
				MarkdownDescription: "IDs of the user groups the user should belong to.",
				Required:            true,
				ElementType:         types.StringType,
			},
		},
	}
}

func (r *userGroupMembershipsResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerClient(req.ProviderData, &resp.Diagnostics)
}

func (r *userGroupMembershipsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan userGroupMembershipsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	groups := setToStrings(ctx, plan.GroupIDs, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	notAdded, _ := r.change(ctx, plan.UserID.ValueString(), groups, nil, &resp.Diagnostics)
	plan.ID = plan.UserID
	plan.GroupIDs = stringSet(ctx, without(groups, notAdded), &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *userGroupMembershipsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state userGroupMembershipsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	userID := state.UserID.ValueString()
	current, err := r.client.UserGroupIDs(ctx, userID)
	if err != nil {
		resp.Diagnostics.AddError("Error reading JumpCloud user group memberships", err.Error())
		return
	}
	if len(current) == 0 {
		// memberof answers 200 with no groups for a user that does not exist.
		_, err := r.client.GetUser(ctx, userID)
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		if err != nil {
			resp.Diagnostics.AddError("Error reading JumpCloud user", err.Error())
			return
		}
	}

	var keep []string
	if state.GroupIDs.IsNull() {
		// After import nothing is managed yet, so adopt every direct membership of a
		// static group. Read never stores null, so null always means "just imported".
		for _, groupID := range current {
			g, err := r.client.GetUserGroup(ctx, groupID)
			if err != nil && !errors.Is(err, client.ErrNotFound) {
				resp.Diagnostics.AddError("Error reading JumpCloud user group "+groupID, err.Error())
				return
			}
			if err == nil && !g.Dynamic() {
				keep = append(keep, groupID)
			}
		}
	} else {
		managed := setToStrings(ctx, state.GroupIDs, &resp.Diagnostics)
		keep = slices.DeleteFunc(slices.Clone(current), func(id string) bool { return !slices.Contains(managed, id) })
	}
	state.GroupIDs = stringSet(ctx, keep, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *userGroupMembershipsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state userGroupMembershipsModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	want := setToStrings(ctx, plan.GroupIDs, &resp.Diagnostics)
	have := setToStrings(ctx, state.GroupIDs, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	// State records what JumpCloud has: failed adds are left out and failed removes stay
	// in, so the next apply retries both.
	notAdded, notRemoved := r.change(ctx, plan.UserID.ValueString(), without(want, have), without(have, want), &resp.Diagnostics)
	plan.GroupIDs = stringSet(ctx, append(without(want, notAdded), notRemoved...), &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *userGroupMembershipsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state userGroupMembershipsModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	groups := setToStrings(ctx, state.GroupIDs, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	r.change(ctx, state.UserID.ValueString(), nil, groups, &resp.Diagnostics)
}

func (r *userGroupMembershipsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("user_id"), req.ID)...)
}

// change applies membership changes and returns the groups it failed to add or remove.
// Adding an existing membership (409) or removing a missing one (404) already matches
// the goal, so neither is an error.
func (r *userGroupMembershipsResource) change(ctx context.Context, userID string, add, remove []string, diags *diag.Diagnostics) (notAdded, notRemoved []string) {
	for _, groupID := range add {
		// JumpCloud accepts adding a user to a dynamic group but ignores it.
		if g, err := r.client.GetUserGroup(ctx, groupID); err == nil && g.Dynamic() {
			diags.AddError("Cannot add user to a dynamic JumpCloud user group", fmt.Sprintf(
				"User group %s is dynamic, so its membership rule decides its members. "+
					"Add user %s to membership_rule.include_user_ids of the group instead.", groupID, userID))
			notAdded = append(notAdded, groupID)
			continue
		} else if err != nil && !errors.Is(err, client.ErrNotFound) {
			diags.AddError("Error reading JumpCloud user group "+groupID, err.Error())
			notAdded = append(notAdded, groupID)
			continue
		}

		err := r.client.AddUserToGroup(ctx, groupID, userID)
		switch {
		case err == nil, isStatus(err, http.StatusConflict):
		case errors.Is(err, client.ErrNotFound), isUserGroupNotFound(err):
			diags.AddError("Error adding user to JumpCloud user group", fmt.Sprintf("User %s or user group %s does not exist.", userID, groupID))
			notAdded = append(notAdded, groupID)
		default:
			diags.AddError("Error adding user to JumpCloud user group "+groupID, err.Error())
			notAdded = append(notAdded, groupID)
		}
	}
	for _, groupID := range remove {
		if err := r.client.RemoveUserFromGroup(ctx, groupID, userID); err != nil && !errors.Is(err, client.ErrNotFound) && !isUserGroupNotFound(err) {
			diags.AddError("Error removing user from JumpCloud user group "+groupID, err.Error())
			notRemoved = append(notRemoved, groupID)
		}
	}
	return notAdded, notRemoved
}
