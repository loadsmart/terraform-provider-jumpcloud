package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

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

// isUserGroupNotFound reports the 400 JumpCloud returns when changing members of a
// group that does not exist.
func isUserGroupNotFound(err error) bool {
	var apiErr *client.APIError
	return isStatus(err, http.StatusBadRequest) && errors.As(err, &apiErr) && strings.Contains(apiErr.Message, "user_group not found")
}

func setToStrings(ctx context.Context, s types.Set, diags *diag.Diagnostics) []string {
	var out []string
	diags.Append(s.ElementsAs(ctx, &out, false)...)
	return out
}

// without returns the items of a that are not in b.
func without(a, b []string) []string {
	return slices.DeleteFunc(slices.Clone(a), func(id string) bool { return slices.Contains(b, id) })
}

// stringSet converts to a set, never null: a nil slice becomes an empty set.
func stringSet(ctx context.Context, items []string, diags *diag.Diagnostics) types.Set {
	if items == nil {
		items = []string{}
	}
	set, d := types.SetValueFrom(ctx, types.StringType, items)
	diags.Append(d...)
	return set
}

// userGroupModel is a user group as the data sources expose it.
type userGroupModel struct {
	ID               types.String `tfsdk:"id"`
	Name             types.String `tfsdk:"name"`
	Description      types.String `tfsdk:"description"`
	Email            types.String `tfsdk:"email"`
	MembershipMethod types.String `tfsdk:"membership_method"`
	Query            types.String `tfsdk:"query"`
}

// userGroupSummaryModel is what the group list returns: JumpCloud leaves out the
// membership fields there, so membership_method and query need jumpcloud_user_group.
type userGroupSummaryModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	Email       types.String `tfsdk:"email"`
}

func newUserGroupModel(g client.UserGroup) userGroupModel {
	query := ""
	if g.Dynamic() {
		query = string(g.MemberQuery)
	}
	return userGroupModel{
		ID:               types.StringValue(g.ID),
		Name:             types.StringValue(g.Name),
		Description:      types.StringValue(g.Description),
		Email:            types.StringValue(g.Email),
		MembershipMethod: types.StringValue(g.MembershipMethod),
		Query:            types.StringValue(query),
	}
}
