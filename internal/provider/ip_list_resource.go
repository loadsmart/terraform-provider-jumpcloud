package provider

import (
	"context"
	"errors"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/loadsmart/terraform-provider-jumpcloud/internal/client"
)

var (
	_ resource.ResourceWithConfigure   = &ipListResource{}
	_ resource.ResourceWithImportState = &ipListResource{}
)

func NewIPListResource() resource.Resource { return &ipListResource{} }

type ipListResource struct{ client *client.Client }

type ipListModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	IPs         types.Set    `tfsdk:"ips"`
}

func (r *ipListResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ip_list"
}

func (r *ipListResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a JumpCloud IP list, a named set of addresses that `jumpcloud_authentication_policy` " +
			"conditions refer to with `ipAddressIn`.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "IP list ID.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{MarkdownDescription: "IP list name.", Required: true},
			"description": schema.StringAttribute{
				MarkdownDescription: "IP list description.",
				Optional:            true,
				Computed:            true,
				Default:             stringdefault.StaticString(""),
			},
			"ips": schema.SetAttribute{
				MarkdownDescription: "IPv4 or IPv6 addresses and CIDR ranges, such as `203.0.113.7` or `198.51.100.0/24`.",
				Required:            true,
				ElementType:         types.StringType,
			},
		},
	}
}

func (r *ipListResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerClient(req.ProviderData, &resp.Diagnostics)
}

func (r *ipListResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ipListModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	l := plan.ipList(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	created, err := r.client.CreateIPList(ctx, l)
	if err != nil {
		resp.Diagnostics.AddError("Error creating JumpCloud IP list", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, newIPListModel(ctx, *created, &resp.Diagnostics))...)
}

func (r *ipListResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ipListModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	l, err := r.client.GetIPList(ctx, state.ID.ValueString())
	if errors.Is(err, client.ErrNotFound) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading JumpCloud IP list", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, newIPListModel(ctx, *l, &resp.Diagnostics))...)
}

func (r *ipListResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan ipListModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	l := plan.ipList(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	updated, err := r.client.ReplaceIPList(ctx, plan.ID.ValueString(), l)
	if err != nil {
		resp.Diagnostics.AddError("Error updating JumpCloud IP list", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, newIPListModel(ctx, *updated, &resp.Diagnostics))...)
}

func (r *ipListResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state ipListModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteIPList(ctx, state.ID.ValueString()); err != nil && !errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Error deleting JumpCloud IP list", err.Error())
	}
}

func (r *ipListResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (m ipListModel) ipList(ctx context.Context, diags *diag.Diagnostics) client.IPList {
	return client.IPList{Name: m.Name.ValueString(), Description: m.Description.ValueString(), IPs: setToStrings(ctx, m.IPs, diags)}
}

func newIPListModel(ctx context.Context, l client.IPList, diags *diag.Diagnostics) ipListModel {
	return ipListModel{
		ID:          types.StringValue(l.ID),
		Name:        types.StringValue(l.Name),
		Description: types.StringValue(l.Description),
		IPs:         stringSet(ctx, l.IPs, diags),
	}
}
