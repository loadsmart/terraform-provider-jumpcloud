package provider

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/loadsmart/terraform-provider-jumpcloud/internal/client"
)

var (
	_ resource.ResourceWithConfigure      = &userResource{}
	_ resource.ResourceWithImportState    = &userResource{}
	_ resource.ResourceWithValidateConfig = &userResource{}
)

var userStates = []string{"ACTIVATED", "STAGED", "SUSPENDED"}

func NewUserResource() resource.Resource { return &userResource{} }

type userResource struct{ client *client.Client }

type userResourceModel struct {
	ID         types.String `tfsdk:"id"`
	Username   types.String `tfsdk:"username"`
	Email      types.String `tfsdk:"email"`
	FirstName  types.String `tfsdk:"first_name"`
	LastName   types.String `tfsdk:"last_name"`
	Department types.String `tfsdk:"department"`
	JobTitle   types.String `tfsdk:"job_title"`
	State      types.String `tfsdk:"state"`
	Attributes types.Map    `tfsdk:"attributes"`
}

func (r *userResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user"
}

func (r *userResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	optional := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{MarkdownDescription: desc, Optional: true, Computed: true, Default: stringdefault.StaticString("")}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a JumpCloud user. Use it for accounts that no other system owns, such as test and service " +
			"accounts; people are created and updated by the HR integration, which would undo changes made here. " +
			"The provider does not set passwords: set one in the admin console or have the user activate their account.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "User ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"username":   schema.StringAttribute{MarkdownDescription: "Username.", Required: true},
			"email":      schema.StringAttribute{MarkdownDescription: "Email address.", Required: true},
			"first_name": optional("First name."),
			"last_name":  optional("Last name."),
			"department": optional("Department, which dynamic group rules can match as `user.user_department`."),
			"job_title":  optional("Job title, which dynamic group rules can match as `user.user_job_title`."),
			"state": schema.StringAttribute{
				MarkdownDescription: "Account state: `ACTIVATED`, `STAGED`, or `SUSPENDED`. When not set, a new user gets the organization's default state and an existing one keeps its state.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"attributes": schema.MapAttribute{
				MarkdownDescription: "Custom attributes by name. Kept as is when not set; `{}` removes them all.",
				Optional:            true,
				Computed:            true,
				ElementType:         types.StringType,
				PlanModifiers:       []planmodifier.Map{mapplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *userResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var state types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("state"), &state)...)
	if !state.IsNull() && !state.IsUnknown() && !slices.Contains(userStates, state.ValueString()) {
		resp.Diagnostics.AddAttributeError(path.Root("state"), "Invalid user state",
			fmt.Sprintf("state must be one of %q, got %q.", userStates, state.ValueString()))
	}
}

func (r *userResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerClient(req.ProviderData, &resp.Diagnostics)
}

func (r *userResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan, config userResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	fields := plan.fields(ctx, config, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	u, err := r.client.CreateUser(ctx, fields)
	if err != nil {
		resp.Diagnostics.AddError("Error creating JumpCloud user", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, newUserResourceModel(ctx, *u, &resp.Diagnostics))...)
}

func (r *userResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state userResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	u, err := r.client.GetUser(ctx, state.ID.ValueString())
	if errors.Is(err, client.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading JumpCloud user", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, newUserResourceModel(ctx, *u, &resp.Diagnostics))...)
}

func (r *userResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, config userResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	fields := plan.fields(ctx, config, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, err := r.client.UpdateUser(ctx, plan.ID.ValueString(), fields); err != nil {
		resp.Diagnostics.AddError("Error updating JumpCloud user", err.Error())
		return
	}
	// Read the user back so state holds what JumpCloud stored.
	u, err := r.client.GetUser(ctx, plan.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error reading JumpCloud user", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, newUserResourceModel(ctx, *u, &resp.Diagnostics))...)
}

func (r *userResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state userResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteUser(ctx, state.ID.ValueString()); err != nil && !errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Error deleting JumpCloud user", err.Error())
	}
}

func (r *userResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// fields returns what to write. State and attributes are sent only when configured.
func (m userResourceModel) fields(ctx context.Context, config userResourceModel, diags *diag.Diagnostics) client.UserFields {
	f := client.UserFields{
		Username: m.Username.ValueString(), Email: m.Email.ValueString(),
		Firstname: m.FirstName.ValueString(), Lastname: m.LastName.ValueString(),
		Department: m.Department.ValueString(), JobTitle: m.JobTitle.ValueString(),
	}
	if !config.State.IsNull() {
		f.State = m.State.ValueString()
	}
	if !config.Attributes.IsNull() {
		var values map[string]string
		diags.Append(m.Attributes.ElementsAs(ctx, &values, false)...)
		attrs := []client.UserAttribute{}
		for _, name := range slices.Sorted(maps.Keys(values)) {
			attrs = append(attrs, client.UserAttribute{Name: name, Value: values[name]})
		}
		f.Attributes = &attrs
	}
	return f
}

func newUserResourceModel(ctx context.Context, u client.User, diags *diag.Diagnostics) userResourceModel {
	values := make(map[string]string, len(u.Attributes))
	for _, a := range u.Attributes {
		values[a.Name] = a.Value
	}
	attrs, d := types.MapValueFrom(ctx, types.StringType, values)
	diags.Append(d...)
	return userResourceModel{
		ID: types.StringValue(u.ID), Username: types.StringValue(u.Username), Email: types.StringValue(u.Email),
		FirstName: types.StringValue(u.Firstname), LastName: types.StringValue(u.Lastname),
		Department: types.StringValue(u.Department), JobTitle: types.StringValue(u.JobTitle),
		State: types.StringValue(u.State), Attributes: attrs,
	}
}
