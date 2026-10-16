package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/loadsmart/terraform-provider-jumpcloud/internal/client"
)

var (
	_ resource.ResourceWithConfigure      = &samlApplicationResource{}
	_ resource.ResourceWithImportState    = &samlApplicationResource{}
	_ resource.ResourceWithModifyPlan     = &samlApplicationResource{}
	_ resource.ResourceWithValidateConfig = &samlApplicationResource{}
)

func NewSAMLApplicationResource() resource.Resource { return &samlApplicationResource{} }

type samlApplicationResource struct{ client *client.Client }

type samlApplicationModel struct {
	ID                 types.String `tfsdk:"id"`
	DisplayLabel       types.String `tfsdk:"display_label"`
	Template           types.String `tfsdk:"template"`
	ShowInPortal       types.Bool   `tfsdk:"show_in_portal"`
	SSOURL             types.String `tfsdk:"sso_url"`
	SPEntityID         types.String `tfsdk:"sp_entity_id"`
	ACSURLs            types.List   `tfsdk:"acs_urls"`
	IdPEntityID        types.String `tfsdk:"idp_entity_id"`
	IdPInitURL         types.String `tfsdk:"idp_init_url"`
	DefaultRelayState  types.String `tfsdk:"default_relay_state"`
	NameID             types.String `tfsdk:"name_id"`
	NameIDFormat       types.String `tfsdk:"name_id_format"`
	SignResponse       types.Bool   `tfsdk:"sign_response"`
	SignAssertion      types.Bool   `tfsdk:"sign_assertion"`
	GroupsAttribute    types.String `tfsdk:"groups_attribute"`
	ConstantAttributes types.Map    `tfsdk:"constant_attributes"`
	UserAttributes     types.Map    `tfsdk:"user_attributes"`
	IdPCertificate     types.String `tfsdk:"idp_certificate"`
}

// samlSSOURLPrefix is where JumpCloud serves the IdP endpoint of SAML applications.
const samlSSOURLPrefix = "https://sso.jumpcloud.com/saml2/"

// Optional string settings: kept as is when not configured.
var samlStringSettings = []struct {
	attr string
	key  string
	get  func(*samlApplicationModel) *types.String
}{
	{"idp_entity_id", client.SAMLIdPEntityID, func(m *samlApplicationModel) *types.String { return &m.IdPEntityID }},
	{"idp_init_url", client.SAMLIdPInitURL, func(m *samlApplicationModel) *types.String { return &m.IdPInitURL }},
	{"default_relay_state", client.SAMLDefaultRelayState, func(m *samlApplicationModel) *types.String { return &m.DefaultRelayState }},
	{"name_id", client.SAMLNameID, func(m *samlApplicationModel) *types.String { return &m.NameID }},
	{"name_id_format", client.SAMLNameIDFormat, func(m *samlApplicationModel) *types.String { return &m.NameIDFormat }},
}

var samlBoolSettings = []struct {
	attr string
	key  string
	get  func(*samlApplicationModel) *types.Bool
}{
	{"sign_response", client.SAMLSignResponse, func(m *samlApplicationModel) *types.Bool { return &m.SignResponse }},
	{"sign_assertion", client.SAMLSignAssertion, func(m *samlApplicationModel) *types.Bool { return &m.SignAssertion }},
}

var samlAttributeSettings = []struct {
	attr string
	key  string
	get  func(*samlApplicationModel) *types.Map
}{
	{"constant_attributes", client.SAMLConstantAttrs, func(m *samlApplicationModel) *types.Map { return &m.ConstantAttributes }},
	{"user_attributes", client.SAMLUserAttrs, func(m *samlApplicationModel) *types.Map { return &m.UserAttributes }},
}

func (r *samlApplicationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_saml_application"
}

func (r *samlApplicationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	keep := []planmodifier.String{stringplanmodifier.UseStateForUnknown()}
	optionalString := func(desc string) schema.StringAttribute {
		return schema.StringAttribute{MarkdownDescription: desc + " Kept as is when not set.", Optional: true, Computed: true, PlanModifiers: keep}
	}
	optionalBool := func(desc string) schema.BoolAttribute {
		return schema.BoolAttribute{
			MarkdownDescription: desc + " Kept as is when not set.", Optional: true, Computed: true,
			PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
		}
	}
	optionalMap := func(desc string) schema.MapAttribute {
		return schema.MapAttribute{
			MarkdownDescription: desc + " Kept as is when not set; `{}` removes them.", Optional: true, Computed: true,
			ElementType: types.StringType, PlanModifiers: []planmodifier.Map{mapplanmodifier.UseStateForUnknown()},
		}
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a SAML application in JumpCloud: a custom app (`template = \"saml2\"`) or an app from " +
			"JumpCloud's catalog. JumpCloud generates the IdP certificate; " +
			"give `idp_certificate`, `sso_url`, and `idp_entity_id` to the service provider. " +
			"Give users and groups access with `jumpcloud_application_association`.\n\n" +
			"Catalog templates have fewer settings than custom apps, and apply fails if you set one the template lacks. " +
			"Settings you leave out keep their current values, such as ones set in the admin console.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{MarkdownDescription: "Application ID.", Computed: true, PlanModifiers: keep},
			"display_label": schema.StringAttribute{
				MarkdownDescription: "Name shown in the admin console and the user portal.",
				Required:            true,
			},
			"template": schema.StringAttribute{
				MarkdownDescription: "Catalog template name: `saml2` for a custom app, or the name of a catalog app. Changing it replaces the application.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(client.SAMLTemplateCustom),
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"show_in_portal": schema.BoolAttribute{
				MarkdownDescription: "Whether the application appears in users' JumpCloud portal.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"sso_url": schema.StringAttribute{
				MarkdownDescription: "IdP single sign-on URL. Defaults to `https://sso.jumpcloud.com/saml2/` followed by the display label in lowercase " +
					"with dashes, and stays the same when the label changes. JumpCloud does not check that it is unique.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: keep,
			},
			"sp_entity_id": schema.StringAttribute{
				MarkdownDescription: "Service provider entity ID (audience).",
				Required:            true,
			},
			"acs_urls": schema.ListAttribute{
				MarkdownDescription: "Assertion consumer service URLs. The first one is the default.",
				Required:            true,
				ElementType:         types.StringType,
			},
			"idp_entity_id": optionalString("IdP entity ID JumpCloud sends to the service provider. When not set on create, " +
				"custom apps use the display label in lowercase with dashes, and catalog apps use their template's value."),
			"idp_init_url":        optionalString("URL users are sent to when they open the app from the JumpCloud portal."),
			"default_relay_state": optionalString("Default relay state, the URL users land on after an IdP-initiated sign-in."),
			"name_id":             optionalString("User attribute sent as the SAML subject NameID, such as `email` or `username`."),
			"name_id_format":      optionalString("NameID format, such as `urn:oasis:names:tc:SAML:1.1:nameid-format:emailAddress`."),
			"sign_response":       optionalBool("Whether JumpCloud signs the SAML response."),
			"sign_assertion":      optionalBool("Whether JumpCloud signs the SAML assertion."),
			"groups_attribute": optionalString("Name of the assertion attribute that lists the user's groups bound to the application; " +
				"`\"\"` stops sending groups."),
			"constant_attributes": optionalMap("Attributes with the same value for every user, by attribute name."),
			"user_attributes":     optionalMap("Attributes taken from the user, from attribute name to JumpCloud user field such as `firstname` or `email`."),
			"idp_certificate": schema.StringAttribute{
				MarkdownDescription: "IdP signing certificate in PEM, generated by JumpCloud when the application is created.",
				Computed:            true,
				PlanModifiers:       keep,
			},
		},
	}
}

func (r *samlApplicationResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var urls types.List
	var ssoURL types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("acs_urls"), &urls)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("sso_url"), &ssoURL)...)
	if !urls.IsNull() && !urls.IsUnknown() && len(urls.Elements()) == 0 {
		resp.Diagnostics.AddAttributeError(path.Root("acs_urls"), "Missing ACS URL", "Set at least one assertion consumer service URL.")
	}
	if v := ssoURL.ValueString(); !ssoURL.IsUnknown() && !ssoURL.IsNull() && !strings.HasPrefix(v, "https://") {
		resp.Diagnostics.AddAttributeError(path.Root("sso_url"), "Invalid SSO URL", fmt.Sprintf("Expected an https:// URL, got %q.", v))
	}
}

// ModifyPlan rejects settings the template does not have at plan time. Failing on apply
// could come after Terraform already destroyed apps in the same run.
func (r *samlApplicationResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || r.client == nil {
		return
	}
	var plan, config samlApplicationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() || plan.Template.IsUnknown() {
		return
	}

	var supports func(string) bool
	if !req.State.Raw.IsNull() {
		var state samlApplicationModel
		resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
		if resp.Diagnostics.HasError() {
			return
		}
		if state.Template.Equal(plan.Template) {
			// State holds every setting the template has and null for the rest.
			supported := map[string]bool{}
			for _, s := range setSettings(state) {
				supported[s.key] = true
			}
			supports = func(key string) bool { return supported[key] }
		}
	}
	if supports == nil {
		template, err := r.client.GetSAMLTemplate(ctx, plan.Template.ValueString())
		if err != nil {
			resp.Diagnostics.AddAttributeError(path.Root("template"), "Unknown JumpCloud SAML template", err.Error())
			return
		}
		supports = template.Supports
	}
	for _, s := range setSettings(config) {
		if !supports(s.key) {
			resp.Diagnostics.AddAttributeError(path.Root(s.attr), "Setting not supported by template",
				fmt.Sprintf("JumpCloud template %q has no %s setting.", plan.Template.ValueString(), s.attr))
		}
	}
}

type samlSetting struct{ attr, key string }

// setSettings lists the optional settings that are not null in a model.
func setSettings(config samlApplicationModel) []samlSetting {
	var out []samlSetting
	for _, s := range samlStringSettings {
		if !s.get(&config).IsNull() {
			out = append(out, samlSetting{s.attr, s.key})
		}
	}
	for _, s := range samlBoolSettings {
		if !s.get(&config).IsNull() {
			out = append(out, samlSetting{s.attr, s.key})
		}
	}
	if !config.GroupsAttribute.IsNull() {
		out = append(out, samlSetting{"groups_attribute", client.SAMLIncludeGroups})
	}
	for _, s := range samlAttributeSettings {
		if !s.get(&config).IsNull() {
			out = append(out, samlSetting{s.attr, s.key})
		}
	}
	return out
}

func (r *samlApplicationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerClient(req.ProviderData, &resp.Diagnostics)
}

func (r *samlApplicationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan, config samlApplicationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	template, err := r.client.GetSAMLTemplate(ctx, plan.Template.ValueString())
	if err != nil {
		resp.Diagnostics.AddAttributeError(path.Root("template"), "Unknown JumpCloud SAML template", err.Error())
		return
	}
	settings := samlSettings(ctx, plan, config, template.Supports, template.Name, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	// Service providers reject an empty issuer, which is the custom template's default.
	if _, set := settings[client.SAMLIdPEntityID]; !set && template.Supports(client.SAMLIdPEntityID) &&
		configString(template.Defaults[client.SAMLIdPEntityID]) == "" {
		settings[client.SAMLIdPEntityID] = slug(plan.DisplayLabel.ValueString())
	}
	ssoURL := plan.SSOURL.ValueString()
	if config.SSOURL.IsNull() {
		ssoURL = samlSSOURLPrefix + slug(plan.DisplayLabel.ValueString())
	}

	app, err := r.client.CreateSAMLApplication(ctx, *template, plan.DisplayLabel.ValueString(), ssoURL, !plan.ShowInPortal.ValueBool(), settings)
	if err != nil {
		resp.Diagnostics.AddError("Error creating JumpCloud SAML application", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, newSAMLApplicationModel(ctx, *app, &resp.Diagnostics))...)
}

func (r *samlApplicationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state samlApplicationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	app, err := r.client.GetSAMLApplication(ctx, state.ID.ValueString())
	if errors.Is(err, client.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading JumpCloud SAML application", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, newSAMLApplicationModel(ctx, *app, &resp.Diagnostics))...)
}

func (r *samlApplicationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, config samlApplicationModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := plan.ID.ValueString()
	current, err := r.client.GetSAMLApplication(ctx, id)
	if err != nil {
		resp.Diagnostics.AddError("Error reading JumpCloud SAML application", err.Error())
		return
	}
	settings := samlSettings(ctx, plan, config, current.Supports, current.Template, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	app, err := r.client.UpdateSAMLApplication(ctx, id, plan.DisplayLabel.ValueString(), plan.SSOURL.ValueString(), !plan.ShowInPortal.ValueBool(), settings)
	if err != nil {
		resp.Diagnostics.AddError("Error updating JumpCloud SAML application", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, newSAMLApplicationModel(ctx, *app, &resp.Diagnostics))...)
}

func (r *samlApplicationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state samlApplicationModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteApplication(ctx, state.ID.ValueString()); err != nil && !errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Error deleting JumpCloud SAML application", err.Error())
	}
}

func (r *samlApplicationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// samlSettings returns the config values to write: the required ones, and the optional
// ones that are configured. A setting the template does not support is an error.
func samlSettings(ctx context.Context, plan, config samlApplicationModel, supports func(string) bool, template string, diags *diag.Diagnostics) map[string]any {
	var urls []string
	diags.Append(plan.ACSURLs.ElementsAs(ctx, &urls, false)...)
	out := map[string]any{
		client.SAMLSPEntityID: plan.SPEntityID.ValueString(),
		client.SAMLACSURL:     client.ACSURLValue(urls),
	}
	set := func(attr, key string, value any) {
		if !supports(key) {
			diags.AddAttributeError(path.Root(attr), "Setting not supported by template",
				fmt.Sprintf("JumpCloud template %q has no %s setting.", template, attr))
			return
		}
		out[key] = value
	}
	for _, s := range samlStringSettings {
		if !s.get(&config).IsNull() {
			set(s.attr, s.key, s.get(&plan).ValueString())
		}
	}
	for _, s := range samlBoolSettings {
		if !s.get(&config).IsNull() {
			set(s.attr, s.key, s.get(&plan).ValueBool())
		}
	}
	if !config.GroupsAttribute.IsNull() {
		name := plan.GroupsAttribute.ValueString()
		set("groups_attribute", client.SAMLIncludeGroups, name != "")
		set("groups_attribute", client.SAMLGroupsAttribute, name)
	}
	for _, s := range samlAttributeSettings {
		if s.get(&config).IsNull() {
			continue
		}
		var values map[string]string
		diags.Append(s.get(&plan).ElementsAs(ctx, &values, false)...)
		attrs := []client.SAMLAttribute{}
		for _, name := range slices.Sorted(maps.Keys(values)) {
			attrs = append(attrs, client.SAMLAttribute{Name: name, Value: values[name]})
		}
		set(s.attr, s.key, attrs)
	}
	return out
}

// newSAMLApplicationModel builds state from an application. Settings its template does
// not have are null.
func newSAMLApplicationModel(ctx context.Context, app client.SAMLApplication, diags *diag.Diagnostics) samlApplicationModel {
	m := samlApplicationModel{
		ID:             types.StringValue(app.ID),
		DisplayLabel:   types.StringValue(app.DisplayLabel),
		Template:       types.StringValue(app.Template),
		ShowInPortal:   types.BoolValue(!app.Hidden),
		SSOURL:         types.StringValue(app.SSOURL),
		SPEntityID:     types.StringValue(configString(app.Config[client.SAMLSPEntityID])),
		IdPCertificate: types.StringValue(app.Certificate()),
	}
	urls := client.ACSURLs(app.Config[client.SAMLACSURL])
	if urls == nil {
		urls = []string{}
	}
	var d diag.Diagnostics
	m.ACSURLs, d = types.ListValueFrom(ctx, types.StringType, urls)
	diags.Append(d...)

	for _, s := range samlStringSettings {
		*s.get(&m) = types.StringNull()
		if app.Supports(s.key) {
			*s.get(&m) = types.StringValue(configString(app.Config[s.key]))
		}
	}
	for _, s := range samlBoolSettings {
		*s.get(&m) = types.BoolNull()
		if app.Supports(s.key) {
			var v bool
			_ = json.Unmarshal(app.Config[s.key], &v)
			*s.get(&m) = types.BoolValue(v)
		}
	}
	m.GroupsAttribute = types.StringNull()
	if app.Supports(client.SAMLIncludeGroups) {
		var include bool
		_ = json.Unmarshal(app.Config[client.SAMLIncludeGroups], &include)
		name := ""
		if include {
			name = configString(app.Config[client.SAMLGroupsAttribute])
		}
		m.GroupsAttribute = types.StringValue(name)
	}
	for _, s := range samlAttributeSettings {
		*s.get(&m) = types.MapNull(types.StringType)
		if !app.Supports(s.key) {
			continue
		}
		var attrs []client.SAMLAttribute
		_ = json.Unmarshal(app.Config[s.key], &attrs)
		values := make(map[string]string, len(attrs))
		for _, a := range attrs {
			values[a.Name] = a.Value
		}
		*s.get(&m), d = types.MapValueFrom(ctx, types.StringType, values)
		diags.Append(d...)
	}
	return m
}

// configString decodes a string setting; JumpCloud stores some unset ones as null.
func configString(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// slug turns a display label into the last part of the default SSO URL.
func slug(label string) string {
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(label), "-"), "-")
}
