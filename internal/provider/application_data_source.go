package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/loadsmart/terraform-provider-jumpcloud/internal/client"
)

var _ datasource.DataSourceWithConfigure = &applicationDataSource{}

func NewApplicationDataSource() datasource.DataSource { return &applicationDataSource{} }

type applicationDataSource struct{ client *client.Client }

type applicationModel struct {
	ID           types.String `tfsdk:"id"`
	DisplayLabel types.String `tfsdk:"display_label"`
}

func (d *applicationDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_application"
}

func (d *applicationDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a JumpCloud SSO application, such as one created in the admin console, by its label. Fails unless exactly one application matches.",
		Attributes: map[string]schema.Attribute{
			"id":            schema.StringAttribute{MarkdownDescription: "Application ID.", Computed: true},
			"display_label": schema.StringAttribute{MarkdownDescription: "Label shown in the admin console, matched exactly and case-sensitively.", Required: true},
		},
	}
}

func (d *applicationDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = providerClient(req.ProviderData, &resp.Diagnostics)
}

func (d *applicationDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var state applicationModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	label := state.DisplayLabel.ValueString()
	apps, err := d.client.ListApplications(ctx, map[string]string{"displayLabel": label})
	if err != nil {
		resp.Diagnostics.AddError("Error reading JumpCloud applications", err.Error())
		return
	}
	if len(apps) != 1 {
		resp.Diagnostics.AddError("JumpCloud application not found", fmt.Sprintf("Expected one application labeled %q, found %d.", label, len(apps)))
		return
	}
	state.ID = types.StringValue(apps[0].ID)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}
