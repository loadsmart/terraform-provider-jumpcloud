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
