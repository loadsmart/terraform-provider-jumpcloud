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
	ID             types.String `tfsdk:"id"`
	DisplayLabel   types.String `tfsdk:"display_label"`
	Template       types.String `tfsdk:"template"`
	Type           types.String `tfsdk:"type"`
	ShowInPortal   types.Bool   `tfsdk:"show_in_portal"`
	SSOURL         types.String `tfsdk:"sso_url"`
	IdPEntityID    types.String `tfsdk:"idp_entity_id"`
	IdPCertificate types.String `tfsdk:"idp_certificate"`
}

func (d *applicationDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_application"
}

func (d *applicationDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up a JumpCloud SSO application, such as one created in the admin console, by its label. Fails unless exactly one application matches.",
		Attributes: map[string]schema.Attribute{
			"id":              schema.StringAttribute{MarkdownDescription: "Application ID.", Computed: true},
			"display_label":   schema.StringAttribute{MarkdownDescription: "Label shown in the admin console, matched exactly and case-sensitively.", Required: true},
			"template":        schema.StringAttribute{MarkdownDescription: "Catalog template the application was created from, such as `saml2`, `oidc`, `bookmark`, or the name of a catalog app.", Computed: true},
			"type":            schema.StringAttribute{MarkdownDescription: "Sign-in protocol: `saml`, `oidc`, or `bookmark`.", Computed: true},
			"show_in_portal":  schema.BoolAttribute{MarkdownDescription: "Whether the application appears in users' JumpCloud portal.", Computed: true},
			"sso_url":         schema.StringAttribute{MarkdownDescription: "IdP single sign-on URL of a SAML application, or the target URL of a bookmark. Empty for OIDC applications.", Computed: true},
			"idp_entity_id":   schema.StringAttribute{MarkdownDescription: "IdP entity ID of a SAML application. Empty for other types.", Computed: true},
			"idp_certificate": schema.StringAttribute{MarkdownDescription: "IdP signing certificate in PEM of a SAML application. Empty for other types.", Computed: true},
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
		resp.Diagnostics.AddError("Expected exactly one JumpCloud application", fmt.Sprintf("Expected one application labeled %q, found %d.", label, len(apps)))
		return
	}
	app, err := d.client.GetApplicationDetails(ctx, apps[0].ID)
	if err != nil {
		resp.Diagnostics.AddError("Error reading JumpCloud application", err.Error())
		return
	}
	state.ID = types.StringValue(app.ID)
	state.Template = types.StringValue(app.Template)
	state.Type = types.StringValue(app.Type)
	state.ShowInPortal = types.BoolValue(!app.Hidden)
	state.SSOURL = types.StringValue(app.SSOURL)
	state.IdPEntityID = types.StringValue(app.IdPEntityID)
	state.IdPCertificate = types.StringValue(app.IdPCertificate)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}
