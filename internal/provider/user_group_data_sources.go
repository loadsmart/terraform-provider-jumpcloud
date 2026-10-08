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
			"email":       schema.StringAttribute{MarkdownDescription: "Group email address.", Computed: true},
			"membership_method": schema.StringAttribute{
				MarkdownDescription: "`STATIC` or `NOTSET` for static groups, `DYNAMIC_AUTOMATED` or `DYNAMIC_REVIEW_REQUIRED` for dynamic ones.",
				Computed:            true,
			},
			"query": schema.StringAttribute{MarkdownDescription: "Membership rule as JSON for dynamic groups, empty for static groups.", Computed: true},
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
	// The list omits the membership fields, so read the group itself.
	g, err := d.client.GetUserGroup(ctx, groups[0].ID)
	if err != nil {
		resp.Diagnostics.AddError("Error reading JumpCloud user group", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, newUserGroupModel(*g))...)
}

type userGroupsDataSource struct{ client *client.Client }

type userGroupsModel struct {
	Groups []userGroupSummaryModel `tfsdk:"groups"`
}

func (d *userGroupsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user_groups"
}

func (d *userGroupsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists every JumpCloud user group in the organization. Filter the result in Terraform, for example with `regexall`. " +
			"JumpCloud's list leaves out membership details; use `jumpcloud_user_group` for a group's `membership_method` and `query`.",
		Attributes: map[string]schema.Attribute{
			"groups": schema.ListNestedAttribute{
				MarkdownDescription: "User groups.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":          schema.StringAttribute{MarkdownDescription: "User group ID.", Computed: true},
						"name":        schema.StringAttribute{MarkdownDescription: "Group name.", Computed: true},
						"description": schema.StringAttribute{MarkdownDescription: "Group description.", Computed: true},
						"email":       schema.StringAttribute{MarkdownDescription: "Group email address.", Computed: true},
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
	state := userGroupsModel{Groups: make([]userGroupSummaryModel, 0, len(groups))}
	for _, g := range groups {
		state.Groups = append(state.Groups, userGroupSummaryModel{
			ID:          types.StringValue(g.ID),
			Name:        types.StringValue(g.Name),
			Description: types.StringValue(g.Description),
			Email:       types.StringValue(g.Email),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}
