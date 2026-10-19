package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/loadsmart/terraform-provider-jumpcloud/internal/client"
)

var _ datasource.DataSourceWithConfigure = &roleDataSource{}

func NewRoleDataSource() datasource.DataSource { return &roleDataSource{} }

type roleDataSource struct{ client *client.Client }

type roleModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	Custom      types.Bool   `tfsdk:"custom"`
}

func (d *roleDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_role"
}

func (d *roleDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up an admin role, such as `Read Only` or a custom role, by its name. " +
			"Use it to give a `jumpcloud_service_account` a role. Fails unless exactly one role matches.",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{MarkdownDescription: "Role ID.", Computed: true},
			"name":        schema.StringAttribute{MarkdownDescription: "Role name, matched exactly and case-sensitively.", Required: true},
			"description": schema.StringAttribute{MarkdownDescription: "Role description.", Computed: true},
			"custom":      schema.BoolAttribute{MarkdownDescription: "Whether the organization created the role, rather than JumpCloud.", Computed: true},
		},
	}
}

func (d *roleDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = providerClient(req.ProviderData, &resp.Diagnostics)
}

func (d *roleDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var state roleModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	roles, err := d.client.ListRoles(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error reading JumpCloud roles", err.Error())
		return
	}
	// Roles are few, so matching here avoids depending on the filter syntax.
	name := state.Name.ValueString()
	var matches []client.Role
	for _, r := range roles {
		if r.Name == name {
			matches = append(matches, r)
		}
	}
	if len(matches) != 1 {
		resp.Diagnostics.AddError("Expected exactly one JumpCloud role", fmt.Sprintf("Expected one role named %q, found %d.", name, len(matches)))
		return
	}
	state.ID = types.StringValue(matches[0].ID)
	state.Description = types.StringValue(matches[0].Description)
	state.Custom = types.BoolValue(matches[0].IsCustom)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}
