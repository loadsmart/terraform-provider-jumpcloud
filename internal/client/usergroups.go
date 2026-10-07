package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// UserGroup is a JumpCloud user group (v2 API).
type UserGroup struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name"`
	// Description is accepted on create; the v2 spec omits it from responses and PUT,
	// so acceptance tests must confirm it round-trips.
	Description string `json:"description"`
}

// CreateUserGroup creates a user group and returns it with its ID.
func (c *Client) CreateUserGroup(ctx context.Context, g UserGroup) (*UserGroup, error) {
	g.ID = ""
	var out UserGroup
	if err := c.do(ctx, http.MethodPost, "/api/v2/usergroups", nil, g, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetUserGroup returns ErrNotFound when the group does not exist.
func (c *Client) GetUserGroup(ctx context.Context, id string) (*UserGroup, error) {
	var out UserGroup
	if err := c.do(ctx, http.MethodGet, "/api/v2/usergroups/"+url.PathEscape(id), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateUserGroup replaces the group's name and description.
func (c *Client) UpdateUserGroup(ctx context.Context, id string, g UserGroup) (*UserGroup, error) {
	g.ID = ""
	var out UserGroup
	if err := c.do(ctx, http.MethodPut, "/api/v2/usergroups/"+url.PathEscape(id), nil, g, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteUserGroup returns ErrNotFound when the group is already gone.
func (c *Client) DeleteUserGroup(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/v2/usergroups/"+url.PathEscape(id), nil, nil, nil)
}

// ListUserGroups returns every user group in the organization.
func (c *Client) ListUserGroups(ctx context.Context) ([]UserGroup, error) {
	return listV2[UserGroup](ctx, c, "/api/v2/usergroups", nil)
}

// FindUserGroupsByName returns groups whose name equals name exactly. JumpCloud's
// filter values accept wildcards, so results are re-checked for an exact match.
func (c *Client) FindUserGroupsByName(ctx context.Context, name string) ([]UserGroup, error) {
	groups, err := listV2[UserGroup](ctx, c, "/api/v2/usergroups", url.Values{"filter": {"name:eq:" + name}})
	if err != nil {
		return nil, err
	}
	var exact []UserGroup
	for _, g := range groups {
		if g.Name == name {
			exact = append(exact, g)
		}
	}
	return exact, nil
}

// AddUserToGroup makes the user a direct member of the group.
func (c *Client) AddUserToGroup(ctx context.Context, groupID, userID string) error {
	return c.memberOp(ctx, "add", groupID, userID)
}

// RemoveUserFromGroup removes the user's direct membership.
func (c *Client) RemoveUserFromGroup(ctx context.Context, groupID, userID string) error {
	return c.memberOp(ctx, "remove", groupID, userID)
}

func (c *Client) memberOp(ctx context.Context, op, groupID, userID string) error {
	path := "/api/v2/usergroups/" + url.PathEscape(groupID) + "/members"
	return c.do(ctx, http.MethodPost, path, nil, graphOperation{Op: op, Type: "user", ID: userID}, nil)
}

// UserGroupIDs returns the IDs of groups the user belongs to directly. The memberof
// endpoint also reports inherited memberships; a direct one has a single-hop path.
func (c *Client) UserGroupIDs(ctx context.Context, userID string) ([]string, error) {
	type graphObjectWithPaths struct {
		ID    string              `json:"id"`
		Type  string              `json:"type"`
		Paths [][]json.RawMessage `json:"paths"`
	}
	objs, err := listV2[graphObjectWithPaths](ctx, c, "/api/v2/users/"+url.PathEscape(userID)+"/memberof", nil)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, o := range objs {
		if o.Type != "user_group" {
			continue
		}
		for _, p := range o.Paths {
			if len(p) == 1 {
				ids = append(ids, o.ID)
				break
			}
		}
	}
	return ids, nil
}
