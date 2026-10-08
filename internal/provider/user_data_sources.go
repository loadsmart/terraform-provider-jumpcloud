package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/loadsmart/terraform-provider-jumpcloud/internal/client"
)

var _ datasource.DataSourceWithConfigure = &usersDataSource{}

func NewUsersDataSource() datasource.DataSource { return &usersDataSource{} }

type usersDataSource struct{ client *client.Client }

type usersModel struct {
	Filter types.Map   `tfsdk:"filter"`
	Users  []userModel `tfsdk:"users"`
}

func (d *usersDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_users"
}

func (d *usersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists JumpCloud users, optionally filtered by exact field values. To look up one user, filter by `email` and use `one(data.jumpcloud_users.<name>.users)`.",
		Attributes: map[string]schema.Attribute{
			"filter": schema.MapAttribute{
				MarkdownDescription: "Exact, case-sensitive matches on JumpCloud user fields, combined with AND, for example `{ department = \"Engineering\" }`. " +
					"Keys use the JumpCloud API field names, such as `department`, `jobTitle`, `state`, or `email`. Omit to list every user.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"users": schema.ListNestedAttribute{
				MarkdownDescription: "Matching users.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":         schema.StringAttribute{MarkdownDescription: "User ID.", Computed: true},
						"email":      schema.StringAttribute{MarkdownDescription: "Email address.", Computed: true},
						"username":   schema.StringAttribute{MarkdownDescription: "Username.", Computed: true},
						"state":      schema.StringAttribute{MarkdownDescription: "Account state, such as `ACTIVATED`, `STAGED`, or `SUSPENDED`.", Computed: true},
						"department": schema.StringAttribute{MarkdownDescription: "Department.", Computed: true},
						"job_title":  schema.StringAttribute{MarkdownDescription: "Job title.", Computed: true},
						"attributes": schema.MapAttribute{MarkdownDescription: "Custom attributes, keyed by name.", Computed: true, ElementType: types.StringType},
					},
				},
			},
		},
	}
}

func (d *usersDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = providerClient(req.ProviderData, &resp.Diagnostics)
}

func (d *usersDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var state usersModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &state)...)
	filters := map[string]string{}
	if !state.Filter.IsNull() {
		resp.Diagnostics.Append(state.Filter.ElementsAs(ctx, &filters, false)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	users, err := d.client.ListUsers(ctx, filters)
	if err != nil {
		resp.Diagnostics.AddError("Error reading JumpCloud users", err.Error())
		return
	}
	state.Users = make([]userModel, 0, len(users))
	for _, u := range users {
		state.Users = append(state.Users, newUserModel(ctx, u, &resp.Diagnostics))
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

type userModel struct {
	ID         types.String `tfsdk:"id"`
	Email      types.String `tfsdk:"email"`
	Username   types.String `tfsdk:"username"`
	State      types.String `tfsdk:"state"`
	Department types.String `tfsdk:"department"`
	JobTitle   types.String `tfsdk:"job_title"`
	Attributes types.Map    `tfsdk:"attributes"`
}

func newUserModel(ctx context.Context, u client.User, diags *diag.Diagnostics) userModel {
	attrs := make(map[string]string, len(u.Attributes))
	for _, a := range u.Attributes {
		attrs[a.Name] = a.Value
	}
	attributes, d := types.MapValueFrom(ctx, types.StringType, attrs)
	diags.Append(d...)
	return userModel{
		ID:         types.StringValue(u.ID),
		Email:      types.StringValue(u.Email),
		Username:   types.StringValue(u.Username),
		State:      types.StringValue(u.State),
		Department: types.StringValue(u.Department),
		JobTitle:   types.StringValue(u.JobTitle),
		Attributes: attributes,
	}
}
