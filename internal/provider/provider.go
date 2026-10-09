package provider

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/loadsmart/terraform-provider-jumpcloud/internal/client"
)

const defaultAPIURL = "https://console.jumpcloud.com"

var _ provider.Provider = &jumpcloudProvider{}

type jumpcloudProvider struct {
	// version is "dev" for local builds, "test" in tests, and the release tag otherwise.
	version string
}

type providerModel struct {
	APIKey types.String `tfsdk:"api_key"`
	OrgID  types.String `tfsdk:"org_id"`
	APIURL types.String `tfsdk:"api_url"`
}

// Config holds the resolved provider settings.
type Config struct {
	APIKey string
	OrgID  string
	APIURL string
}

// New returns a constructor for the JumpCloud provider.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &jumpcloudProvider{version: version}
	}
}

func (p *jumpcloudProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "jumpcloud"
	resp.Version = p.version
}

func (p *jumpcloudProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage JumpCloud user groups, group memberships, OIDC applications, and application access.",
		Attributes: map[string]schema.Attribute{
			"api_key": schema.StringAttribute{
				MarkdownDescription: "JumpCloud API key. Can also be set with the `JUMPCLOUD_API_KEY` environment variable.",
				Optional:            true,
				Sensitive:           true,
			},
			"org_id": schema.StringAttribute{
				MarkdownDescription: "JumpCloud organization ID, required only for multi-tenant (MTP) administrators. Can also be set with the `JUMPCLOUD_ORG_ID` environment variable.",
				Optional:            true,
			},
			"api_url": schema.StringAttribute{
				MarkdownDescription: "JumpCloud console base URL. Defaults to `" + defaultAPIURL + "`; use `https://console.eu.jumpcloud.com` for EU organizations. Can also be set with the `JUMPCLOUD_API_URL` environment variable.",
				Optional:            true,
			},
		},
	}
}

func (p *jumpcloudProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var data providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	for name, value := range map[string]types.String{"api_key": data.APIKey, "org_id": data.OrgID, "api_url": data.APIURL} {
		if value.IsUnknown() {
			resp.Diagnostics.AddAttributeError(path.Root(name), "Unknown JumpCloud provider setting",
				fmt.Sprintf("The provider cannot be configured because %q is unknown until apply. Set it to a known value or use its environment variable.", name))
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}

	cfg, err := resolveConfig(data, os.Getenv)
	if err != nil {
		resp.Diagnostics.AddError("Invalid JumpCloud provider configuration", err.Error())
		return
	}

	c := client.New(cfg.APIURL, cfg.APIKey, cfg.OrgID, "terraform-provider-jumpcloud/"+p.version)
	resp.DataSourceData = c
	resp.ResourceData = c
}

// resolveConfig merges provider arguments with environment variables; arguments win.
func resolveConfig(data providerModel, getenv func(string) string) (*Config, error) {
	pick := func(v types.String, env string) string {
		if s := v.ValueString(); s != "" {
			return s
		}
		return getenv(env)
	}

	cfg := &Config{
		APIKey: pick(data.APIKey, "JUMPCLOUD_API_KEY"),
		OrgID:  pick(data.OrgID, "JUMPCLOUD_ORG_ID"),
		APIURL: strings.TrimRight(pick(data.APIURL, "JUMPCLOUD_API_URL"), "/"),
	}
	if cfg.APIURL == "" {
		cfg.APIURL = defaultAPIURL
	}

	if cfg.APIKey == "" {
		return nil, fmt.Errorf("missing API key: set the api_key argument or the JUMPCLOUD_API_KEY environment variable")
	}
	u, err := url.Parse(cfg.APIURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return nil, fmt.Errorf("invalid api_url %q: expected an absolute http(s) URL such as %s", cfg.APIURL, defaultAPIURL)
	}
	return cfg, nil
}

func (p *jumpcloudProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewUserGroupResource,
		NewUserGroupMembershipsResource,
		NewUserGroupMembersResource,
		NewOIDCApplicationResource,
		NewSAMLApplicationResource,
		NewApplicationAssociationResource,
	}
}

func (p *jumpcloudProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewUserGroupDataSource,
		NewUserGroupsDataSource,
		NewUsersDataSource,
		NewApplicationDataSource,
	}
}
