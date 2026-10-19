package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/loadsmart/terraform-provider-jumpcloud/internal/client"
)

var (
	_ resource.ResourceWithConfigure   = &applicationLogoResource{}
	_ resource.ResourceWithImportState = &applicationLogoResource{}
	_ resource.ResourceWithModifyPlan  = &applicationLogoResource{}
)

func NewApplicationLogoResource() resource.Resource { return &applicationLogoResource{} }

type applicationLogoResource struct{ client *client.Client }

type applicationLogoModel struct {
	ID            types.String `tfsdk:"id"`
	ApplicationID types.String `tfsdk:"application_id"`
	File          types.String `tfsdk:"file"`
	SHA256        types.String `tfsdk:"sha256"`
	URL           types.String `tfsdk:"url"`
}

func (r *applicationLogoResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_application_logo"
}

func (r *applicationLogoResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Uploads the logo shown for an application in the admin console and the user portal. " +
			"Works for any application type. Destroying it puts back the template's default icon.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Same as `application_id`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"application_id": schema.StringAttribute{
				MarkdownDescription: "ID of the application.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"file": schema.StringAttribute{
				MarkdownDescription: "Path to a PNG or JPEG file, such as `\"${path.module}/logo.png\"`. " +
					"The logo is uploaded again when the file's content changes. JumpCloud shows it in a square tile, so use a square image.",
				Required: true,
			},
			"sha256": schema.StringAttribute{
				MarkdownDescription: "SHA-256 of the uploaded file, used to detect changes.",
				Computed:            true,
			},
			"url": schema.StringAttribute{
				MarkdownDescription: "URL JumpCloud serves the logo from.",
				Computed:            true,
			},
		},
	}
}

func (r *applicationLogoResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerClient(req.ProviderData, &resp.Diagnostics)
}

// ModifyPlan hashes the file, so a changed image shows up in the plan even when the
// path stays the same.
func (r *applicationLogoResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() {
		return
	}
	var plan applicationLogoModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || plan.File.IsUnknown() {
		return
	}
	_, sum := readLogo(plan.File.ValueString(), &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	var state applicationLogoModel
	if !req.State.Raw.IsNull() {
		resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	}
	plan.SHA256 = types.StringValue(sum)
	if state.SHA256.ValueString() == sum && !state.URL.IsNull() {
		plan.URL = state.URL
	} else {
		plan.URL = types.StringUnknown()
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, plan)...)
}

func (r *applicationLogoResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan applicationLogoModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.upload(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *applicationLogoResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state applicationLogoModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	logo, err := r.client.ApplicationLogoURL(ctx, state.ApplicationID.ValueString())
	// A template icon means the logo was removed in the admin console; upload it again.
	if errors.Is(err, client.ErrNotFound) || (err == nil && !client.IsUploadedLogo(logo)) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error reading JumpCloud application logo", err.Error())
		return
	}
	state.ID = state.ApplicationID
	state.URL = types.StringValue(logo)
	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

func (r *applicationLogoResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state applicationLogoModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Only the path changed: the logo in JumpCloud is already this image.
	if plan.SHA256.Equal(state.SHA256) {
		plan.URL = state.URL
	} else {
		r.upload(ctx, &plan, &resp.Diagnostics)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, plan)...)
}

func (r *applicationLogoResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state applicationLogoModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.DeleteApplicationLogo(ctx, state.ApplicationID.ValueString())
	if err != nil && !errors.Is(err, client.ErrNotFound) {
		resp.Diagnostics.AddError("Error deleting JumpCloud application logo", err.Error())
	}
}

// ImportState takes the application ID. The next plan uploads the configured file once,
// because the image already in JumpCloud cannot be compared with it.
func (r *applicationLogoResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("application_id"), req, resp)
}

func (r *applicationLogoResource) upload(ctx context.Context, m *applicationLogoModel, diags *diag.Diagnostics) {
	id := m.ApplicationID.ValueString()
	image, sum := readLogo(m.File.ValueString(), diags)
	if diags.HasError() {
		return
	}
	err := r.client.UploadApplicationLogo(ctx, id, filepath.Base(m.File.ValueString()), image)
	if errors.Is(err, client.ErrNotFound) {
		diags.AddError("Error uploading JumpCloud application logo", fmt.Sprintf("Application %s does not exist.", id))
		return
	}
	if err != nil {
		diags.AddError("Error uploading JumpCloud application logo", err.Error())
		return
	}
	logo, err := r.client.ApplicationLogoURL(ctx, id)
	if err != nil {
		diags.AddError("Error reading JumpCloud application logo", err.Error())
		return
	}
	m.ID = m.ApplicationID
	m.SHA256 = types.StringValue(sum)
	m.URL = types.StringValue(logo)
}

// readLogo reads a PNG or JPEG file and returns it with its SHA-256.
func readLogo(file string, diags *diag.Diagnostics) ([]byte, string) {
	image, err := os.ReadFile(file)
	if err != nil {
		diags.AddAttributeError(path.Root("file"), "Cannot read logo file", err.Error())
		return nil, ""
	}
	if t := http.DetectContentType(image); t != "image/png" && t != "image/jpeg" {
		diags.AddAttributeError(path.Root("file"), "Unsupported logo file", fmt.Sprintf("%s is %s; JumpCloud accepts PNG or JPEG.", file, t))
		return nil, ""
	}
	sum := sha256.Sum256(image)
	return image, hex.EncodeToString(sum[:])
}
