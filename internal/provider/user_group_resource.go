package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"github.com/loadsmart/terraform-provider-jumpcloud/internal/client"
)

var (
	_ resource.ResourceWithConfigure      = &userGroupResource{}
	_ resource.ResourceWithImportState    = &userGroupResource{}
	_ resource.ResourceWithValidateConfig = &userGroupResource{}
)

func NewUserGroupResource() resource.Resource { return &userGroupResource{} }

type userGroupResource struct{ client *client.Client }

type userGroupResourceModel struct {
	ID               types.String         `tfsdk:"id"`
	Name             types.String         `tfsdk:"name"`
	Description      types.String         `tfsdk:"description"`
	Email            types.String         `tfsdk:"email"`
	MembershipRule   *membershipRuleModel `tfsdk:"membership_rule"`
	Sudo             types.Object         `tfsdk:"sudo"`
	RadiusReply      types.List           `tfsdk:"radius_reply"`
	SambaEnabled     types.Bool           `tfsdk:"samba_enabled"`
	LDAPGroups       types.List           `tfsdk:"ldap_groups"`
	POSIXGroups      types.List           `tfsdk:"posix_groups"`
	MembershipMethod types.String         `tfsdk:"membership_method"`
	RuleErrors       types.List           `tfsdk:"rule_errors"`
}

type membershipRuleModel struct {
	Query             jsontypes.Normalized `tfsdk:"query"`
	ReviewRequired    types.Bool           `tfsdk:"review_required"`
	NotifySuggestions types.Bool           `tfsdk:"notify_suggestions"`
	IncludeUserIDs    types.Set            `tfsdk:"include_user_ids"`
	ExcludeUserIDs    types.Set            `tfsdk:"exclude_user_ids"`
}

type sudoModel struct {
	Enabled         types.Bool `tfsdk:"enabled"`
	WithoutPassword types.Bool `tfsdk:"without_password"`
}

type radiusReplyModel struct {
	Name  types.String `tfsdk:"name"`
	Value types.String `tfsdk:"value"`
}

type posixGroupModel struct {
	ID   types.Int64  `tfsdk:"id"`
	Name types.String `tfsdk:"name"`
}

var (
	sudoAttrTypes        = map[string]attr.Type{"enabled": types.BoolType, "without_password": types.BoolType}
	radiusReplyAttrTypes = map[string]attr.Type{"name": types.StringType, "value": types.StringType}
	posixGroupAttrTypes  = map[string]attr.Type{"id": types.Int64Type, "name": types.StringType}
)

func (r *userGroupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user_group"
}

func (r *userGroupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	emptySet := setdefault.StaticValue(types.SetValueMust(types.StringType, []attr.Value{}))
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a JumpCloud user group. A group is static, with members added directly " +
			"(see `jumpcloud_user_group_members` and `jumpcloud_user_group_memberships`), or dynamic, with members decided by `membership_rule`. " +
			"Group attributes left out of the configuration keep the values set elsewhere, such as in the admin console.",
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
			"email": schema.StringAttribute{
				MarkdownDescription: "Group email address. Kept as is when not set; `\"\"` clears it.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"membership_rule": schema.SingleNestedAttribute{
				MarkdownDescription: "Makes the group dynamic: JumpCloud adds and removes members by this rule. " +
					"Removing it makes the group static and keeps its current members as direct members. " +
					"Every exemption on the group is managed through `include_user_ids` and `exclude_user_ids`; exemptions added elsewhere are removed.",
				Optional: true,
				Attributes: map[string]schema.Attribute{
					"query": schema.StringAttribute{
						MarkdownDescription: "Rule as JSON in JumpCloud's v2 query format, built with `jsonencode`, for example " +
							"`jsonencode({ filters = [{ field = \"user.user_department\", operation = \"equals\", value = \"Engineering\" }] })`. " +
							"Filters are combined with AND. Fields include `user.email`, `user.username`, `user.user_department`, `user.user_job_title`, " +
							"`user.user_location`, `user.user_company`, `user.employee_id`, and `user.user_state`. " +
							"JumpCloud does not validate fields, so preview a rule with `POST /api/v2/search/query` before applying it.",
						Required:   true,
						CustomType: jsontypes.NormalizedType{},
					},
					"review_required": schema.BoolAttribute{
						MarkdownDescription: "Whether an admin must approve the rule's suggested changes in the admin console instead of JumpCloud applying them.",
						Optional:            true,
						Computed:            true,
						Default:             booldefault.StaticBool(false),
					},
					"notify_suggestions": schema.BoolAttribute{
						MarkdownDescription: "Whether JumpCloud emails admins about suggested membership changes.",
						Optional:            true,
						Computed:            true,
						Default:             booldefault.StaticBool(false),
					},
					"include_user_ids": schema.SetAttribute{
						MarkdownDescription: "IDs of users who are members whether or not they match the rule. If adding one fails, it shows under `exclude_user_ids` until an apply succeeds.",
						Optional:            true,
						Computed:            true,
						ElementType:         types.StringType,
						Default:             emptySet,
					},
					"exclude_user_ids": schema.SetAttribute{
						MarkdownDescription: "IDs of users who are not members whether or not they match the rule.",
						Optional:            true,
						Computed:            true,
						ElementType:         types.StringType,
						Default:             emptySet,
					},
				},
			},
			"sudo": schema.SingleNestedAttribute{
				MarkdownDescription: "Sudo access for members on devices associated with the group. Kept as is when not set; " +
					"`{ enabled = false, without_password = false }` removes it.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.Object{objectplanmodifier.UseStateForUnknown()},
				Attributes: map[string]schema.Attribute{
					"enabled":          schema.BoolAttribute{MarkdownDescription: "Whether members get sudo. Must be set.", Optional: true, Computed: true},
					"without_password": schema.BoolAttribute{MarkdownDescription: "Whether sudo works without a password. Must be set.", Optional: true, Computed: true},
				},
			},
			"radius_reply": schema.ListNestedAttribute{
				MarkdownDescription: "RADIUS reply attributes sent for members. Kept as is when not set; `[]` removes them.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.List{listplanmodifier.UseStateForUnknown()},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name":  schema.StringAttribute{MarkdownDescription: "Attribute name. Must be set.", Optional: true, Computed: true},
						"value": schema.StringAttribute{MarkdownDescription: "Attribute value. Must be set.", Optional: true, Computed: true},
					},
				},
			},
			"samba_enabled": schema.BoolAttribute{
				MarkdownDescription: "Whether Samba authentication is enabled for the group in JumpCloud LDAP. Kept as is when not set.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"ldap_groups": schema.ListAttribute{
				MarkdownDescription: "LDAP group names. JumpCloud sets it to the group name on create and keeps it on rename. Kept as is when not set.",
				Optional:            true,
				Computed:            true,
				ElementType:         types.StringType,
				PlanModifiers:       []planmodifier.List{listplanmodifier.UseStateForUnknown()},
			},
			"posix_groups": schema.ListNestedAttribute{
				MarkdownDescription: "POSIX groups. JumpCloud does not allow changing or removing them once set, " +
					"so a plan that does fails. To get different POSIX groups, run `terraform taint` on the group so the next apply replaces it. Kept as is when not set.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown(), posixGroupsUnchanged{}},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":   schema.Int64Attribute{MarkdownDescription: "POSIX group ID (GID). Must be set.", Optional: true, Computed: true},
						"name": schema.StringAttribute{MarkdownDescription: "POSIX group name. Must be set.", Optional: true, Computed: true},
					},
				},
			},
			"membership_method": schema.StringAttribute{
				MarkdownDescription: "How JumpCloud decides members: `STATIC` or `NOTSET` for static groups, `DYNAMIC_AUTOMATED` or `DYNAMIC_REVIEW_REQUIRED` for dynamic ones.",
				Computed:            true,
			},
			"rule_errors": schema.ListAttribute{
				MarkdownDescription: "Problems JumpCloud found in the membership rule, such as `CYCLE` or `INVALID_GROUP_REFERENCE`.",
				Computed:            true,
				ElementType:         types.StringType,
			},
		},
	}
}

// posixGroupsUnchanged fails the plan when it changes POSIX groups that are already set,
// because JumpCloud rejects that on update with "existing attribute posixGroups may not be changed".
type posixGroupsUnchanged struct{}

func (posixGroupsUnchanged) Description(context.Context) string {
	return "Fails when POSIX groups that are already set would change."
}

func (m posixGroupsUnchanged) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (posixGroupsUnchanged) PlanModifyList(_ context.Context, req planmodifier.ListRequest, resp *planmodifier.ListResponse) {
	if req.StateValue.IsNull() || len(req.StateValue.Elements()) == 0 || req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if !req.ConfigValue.Equal(req.StateValue) {
		resp.Diagnostics.AddAttributeError(req.Path, "POSIX groups cannot be changed",
			"JumpCloud does not allow changing POSIX groups once set. If a new group is intended, run "+
				"terraform taint <resource address> so the next apply replaces it. Replacing a group drops its "+
				"application, device, and policy bindings.")
	}
}

func (r *userGroupResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	// Attributes are read one by one: a whole membership_rule can be unknown here, which
	// the model's struct pointer cannot hold.
	var sudo, ruleObj types.Object
	var radius, posix types.List
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("sudo"), &sudo)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("radius_reply"), &radius)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("posix_groups"), &posix)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("membership_rule"), &ruleObj)...)
	if resp.Diagnostics.HasError() {
		return
	}

	requireNested(path.Root("sudo"), sudo, &resp.Diagnostics)
	for name, list := range map[string]types.List{"radius_reply": radius, "posix_groups": posix} {
		for i, elem := range list.Elements() {
			if obj, ok := elem.(types.Object); ok {
				requireNested(path.Root(name).AtListIndex(i), obj, &resp.Diagnostics)
			}
		}
	}

	if ruleObj.IsNull() || ruleObj.IsUnknown() {
		return
	}
	var rule membershipRuleModel
	resp.Diagnostics.Append(ruleObj.As(ctx, &rule, basetypes.ObjectAsOptions{})...)
	if resp.Diagnostics.HasError() {
		return
	}
	include := knownStrings(rule.IncludeUserIDs)
	for _, id := range knownStrings(rule.ExcludeUserIDs) {
		if slices.Contains(include, id) {
			resp.Diagnostics.AddAttributeError(path.Root("membership_rule"), "User both included and excluded",
				fmt.Sprintf("User %s is in both include_user_ids and exclude_user_ids.", id))
		}
	}
}

// knownStrings returns the known elements of a set, which can hold unknown IDs (from
// resources not created yet) while the configuration is validated.
func knownStrings(s types.Set) []string {
	var out []string
	for _, v := range s.Elements() {
		if str, ok := v.(types.String); ok && !str.IsUnknown() && !str.IsNull() {
			out = append(out, str.ValueString())
		}
	}
	return out
}

// requireNested reports arguments left out of a configured object. They are declared
// Optional+Computed only because Terraform plans a perpetual diff for required arguments
// nested in an optional, computed attribute that is not configured.
func requireNested(p path.Path, obj types.Object, diags *diag.Diagnostics) {
	if obj.IsNull() || obj.IsUnknown() {
		return
	}
	for name, v := range obj.Attributes() {
		if v.IsNull() {
			diags.AddAttributeError(p.AtName(name), "Missing required argument", fmt.Sprintf("The argument %q is required.", name))
		}
	}
}

func (r *userGroupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerClient(req.ProviderData, &resp.Diagnostics)
}

func (r *userGroupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan, config userGroupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	change := plan.change(ctx, config, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	g, err := r.client.CreateUserGroup(ctx, change)
	if err != nil {
		resp.Diagnostics.AddError("Error creating JumpCloud user group", writeErrorDetail(err, change))
		return
	}
	// The group exists, so member failures are warnings: an error would taint it and the next
	// apply would recreate it. State must then match the plan; the next refresh corrects it.
	var memberDiags diag.Diagnostics
	members := r.applyExemptions(ctx, g.ID, plan.MembershipRule, &memberDiags)
	state := newUserGroupResourceModel(ctx, *g, members, &resp.Diagnostics)
	if len(memberDiags) > 0 && state.MembershipRule != nil {
		state.MembershipRule.IncludeUserIDs = plan.MembershipRule.IncludeUserIDs
		state.MembershipRule.ExcludeUserIDs = plan.MembershipRule.ExcludeUserIDs
	}
	for _, d := range memberDiags {
		resp.Diagnostics.AddWarning(d.Summary(), d.Detail()+" The group was created; the next apply retries this change.")
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *userGroupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state userGroupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := state.ID.ValueString()
	g, err := r.client.GetUserGroup(ctx, id)
	if errors.Is(err, client.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading JumpCloud user group", err.Error())
		return
	}
	// Exempt users split into included and excluded by membership. Checking each one costs a
	// request per exemption instead of paging through every member of a large group.
	var members []string
	if g.Dynamic() {
		for _, userID := range g.ExemptUserIDs() {
			groups, err := r.client.UserGroupIDs(ctx, userID)
			if err != nil {
				resp.Diagnostics.AddError("Error reading JumpCloud user group memberships of user "+userID, err.Error())
				return
			}
			if slices.Contains(groups, id) {
				members = append(members, userID)
			}
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, newUserGroupResourceModel(ctx, *g, members, &resp.Diagnostics))...)
}

func (r *userGroupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, config userGroupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	change := plan.change(ctx, config, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	g, err := r.client.UpdateUserGroup(ctx, plan.ID.ValueString(), change)
	if err != nil {
		resp.Diagnostics.AddError("Error updating JumpCloud user group", writeErrorDetail(err, change))
		return
	}
	members := r.applyExemptions(ctx, g.ID, plan.MembershipRule, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, newUserGroupResourceModel(ctx, *g, members, &resp.Diagnostics))...)
}

func (r *userGroupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state userGroupResourceModel
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

// writeErrorDetail explains a create or update failure. JumpCloud answers a bare 404 when an
// exemption names a user that does not exist.
func writeErrorDetail(err error, ch client.UserGroupChange) string {
	if errors.Is(err, client.ErrNotFound) && ch.Rule != nil && len(ch.Rule.ExemptUserIDs) > 0 {
		return "The group or a user in include_user_ids or exclude_user_ids does not exist."
	}
	return err.Error()
}

// applyExemptions makes included users members and excluded users non-members. An exemption
// alone only stops the rule from deciding for the user, so each needs a membership change.
// It returns the exempt users that are members afterwards: failed adds are left out and
// failed removes stay in, so state records what JumpCloud has.
func (r *userGroupResource) applyExemptions(ctx context.Context, groupID string, rule *membershipRuleModel, diags *diag.Diagnostics) []string {
	if rule == nil {
		return nil
	}
	var members []string
	for _, userID := range setToStrings(ctx, rule.IncludeUserIDs, diags) {
		err := r.client.AddUserToGroup(ctx, groupID, userID)
		switch {
		case err == nil, isStatus(err, http.StatusConflict):
			members = append(members, userID)
		case errors.Is(err, client.ErrNotFound):
			diags.AddError("Error adding user to JumpCloud user group", fmt.Sprintf("User %s does not exist.", userID))
		default:
			diags.AddError("Error adding user "+userID+" to JumpCloud user group", err.Error())
		}
	}
	for _, userID := range setToStrings(ctx, rule.ExcludeUserIDs, diags) {
		if err := r.client.RemoveUserFromGroup(ctx, groupID, userID); err != nil && !errors.Is(err, client.ErrNotFound) {
			diags.AddError("Error removing user "+userID+" from JumpCloud user group", err.Error())
			members = append(members, userID)
		}
	}
	return members
}

// change builds the API change from the plan. Attributes are only sent when configured,
// so values set elsewhere are kept.
func (m userGroupResourceModel) change(ctx context.Context, config userGroupResourceModel, diags *diag.Diagnostics) client.UserGroupChange {
	ch := client.UserGroupChange{
		Name:        m.Name.ValueString(),
		Description: m.Description.ValueString(),
		Attributes:  map[string]any{},
	}
	if !config.Email.IsNull() {
		ch.Email = m.Email.ValueStringPointer()
	}
	if rule := m.MembershipRule; rule != nil {
		include := setToStrings(ctx, rule.IncludeUserIDs, diags)
		exclude := setToStrings(ctx, rule.ExcludeUserIDs, diags)
		ch.Rule = &client.MemberRule{
			Query:          json.RawMessage(rule.Query.ValueString()),
			ReviewRequired: rule.ReviewRequired.ValueBool(),
			Notify:         rule.NotifySuggestions.ValueBool(),
			ExemptUserIDs:  append(include, exclude...),
		}
	}

	if !config.Sudo.IsNull() {
		var sudo sudoModel
		diags.Append(m.Sudo.As(ctx, &sudo, basetypes.ObjectAsOptions{})...)
		ch.Attributes["sudo"] = nil
		if sudo.Enabled.ValueBool() || sudo.WithoutPassword.ValueBool() {
			ch.Attributes["sudo"] = client.Sudo{Enabled: sudo.Enabled.ValueBool(), WithoutPassword: sudo.WithoutPassword.ValueBool()}
		}
	}
	if !config.RadiusReply.IsNull() {
		var replies []radiusReplyModel
		diags.Append(m.RadiusReply.ElementsAs(ctx, &replies, false)...)
		ch.Attributes["radius"] = nil
		if len(replies) > 0 {
			radius := client.Radius{}
			for _, reply := range replies {
				radius.Reply = append(radius.Reply, client.RadiusReply{Name: reply.Name.ValueString(), Value: reply.Value.ValueString()})
			}
			ch.Attributes["radius"] = radius
		}
	}
	if !config.SambaEnabled.IsNull() {
		ch.Attributes["sambaEnabled"] = nil
		if m.SambaEnabled.ValueBool() {
			ch.Attributes["sambaEnabled"] = true
		}
	}
	if !config.LDAPGroups.IsNull() {
		var names []string
		diags.Append(m.LDAPGroups.ElementsAs(ctx, &names, false)...)
		// An empty list is sent as is: leaving ldapGroups out makes JumpCloud set it to the group name.
		groups := []client.LDAPGroup{}
		for _, name := range names {
			groups = append(groups, client.LDAPGroup{Name: name})
		}
		ch.Attributes["ldapGroups"] = groups
	}
	if !config.POSIXGroups.IsNull() && len(m.POSIXGroups.Elements()) > 0 {
		var posix []posixGroupModel
		diags.Append(m.POSIXGroups.ElementsAs(ctx, &posix, false)...)
		groups := []client.POSIXGroup{}
		for _, p := range posix {
			groups = append(groups, client.POSIXGroup{ID: p.ID.ValueInt64(), Name: p.Name.ValueString()})
		}
		ch.Attributes["posixGroups"] = groups
	}
	return ch
}

// newUserGroupResourceModel builds state from a group. members lists the group's exempt
// users that are members: they are included, other exempt users are excluded.
func newUserGroupResourceModel(ctx context.Context, g client.UserGroup, members []string, diags *diag.Diagnostics) userGroupResourceModel {
	m := userGroupResourceModel{
		ID:               types.StringValue(g.ID),
		Name:             types.StringValue(g.Name),
		Description:      types.StringValue(g.Description),
		Email:            types.StringValue(g.Email),
		SambaEnabled:     types.BoolValue(g.Attributes.SambaEnabled),
		MembershipMethod: types.StringValue(g.MembershipMethod),
	}
	if g.Dynamic() {
		var include, exclude []string
		for _, id := range g.ExemptUserIDs() {
			if slices.Contains(members, id) {
				include = append(include, id)
			} else {
				exclude = append(exclude, id)
			}
		}
		m.MembershipRule = &membershipRuleModel{
			Query:             jsontypes.NewNormalizedValue(string(g.MemberQuery)),
			ReviewRequired:    types.BoolValue(g.MembershipMethod == client.MembershipDynamicReviewRequired),
			NotifySuggestions: types.BoolValue(g.MemberSuggestionsNotify),
			IncludeUserIDs:    stringSet(ctx, include, diags),
			ExcludeUserIDs:    stringSet(ctx, exclude, diags),
		}
	}

	var d diag.Diagnostics
	sudo := sudoModel{Enabled: types.BoolValue(false), WithoutPassword: types.BoolValue(false)}
	if s := g.Attributes.Sudo; s != nil {
		sudo = sudoModel{Enabled: types.BoolValue(s.Enabled), WithoutPassword: types.BoolValue(s.WithoutPassword)}
	}
	m.Sudo, d = types.ObjectValueFrom(ctx, sudoAttrTypes, sudo)
	diags.Append(d...)

	replies := []radiusReplyModel{}
	if g.Attributes.Radius != nil {
		for _, reply := range g.Attributes.Radius.Reply {
			replies = append(replies, radiusReplyModel{Name: types.StringValue(reply.Name), Value: types.StringValue(reply.Value)})
		}
	}
	m.RadiusReply, d = types.ListValueFrom(ctx, types.ObjectType{AttrTypes: radiusReplyAttrTypes}, replies)
	diags.Append(d...)

	ldap := []string{}
	for _, l := range g.Attributes.LDAPGroups {
		ldap = append(ldap, l.Name)
	}
	m.LDAPGroups, d = types.ListValueFrom(ctx, types.StringType, ldap)
	diags.Append(d...)

	posix := []posixGroupModel{}
	for _, p := range g.Attributes.POSIXGroups {
		posix = append(posix, posixGroupModel{ID: types.Int64Value(p.ID), Name: types.StringValue(p.Name)})
	}
	m.POSIXGroups, d = types.ListValueFrom(ctx, types.ObjectType{AttrTypes: posixGroupAttrTypes}, posix)
	diags.Append(d...)

	errorFlags := g.MemberQueryErrorFlags
	if errorFlags == nil {
		errorFlags = []string{}
	}
	m.RuleErrors, d = types.ListValueFrom(ctx, types.StringType, errorFlags)
	diags.Append(d...)
	return m
}
