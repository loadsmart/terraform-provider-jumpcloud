package provider

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/loadsmart/terraform-provider-jumpcloud/internal/client"
)

var (
	_ resource.ResourceWithConfigure      = &serviceAccountResource{}
	_ resource.ResourceWithImportState    = &serviceAccountResource{}
	_ resource.ResourceWithValidateConfig = &serviceAccountResource{}
)

func NewServiceAccountResource() resource.Resource { return &serviceAccountResource{} }

type serviceAccountResource struct{ client *client.Client }

type serviceAccountModel struct {
	ID                 types.String `tfsdk:"id"`
	Name               types.String `tfsdk:"name"`
	RoleID             types.String `tfsdk:"role_id"`
	SecretLifetimeDays types.Int64  `tfsdk:"secret_lifetime_days"`
	ClientID           types.String `tfsdk:"client_id"`
	ClientSecret       types.String `tfsdk:"client_secret"`
	ExpiresAt          types.String `tfsdk:"expires_at"`
	Status             types.String `tfsdk:"status"`
}

func (r *serviceAccountResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service_account"
}

func (r *serviceAccountResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keepState := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a service account, which calls the JumpCloud admin API with OAuth 2.0 client credentials " +
			"instead of an admin's API key. Exchange `client_id` and `client_secret` for a token at " +
			"`https://admin-oauth.id.jumpcloud.com/oauth2/token` with `grant_type=client_credentials` and `scope=api`, " +
			"then call the API with `Authorization: Bearer <token>`. The account can do what its admin role allows. " +
			"See [Service Account for APIs](https://jumpcloud.com/support/service-account-for-apis).\n\n" +
			"JumpCloud cannot change a service account, so changing any argument replaces it, with a new client ID and secret. " +
			"Creating one needs an API key whose admin has the Billing role, and an organization can have at most 40.\n\n" +
			"~> JumpCloud returns the client secret only when the account is created, so `client_secret` is stored in the " +
			"Terraform state and is empty after an import. Use a state backend that encrypts its data. " +
			"The secret stops working at `expires_at`; replace the account before then.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Service account ID.",
				Computed:            true,
				PlanModifiers:       keepState,
			},
			"name": schema.StringAttribute{
				MarkdownDescription: "Name shown in the admin console. Changing it replaces the account.",
				Required:            true,
				PlanModifiers:       replace,
			},
			"role_id": schema.StringAttribute{
				MarkdownDescription: "ID of the admin role that decides what the account can do, such as one from `jumpcloud_role`. Changing it replaces the account.",
				Required:            true,
				PlanModifiers:       replace,
			},
			"secret_lifetime_days": schema.Int64Attribute{
				MarkdownDescription: "Days until the client secret expires: `30`, `60`, `90`, or `365`. Defaults to `365`. Changing it replaces the account.",
				Optional:            true,
				Computed:            true,
				Default:             int64default.StaticInt64(365),
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
			},
			"client_id": schema.StringAttribute{
				MarkdownDescription: "OAuth client ID.",
				Computed:            true,
				PlanModifiers:       keepState,
			},
			"client_secret": schema.StringAttribute{
				MarkdownDescription: "OAuth client secret. Empty after an import.",
				Computed:            true,
				Sensitive:           true,
				PlanModifiers:       keepState,
			},
			"expires_at": schema.StringAttribute{
				MarkdownDescription: "When the client secret expires, in RFC 3339 format.",
				Computed:            true,
				PlanModifiers:       keepState,
			},
			"status": schema.StringAttribute{
				MarkdownDescription: "State of the client secret: `ACTIVE`, `EXPIRING` when it expires soon, or `EXPIRED`.",
				Computed:            true,
				PlanModifiers:       keepState,
			},
		},
	}
}

func (r *serviceAccountResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerClient(req.ProviderData, &resp.Diagnostics)
}

func (r *serviceAccountResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var lifetime types.Int64
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("secret_lifetime_days"), &lifetime)...)
	if lifetime.IsNull() || lifetime.IsUnknown() || slices.Contains(client.SecretLifetimes, lifetime.ValueInt64()) {
		return
	}
	resp.Diagnostics.AddAttributeError(path.Root("secret_lifetime_days"), "Unsupported secret lifetime",
		fmt.Sprintf("JumpCloud offers client secrets that last %v days, not %d.", client.SecretLifetimes, lifetime.ValueInt64()))
}

func (r *serviceAccountResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan serviceAccountModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	sa, err := r.client.CreateServiceAccount(ctx, plan.Name.ValueString(), plan.RoleID.ValueString(), plan.SecretLifetimeDays.ValueInt64())
	if err != nil {
		resp.Diagnostics.AddError("Error creating JumpCloud service account", err.Error())
		return
	}
	plan.ID = types.StringValue(sa.ID)
	plan.ClientSecret = types.StringValue(sa.FirstClientSecret().Secret)
	plan.setSecret(sa)
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *serviceAccountResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state serviceAccountModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	sa, err := r.client.GetServiceAccount(ctx, state.ID.ValueString())
	if errors.Is(err, client.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading JumpCloud service account", err.Error())
		return
	}
	state.Name = types.StringValue(sa.Name)
	state.RoleID = types.StringValue(sa.RoleID)
	state.setSecret(sa)
	// An import has no secret to keep.
	if state.ClientSecret.IsNull() {
		state.ClientSecret = types.StringValue("")
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

// Update is never called with a change: every argument replaces the account.
func (r *serviceAccountResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan serviceAccountModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *serviceAccountResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state serviceAccountModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteServiceAccount(ctx, state.ID.ValueString())
	if err != nil && !errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Error deleting JumpCloud service account", err.Error())
	}
}

func (r *serviceAccountResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// setSecret copies the client secret's metadata, but not the secret itself, which only
// the create response has.
func (m *serviceAccountModel) setSecret(sa *client.ServiceAccount) {
	m.ExpiresAt = types.StringValue(sa.ExpiresAt)
	m.Status = types.StringValue(sa.Status)
	// JumpCloud may leave the credentials out of reads; then the state keeps what create returned.
	if secret := sa.FirstClientSecret(); secret != nil {
		m.ClientID = types.StringValue(secret.ClientID)
		if secret.ExpiresAt != "" {
			m.ExpiresAt = types.StringValue(secret.ExpiresAt)
		}
		if secret.Status != "" {
			m.Status = types.StringValue(secret.Status)
		}
		if days := secret.LifetimeDays(); days > 0 {
			m.SecretLifetimeDays = types.Int64Value(days)
		}
	}
	// An import starts with only the ID.
	if m.ClientID.IsNull() {
		m.ClientID = types.StringValue("")
	}
	if m.SecretLifetimeDays.IsNull() {
		m.SecretLifetimeDays = types.Int64Value(365)
	}
}
