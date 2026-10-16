package provider

import (
	"context"
	"errors"
	"maps"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/loadsmart/terraform-provider-jumpcloud/internal/client"
)

var (
	_ resource.ResourceWithConfigure   = &oidcApplicationResource{}
	_ resource.ResourceWithImportState = &oidcApplicationResource{}
)

func NewOIDCApplicationResource() resource.Resource { return &oidcApplicationResource{} }

type oidcApplicationResource struct{ client *client.Client }

type oidcApplicationModel struct {
	ID                      types.String `tfsdk:"id"`
	DisplayLabel            types.String `tfsdk:"display_label"`
	ShowInPortal            types.Bool   `tfsdk:"show_in_portal"`
	RedirectURIs            types.Set    `tfsdk:"redirect_uris"`
	LoginURL                types.String `tfsdk:"login_url"`
	GrantTypes              types.Set    `tfsdk:"grant_types"`
	TokenEndpointAuthMethod types.String `tfsdk:"token_endpoint_auth_method"`
	Claims                  types.Map    `tfsdk:"claims"`
	StaticClaims            types.Map    `tfsdk:"static_claims"`
	AccessTokenFormat       types.String `tfsdk:"access_token_format"`
	Audiences               types.Set    `tfsdk:"audiences"`
	ClientID                types.String `tfsdk:"client_id"`
	ClientSecret            types.String `tfsdk:"client_secret"`
}

func (r *oidcApplicationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_oidc_application"
}

func (r *oidcApplicationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keepState := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a custom OpenID Connect (OIDC) application in JumpCloud. " +
			"Users sign in at `https://oauth.id.jumpcloud.com/` " +
			"(discovery document: `https://oauth.id.jumpcloud.com/.well-known/openid-configuration`). " +
			"Give users and groups access with `jumpcloud_application_association`.\n\n" +
			"~> JumpCloud returns the client secret only when the application is created, so `client_secret` " +
			"is empty after an import. This resource uses the `/api/v2/applications/{id}/sso` endpoint, " +
			"which JumpCloud's own SSO migration tool uses but the published API reference does not list.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Application ID.",
				Computed:            true,
				PlanModifiers:       keepState,
			},
			"display_label": schema.StringAttribute{
				MarkdownDescription: "Name shown in the admin console and the user portal.",
				Required:            true,
			},
			"show_in_portal": schema.BoolAttribute{
				MarkdownDescription: "Whether the application appears in users' JumpCloud portal. Hidden applications still accept sign-ins.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"redirect_uris": schema.SetAttribute{
				MarkdownDescription: "Redirect URIs the application may send users back to after sign-in.",
				Required:            true,
				ElementType:         types.StringType,
			},
			"login_url": schema.StringAttribute{
				MarkdownDescription: "URL that starts sign-in on the application. JumpCloud requires it and opens it from the user portal.",
				Required:            true,
			},
			"grant_types": schema.SetAttribute{
				MarkdownDescription: "Allowed grant types: `authorization_code` and `refresh_token`. JumpCloud supports no others.",
				Optional:            true,
				Computed:            true,
				ElementType:         types.StringType,
				Default: setdefault.StaticValue(types.SetValueMust(types.StringType, []attr.Value{
					types.StringValue("authorization_code"), types.StringValue("refresh_token"),
				})),
			},
			"token_endpoint_auth_method": schema.StringAttribute{
				// JumpCloud only returns a client secret on create, so switching to or from a
				// public client (none) creates a new application to get one.
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplaceIf(
					func(_ context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
						resp.RequiresReplace = (req.StateValue.ValueString() == "none") != (req.PlanValue.ValueString() == "none")
					},
					"Changing to or from `none` replaces the application, because JumpCloud only returns a client secret on create.",
					"Changing to or from none replaces the application, because JumpCloud only returns a client secret on create.",
				)},
				MarkdownDescription: "How the client authenticates at the token endpoint: `client_secret_basic`, `client_secret_post`, or `none` for a public client using PKCE. Changing to or from `none` replaces the application.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("client_secret_basic"),
			},
			"claims": schema.MapAttribute{
				MarkdownDescription: "Token claims, mapping each claim name to a JumpCloud user attribute. JumpCloud adds no profile claims on its own, " +
					"even for the `email` and `profile` scopes, so map the ones the application needs, for example " +
					"`{ email = \"email\", name = \"fullname\", groups = \"groups\" }`. The admin console's Profile scope adds " +
					"`{ given_name = \"firstname\", family_name = \"lastname\" }`; its Email scope adds `email` here and `email_verified` in `static_claims`. " +
					"`groups` lists the user groups bound to the application; JumpCloud sends it as a string, not a list, when the user is in exactly one of them.",
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
				Default:     mapdefault.StaticValue(types.MapValueMust(types.StringType, map[string]attr.Value{})),
			},
			"static_claims": schema.MapAttribute{
				MarkdownDescription: "Claims with the same value for every user, mapping each claim name to its value, for example " +
					"`{ email_verified = \"true\" }`. This is what the Email scope adds in the admin console. JumpCloud sends the values as strings. " +
					"Kept as is when not set, such as when set in the admin console; `{}` removes them.",
				Optional:      true,
				Computed:      true,
				ElementType:   types.StringType,
				PlanModifiers: []planmodifier.Map{mapplanmodifier.UseStateForUnknown()},
			},
			"access_token_format": schema.StringAttribute{
				MarkdownDescription: "Access token format: `opaque` (JumpCloud's default) or `jwt`, which applications can validate locally against the JWKS. " +
					"Kept as is when not set, such as when set in the admin console.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"audiences": schema.SetAttribute{
				MarkdownDescription: "Additional audiences an application can request for its access tokens, up to 5. " +
					"Kept as is when not set, such as when set in the admin console; `[]` removes them.",
				Optional:      true,
				Computed:      true,
				ElementType:   types.StringType,
				PlanModifiers: []planmodifier.Set{setplanmodifier.UseStateForUnknown()},
			},
			"client_id": schema.StringAttribute{
				MarkdownDescription: "OAuth client ID.",
				Computed:            true,
				PlanModifiers:       keepState,
			},
			"client_secret": schema.StringAttribute{
				MarkdownDescription: "OAuth client secret. Empty for `none` and after an import.",
				Computed:            true,
				Sensitive:           true,
				PlanModifiers:       keepState,
			},
		},
	}
}

func (r *oidcApplicationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerClient(req.ProviderData, &resp.Diagnostics)
}

func (r *oidcApplicationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan, config oidcApplicationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	settings := plan.settings(ctx, config, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	app, err := r.client.CreateOIDCApplication(ctx, client.OIDCApplication{
		DisplayLabel: plan.DisplayLabel.ValueString(),
		Hidden:       !plan.ShowInPortal.ValueBool(),
		OIDC:         settings,
	})
	if err != nil {
		resp.Diagnostics.AddError("Error creating JumpCloud OIDC application", err.Error())
		return
	}
	plan.ID = types.StringValue(app.ID)
	plan.ClientID = types.StringValue(app.OIDC.ClientID)
	plan.ClientSecret = types.StringValue(app.OIDC.ClientSecret)
	if plan.StaticClaims.IsUnknown() {
		plan.StaticClaims = claimsMap(ctx, app.OIDC.StaticClaims, &resp.Diagnostics)
	}
	if plan.AccessTokenFormat.IsUnknown() {
		plan.AccessTokenFormat = types.StringValue(app.OIDC.AccessTokenStrategy)
	}
	if plan.Audiences.IsUnknown() {
		plan.Audiences = stringSet(ctx, audienceList(app.OIDC.Audience), &resp.Diagnostics)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *oidcApplicationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state oidcApplicationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	app, err := r.client.GetOIDCApplication(ctx, state.ID.ValueString())
	if errors.Is(err, client.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading JumpCloud OIDC application", err.Error())
		return
	}

	state.DisplayLabel = types.StringValue(app.DisplayLabel)
	state.ShowInPortal = types.BoolValue(!app.Hidden)
	state.RedirectURIs = stringSet(ctx, app.OIDC.RedirectURIs, &resp.Diagnostics)
	state.GrantTypes = stringSet(ctx, app.OIDC.GrantTypes, &resp.Diagnostics)
	state.Claims = claimsMap(ctx, &app.OIDC.DynamicClaims, &resp.Diagnostics)
	state.StaticClaims = claimsMap(ctx, app.OIDC.StaticClaims, &resp.Diagnostics)
	state.AccessTokenFormat = types.StringValue(app.OIDC.AccessTokenStrategy)
	state.Audiences = stringSet(ctx, audienceList(app.OIDC.Audience), &resp.Diagnostics)
	state.LoginURL = types.StringValue(app.OIDC.RelyingPartyURL)
	state.TokenEndpointAuthMethod = types.StringValue(app.OIDC.TokenEndpointAuthMethod)
	state.ClientID = types.StringValue(app.OIDC.ClientID)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *oidcApplicationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state, config oidcApplicationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	settings := plan.settings(ctx, config, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	id := plan.ID.ValueString()
	if !plan.DisplayLabel.Equal(state.DisplayLabel) {
		if err := r.client.RenameApplication(ctx, id, plan.DisplayLabel.ValueString(), !plan.ShowInPortal.ValueBool()); err != nil {
			resp.Diagnostics.AddError("Error renaming JumpCloud OIDC application", err.Error())
			return
		}
		// Record the rename before touching the SSO settings, so failing there does not
		// leave state holding the old label while JumpCloud already has the new one.
		state.DisplayLabel = plan.DisplayLabel
		resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	if err := r.client.UpdateOIDCSettings(ctx, id, !plan.ShowInPortal.ValueBool(), settings); err != nil {
		resp.Diagnostics.AddError("Error updating JumpCloud OIDC application", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *oidcApplicationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state oidcApplicationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if err := r.client.DeleteApplication(ctx, state.ID.ValueString()); err != nil && !errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Error deleting JumpCloud OIDC application", err.Error())
	}
}

func (r *oidcApplicationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// settings returns the OIDC settings to write. Static claims are sent only when
// configured, so ones set in the admin console are kept.
func (m oidcApplicationModel) settings(ctx context.Context, config oidcApplicationModel, diags *diag.Diagnostics) client.OIDCSettings {
	// An empty list (not null) clears dynamic claims on update.
	dynamic := claimList(ctx, m.Claims, diags)
	var static *[]client.OIDCClaim
	if !config.StaticClaims.IsNull() {
		list := claimList(ctx, m.StaticClaims, diags)
		static = &list
	}
	// Unknown means not configured on create: send nothing and keep JumpCloud's default.
	var audience *[]string
	if !m.Audiences.IsUnknown() {
		list := setToStrings(ctx, m.Audiences, diags)
		if list == nil {
			list = []string{}
		}
		audience = &list
	}
	return client.OIDCSettings{
		AccessTokenStrategy:     m.AccessTokenFormat.ValueString(),
		Audience:                audience,
		RedirectURIs:            setToStrings(ctx, m.RedirectURIs, diags),
		GrantTypes:              setToStrings(ctx, m.GrantTypes, diags),
		TokenEndpointAuthMethod: m.TokenEndpointAuthMethod.ValueString(),
		RelyingPartyURL:         m.LoginURL.ValueString(),
		DynamicClaims:           dynamic,
		StaticClaims:            static,
	}
}

// claimList turns a claim map into JumpCloud's list, sorted so requests are deterministic.
func claimList(ctx context.Context, m types.Map, diags *diag.Diagnostics) []client.OIDCClaim {
	claims := map[string]string{}
	diags.Append(m.ElementsAs(ctx, &claims, false)...)
	list := []client.OIDCClaim{}
	for _, name := range slices.Sorted(maps.Keys(claims)) {
		list = append(list, client.OIDCClaim{Name: name, Value: claims[name]})
	}
	return list
}

// claimsMap turns JumpCloud's claim list into a map; nil becomes an empty map.
func claimsMap(ctx context.Context, list *[]client.OIDCClaim, diags *diag.Diagnostics) types.Map {
	claims := map[string]string{}
	if list != nil {
		for _, c := range *list {
			claims[c.Name] = c.Value
		}
	}
	m, d := types.MapValueFrom(ctx, types.StringType, claims)
	diags.Append(d...)
	return m
}

func audienceList(a *[]string) []string {
	if a == nil {
		return nil
	}
	return *a
}
