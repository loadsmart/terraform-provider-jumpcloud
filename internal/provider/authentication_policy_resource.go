package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/loadsmart/terraform-provider-jumpcloud/internal/client"
)

var (
	_ resource.ResourceWithConfigure      = &authenticationPolicyResource{}
	_ resource.ResourceWithImportState    = &authenticationPolicyResource{}
	_ resource.ResourceWithValidateConfig = &authenticationPolicyResource{}
)

var (
	authnPolicyTypes   = []string{"user_portal", "application", "ldap", "admin_portal"}
	authnPolicyActions = []string{"allow", "deny"}
)

func NewAuthenticationPolicyResource() resource.Resource { return &authenticationPolicyResource{} }

type authenticationPolicyResource struct{ client *client.Client }

type authenticationPolicyModel struct {
	ID                     types.String         `tfsdk:"id"`
	Name                   types.String         `tfsdk:"name"`
	Description            types.String         `tfsdk:"description"`
	Type                   types.String         `tfsdk:"type"`
	Disabled               types.Bool           `tfsdk:"disabled"`
	MonitorOnly            types.Bool           `tfsdk:"monitor_only"`
	Action                 types.String         `tfsdk:"action"`
	MFARequired            types.Bool           `tfsdk:"mfa_required"`
	AllUsers               types.Bool           `tfsdk:"all_users"`
	UserGroupIDs           types.Set            `tfsdk:"user_group_ids"`
	ExcludedUserGroupIDs   types.Set            `tfsdk:"excluded_user_group_ids"`
	ApplicationIDs         types.Set            `tfsdk:"application_ids"`
	ExcludedApplicationIDs types.Set            `tfsdk:"excluded_application_ids"`
	Conditions             jsontypes.Normalized `tfsdk:"conditions"`
}

func (r *authenticationPolicyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_authentication_policy"
}

func (r *authenticationPolicyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	falseDefault := booldefault.StaticBool(false)
	idSet := func(desc string) schema.SetAttribute {
		return schema.SetAttribute{
			MarkdownDescription: desc, Optional: true, Computed: true, ElementType: types.StringType,
			Default: setdefault.StaticValue(types.SetValueMust(types.StringType, []attr.Value{})),
		}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a JumpCloud authentication (conditional access) policy, which allows or denies " +
			"sign-ins, and can require MFA, for the targeted users when its conditions match. " +
			"Settings the provider does not manage, such as the allowed MFA factors and custom error messages, " +
			"keep their values from the admin console.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Policy ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{MarkdownDescription: "Policy name.", Required: true},
			"description": schema.StringAttribute{
				MarkdownDescription: "Policy description.", Optional: true, Computed: true, Default: stringdefault.StaticString(""),
			},
			"type": schema.StringAttribute{
				MarkdownDescription: "What the policy protects: `user_portal`, `application`, `ldap`, or `admin_portal`. Changing it replaces the policy.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"disabled": schema.BoolAttribute{
				MarkdownDescription: "Whether the policy is off.", Optional: true, Computed: true, Default: falseDefault,
			},
			"monitor_only": schema.BoolAttribute{
				MarkdownDescription: "Whether JumpCloud only logs what the policy would do instead of enforcing it.",
				Optional:            true, Computed: true, Default: falseDefault,
			},
			"action": schema.StringAttribute{
				MarkdownDescription: "What happens when the policy matches: `allow` or `deny`.",
				Required:            true,
			},
			"mfa_required": schema.BoolAttribute{
				MarkdownDescription: "Whether an `allow` policy requires MFA. Must be `false` for `deny` policies.",
				Optional:            true, Computed: true, Default: falseDefault,
			},
			"all_users": schema.BoolAttribute{
				MarkdownDescription: "Whether the policy applies to every user. Otherwise it applies to the members of `user_group_ids`.",
				Optional:            true, Computed: true, Default: falseDefault,
			},
			"user_group_ids":          idSet("IDs of user groups whose members the policy applies to."),
			"excluded_user_group_ids": idSet("IDs of user groups whose members the policy never applies to."),
			"application_ids": idSet("For `application` policies, IDs of the SSO applications the policy protects. " +
				"Empty protects every application."),
			"excluded_application_ids": idSet("For `application` policies that protect every application, IDs of applications to leave out."),
			"conditions": schema.StringAttribute{
				MarkdownDescription: "When the policy applies, as JSON in JumpCloud's condition language, built with `jsonencode`. " +
					"Conditions include `ipAddressIn` (a list of `jumpcloud_ip_list` IDs), `locationIn` (`{ countries = [\"US\"] }`), " +
					"`deviceManaged`, and `deviceEncrypted`, combined with `all`, `any`, and `not`, " +
					"for example `jsonencode({ not = { ipAddressIn = [jumpcloud_ip_list.office.id] } })`. Omit to apply always.",
				Optional:   true,
				CustomType: jsontypes.NormalizedType{},
			},
		},
	}
}

func (r *authenticationPolicyResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config authenticationPolicyModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	oneOf := func(attr string, v types.String, allowed []string) {
		if !v.IsNull() && !v.IsUnknown() && !slices.Contains(allowed, v.ValueString()) {
			resp.Diagnostics.AddAttributeError(path.Root(attr), "Invalid value", fmt.Sprintf("%s must be one of %q, got %q.", attr, allowed, v.ValueString()))
		}
	}
	oneOf("type", config.Type, authnPolicyTypes)
	oneOf("action", config.Action, authnPolicyActions)
	if config.Action.ValueString() == "deny" && config.MFARequired.ValueBool() {
		resp.Diagnostics.AddAttributeError(path.Root("mfa_required"), "MFA on a deny policy", "Only allow policies can require MFA.")
	}
	if !config.Type.IsUnknown() && config.Type.ValueString() != "application" {
		for attr, v := range map[string]types.Set{"application_ids": config.ApplicationIDs, "excluded_application_ids": config.ExcludedApplicationIDs} {
			if !v.IsNull() && !v.IsUnknown() && len(v.Elements()) > 0 {
				resp.Diagnostics.AddAttributeError(path.Root(attr), "Applications on a non-application policy",
					fmt.Sprintf("%s is only valid when type is application.", attr))
			}
		}
	}
}

func (r *authenticationPolicyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerClient(req.ProviderData, &resp.Diagnostics)
}

func (r *authenticationPolicyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan authenticationPolicyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p := plan.policy(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	created, err := r.client.CreateAuthnPolicy(ctx, p)
	if err != nil {
		resp.Diagnostics.AddError("Error creating JumpCloud authentication policy", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, newAuthenticationPolicyModel(ctx, *created, &resp.Diagnostics))...)
}

func (r *authenticationPolicyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state authenticationPolicyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p, err := r.client.GetAuthnPolicy(ctx, state.ID.ValueString())
	if errors.Is(err, client.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading JumpCloud authentication policy", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, newAuthenticationPolicyModel(ctx, *p, &resp.Diagnostics))...)
}

func (r *authenticationPolicyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan authenticationPolicyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	p := plan.policy(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	updated, err := r.client.UpdateAuthnPolicy(ctx, plan.ID.ValueString(), p)
	if err != nil {
		resp.Diagnostics.AddError("Error updating JumpCloud authentication policy", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, newAuthenticationPolicyModel(ctx, *updated, &resp.Diagnostics))...)
}

func (r *authenticationPolicyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state authenticationPolicyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteAuthnPolicy(ctx, state.ID.ValueString()); err != nil && !errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Error deleting JumpCloud authentication policy", err.Error())
	}
}

func (r *authenticationPolicyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (m authenticationPolicyModel) policy(ctx context.Context, diags *diag.Diagnostics) client.AuthnPolicy {
	p := client.AuthnPolicy{
		Name: m.Name.ValueString(), Description: m.Description.ValueString(), Type: m.Type.ValueString(),
		Disabled: m.Disabled.ValueBool(), MonitorOnly: m.MonitorOnly.ValueBool(),
		Action: m.Action.ValueString(), MFARequired: m.MFARequired.ValueBool(), AllUsers: m.AllUsers.ValueBool(),
		UserGroupIDs:           setToStrings(ctx, m.UserGroupIDs, diags),
		ExcludedUserGroupIDs:   setToStrings(ctx, m.ExcludedUserGroupIDs, diags),
		ApplicationIDs:         setToStrings(ctx, m.ApplicationIDs, diags),
		ExcludedApplicationIDs: setToStrings(ctx, m.ExcludedApplicationIDs, diags),
	}
	if !m.Conditions.IsNull() {
		p.Conditions = json.RawMessage(m.Conditions.ValueString())
	}
	slices.Sort(p.ApplicationIDs) // deterministic request bodies
	return p
}

func newAuthenticationPolicyModel(ctx context.Context, p client.AuthnPolicy, diags *diag.Diagnostics) authenticationPolicyModel {
	m := authenticationPolicyModel{
		ID: types.StringValue(p.ID), Name: types.StringValue(p.Name), Description: types.StringValue(p.Description),
		Type: types.StringValue(p.Type), Disabled: types.BoolValue(p.Disabled), MonitorOnly: types.BoolValue(p.MonitorOnly),
		Action: types.StringValue(p.Action), MFARequired: types.BoolValue(p.MFARequired), AllUsers: types.BoolValue(p.AllUsers),
		UserGroupIDs:           stringSet(ctx, p.UserGroupIDs, diags),
		ExcludedUserGroupIDs:   stringSet(ctx, p.ExcludedUserGroupIDs, diags),
		ApplicationIDs:         stringSet(ctx, p.ApplicationIDs, diags),
		ExcludedApplicationIDs: stringSet(ctx, p.ExcludedApplicationIDs, diags),
		Conditions:             jsontypes.NewNormalizedNull(),
	}
	if p.Conditions != nil {
		m.Conditions = jsontypes.NewNormalizedValue(string(p.Conditions))
	}
	return m
}
