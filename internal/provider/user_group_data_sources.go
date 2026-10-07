package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"

	"github.com/loadsmart/terraform-provider-jumpcloud/internal/client"
)

var (
	_ datasource.DataSourceWithConfigure = &userGroupDataSource{}
	_ datasource.DataSourceWithConfigure = &userGroupsDataSource{}
)

func NewUserGroupDataSource() datasource.DataSource  { return &userGroupDataSource{} }
func NewUserGroupsDataSource() datasource.DataSource { return &userGroupsDataSource{} }

type userGroupDataSource struct{ client *client.Client }

func (d *userGroupDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user_group"
}

func (d *userGroupDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a JumpCloud user group by its exact name. Fails unless exactly one group matches.",
		Attributes: map[string]schema.Attribute{
			"id":          schema.StringAttribute{MarkdownDescription: "User group ID.", Computed: true},
			"name":        schema.StringAttribute{MarkdownDescription: "Group name, matched exactly and case-sensitively.", Required: true},
			"description": schema.StringAttribute{MarkdownDescription: "Group description.", Computed: true},
		},
	}
}

func (d *userGroupDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = providerClient(req.ProviderData, &resp.Diagnostics)
}

func (d *userGroupDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config userGroupModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	name := config.Name.ValueString()
	groups, err := d.client.FindUserGroupsByName(ctx, name)
	if err != nil {
		resp.Diagnostics.AddError("Error reading JumpCloud user groups", err.Error())
		return
	}
	if len(groups) != 1 {
		resp.Diagnostics.AddError("JumpCloud user group not found", fmt.Sprintf("Expected one user group named %q, found %d.", name, len(groups)))
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, newUserGroupModel(groups[0]))...)
}

type userGroupsDataSource struct{ client *client.Client }

type userGroupsModel struct {
	Groups []userGroupModel `tfsdk:"groups"`
}

func (d *userGroupsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user_groups"
}

func (d *userGroupsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists every JumpCloud user group in the organization. Filter the result in Terraform, for example with `regexall`.",
		Attributes: map[string]schema.Attribute{
			"groups": schema.ListNestedAttribute{
				MarkdownDescription: "User groups.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":          schema.StringAttribute{MarkdownDescription: "User group ID.", Computed: true},
						"name":        schema.StringAttribute{MarkdownDescription: "Group name.", Computed: true},
						"description": schema.StringAttribute{MarkdownDescription: "Group description.", Computed: true},
					},
				},
			},
		},
	}
}

func (d *userGroupsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = providerClient(req.ProviderData, &resp.Diagnostics)
}

func (d *userGroupsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	groups, err := d.client.ListUserGroups(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error reading JumpCloud user groups", err.Error())
		return
	}
	state := userGroupsModel{Groups: make([]userGroupModel, 0, len(groups))}
	for _, g := range groups {
		state.Groups = append(state.Groups, newUserGroupModel(g))
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}
