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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
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
				MarkdownDescription: "How the client authenticates at the token endpoint: `client_secret_basic`, `client_secret_post`, or `none` for a public client using PKCE.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString("client_secret_basic"),
			},
			"claims": schema.MapAttribute{
				MarkdownDescription: "Extra token claims, mapping each claim name to a JumpCloud user attribute, for example `{ department = \"department\" }`.",
				Optional:            true,
				Computed:            true,
				ElementType:         types.StringType,
				Default:             mapdefault.StaticValue(types.MapValueMust(types.StringType, map[string]attr.Value{})),
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
	var plan oidcApplicationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	settings := plan.settings(ctx, &resp.Diagnostics)
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

	claims := make(map[string]string, len(app.OIDC.DynamicClaims))
	for _, c := range app.OIDC.DynamicClaims {
		claims[c.Name] = c.Value
	}
	var diags diag.Diagnostics
	state.DisplayLabel = types.StringValue(app.DisplayLabel)
	state.ShowInPortal = types.BoolValue(!app.Hidden)
	state.RedirectURIs, diags = types.SetValueFrom(ctx, types.StringType, app.OIDC.RedirectURIs)
	resp.Diagnostics.Append(diags...)
	state.GrantTypes, diags = types.SetValueFrom(ctx, types.StringType, app.OIDC.GrantTypes)
	resp.Diagnostics.Append(diags...)
	state.Claims, diags = types.MapValueFrom(ctx, types.StringType, claims)
	resp.Diagnostics.Append(diags...)
	state.LoginURL = types.StringValue(app.OIDC.RelyingPartyURL)
	state.TokenEndpointAuthMethod = types.StringValue(app.OIDC.TokenEndpointAuthMethod)
	state.ClientID = types.StringValue(app.OIDC.ClientID)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *oidcApplicationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state oidcApplicationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	settings := plan.settings(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	id := plan.ID.ValueString()
	if !plan.DisplayLabel.Equal(state.DisplayLabel) {
		if err := r.client.RenameApplication(ctx, id, plan.DisplayLabel.ValueString()); err != nil {
			resp.Diagnostics.AddError("Error renaming JumpCloud OIDC application", err.Error())
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

func (m oidcApplicationModel) settings(ctx context.Context, diags *diag.Diagnostics) client.OIDCSettings {
	claims := map[string]string{}
	diags.Append(m.Claims.ElementsAs(ctx, &claims, false)...)
	// An empty list (not null) clears claims on update.
	dynamic := []client.OIDCClaim{}
	for _, name := range slices.Sorted(maps.Keys(claims)) {
		dynamic = append(dynamic, client.OIDCClaim{Name: name, Value: claims[name]})
	}
	return client.OIDCSettings{
		RedirectURIs:            setToStrings(ctx, m.RedirectURIs, diags),
		GrantTypes:              setToStrings(ctx, m.GrantTypes, diags),
		TokenEndpointAuthMethod: m.TokenEndpointAuthMethod.ValueString(),
		RelyingPartyURL:         m.LoginURL.ValueString(),
		DynamicClaims:           dynamic,
	}
}
