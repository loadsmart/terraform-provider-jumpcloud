package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/loadsmart/terraform-provider-jumpcloud/internal/client"
)

var (
	_ datasource.DataSourceWithConfigure = &userDataSource{}
	_ datasource.DataSourceWithConfigure = &usersDataSource{}
)

func NewUserDataSource() datasource.DataSource  { return &userDataSource{} }
func NewUsersDataSource() datasource.DataSource { return &usersDataSource{} }

// userAttributes describes the user fields both data sources return. email is the
// lookup key of jumpcloud_user, so that data source overrides it.
func userAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id":         schema.StringAttribute{MarkdownDescription: "User ID.", Computed: true},
		"email":      schema.StringAttribute{MarkdownDescription: "Email address.", Computed: true},
		"username":   schema.StringAttribute{MarkdownDescription: "Username.", Computed: true},
		"state":      schema.StringAttribute{MarkdownDescription: "Account state, such as `ACTIVATED`, `STAGED`, or `SUSPENDED`.", Computed: true},
		"department": schema.StringAttribute{MarkdownDescription: "Department.", Computed: true},
		"job_title":  schema.StringAttribute{MarkdownDescription: "Job title.", Computed: true},
		"attributes": schema.MapAttribute{MarkdownDescription: "Custom attributes, keyed by name.", Computed: true, ElementType: types.StringType},
	}
}

type userDataSource struct{ client *client.Client }

func (d *userDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user"
}

func (d *userDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := userAttributes()
	attrs["email"] = schema.StringAttribute{MarkdownDescription: "Email address, matched exactly and case-sensitively.", Required: true}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a JumpCloud user by email. Fails unless exactly one user matches.",
		Attributes:          attrs,
	}
}

func (d *userDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = providerClient(req.ProviderData, &resp.Diagnostics)
}

func (d *userDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config userModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	email := config.Email.ValueString()
	users, err := d.client.ListUsers(ctx, map[string]string{"email": email})
	if err != nil {
		resp.Diagnostics.AddError("Error reading JumpCloud users", err.Error())
		return
	}
	if len(users) != 1 {
		resp.Diagnostics.AddError("JumpCloud user not found", fmt.Sprintf("Expected one user with email %q, found %d.", email, len(users)))
		return
	}
	state := newUserModel(ctx, users[0], &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

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
		MarkdownDescription: "Lists JumpCloud users, optionally filtered by exact field values.",
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
				NestedObject:        schema.NestedAttributeObject{Attributes: userAttributes()},
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
