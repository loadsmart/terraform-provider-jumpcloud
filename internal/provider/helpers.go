package provider

import (
	"context"
	"errors"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/loadsmart/terraform-provider-jumpcloud/internal/client"
)

// providerClient returns the client built in the provider's Configure. Terraform calls
// resource and data source Configure before the provider is configured, with nil data.
func providerClient(data any, diags *diag.Diagnostics) *client.Client {
	if data == nil {
		return nil
	}
	c, ok := data.(*client.Client)
	if !ok {
		diags.AddError("Unexpected provider data", fmt.Sprintf("Expected *client.Client, got %T.", data))
	}
	return c
}

func isStatus(err error, code int) bool {
	var apiErr *client.APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == code
}

func setToStrings(ctx context.Context, s types.Set, diags *diag.Diagnostics) []string {
	var out []string
	diags.Append(s.ElementsAs(ctx, &out, false)...)
	return out
}

// userGroupModel is shared by the user group resource and both group data sources.
type userGroupModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
}

func newUserGroupModel(g client.UserGroup) userGroupModel {
	return userGroupModel{
		ID:          types.StringValue(g.ID),
		Name:        types.StringValue(g.Name),
		Description: types.StringValue(g.Description),
	}
}

// userModel is shared by the user and users data sources.
type userModel struct {
	ID         types.String `tfsdk:"id"`
	Email      types.String `tfsdk:"email"`
	Username   types.String `tfsdk:"username"`
	State      types.String `tfsdk:"state"`
	Department types.String `tfsdk:"department"`
	JobTitle   types.String `tfsdk:"job_title"`
	Attributes types.Map    `tfsdk:"attributes"`
}

func newUserModel(ctx context.Context, u client.User, diags *diag.Diagnostics) userModel {
	attrs := make(map[string]string, len(u.Attributes))
	for _, a := range u.Attributes {
		attrs[a.Name] = a.Value
	}
	attributes, d := types.MapValueFrom(ctx, types.StringType, attrs)
	diags.Append(d...)
	return userModel{
		ID:         types.StringValue(u.ID),
		Email:      types.StringValue(u.Email),
		Username:   types.StringValue(u.Username),
		State:      types.StringValue(u.State),
		Department: types.StringValue(u.Department),
		JobTitle:   types.StringValue(u.JobTitle),
		Attributes: attributes,
	}
}
