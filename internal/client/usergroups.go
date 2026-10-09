package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// UserGroup is a JumpCloud user group (v2 API).
type UserGroup struct {
	ID                      string              `json:"id,omitempty"`
	Name                    string              `json:"name"`
	Description             string              `json:"description"`
	Email                   string              `json:"email"`
	MembershipMethod        string              `json:"membershipMethod"`
	MemberQuery             json.RawMessage     `json:"memberQuery"`
	MemberQueryExemptions   []exemption         `json:"memberQueryExemptions"`
	MemberSuggestionsNotify bool                `json:"memberSuggestionsNotify"`
	MemberQueryErrorFlags   []string            `json:"memberQueryErrorFlags"`
	Attributes              UserGroupAttributes `json:"attributes"`
}

// Membership methods the provider sets. Dynamic methods share the DYNAMIC prefix.
const (
	MembershipStatic                = "STATIC"
	MembershipDynamicAutomated      = "DYNAMIC_AUTOMATED"
	MembershipDynamicReviewRequired = "DYNAMIC_REVIEW_REQUIRED"
)

// Dynamic reports whether a membership rule decides the group's members.
func (g UserGroup) Dynamic() bool { return strings.HasPrefix(g.MembershipMethod, "DYNAMIC") }

// ExemptUserIDs returns the users the membership rule does not decide for.
func (g UserGroup) ExemptUserIDs() []string {
	var ids []string
	for _, e := range g.MemberQueryExemptions {
		if e.Type == exemptionTypeUser {
			ids = append(ids, e.ID)
		}
	}
	return ids
}

// exemptionTypeUser must be uppercase; JumpCloud rejects "user" here.
const exemptionTypeUser = "USER"

type exemption struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

// UserGroupAttributes are the group attributes the provider manages.
type UserGroupAttributes struct {
	Sudo         *Sudo        `json:"sudo"`
	Radius       *Radius      `json:"radius"`
	SambaEnabled bool         `json:"sambaEnabled"`
	LDAPGroups   []LDAPGroup  `json:"ldapGroups"`
	POSIXGroups  []POSIXGroup `json:"posixGroups"`
}

type Sudo struct {
	Enabled         bool `json:"enabled"`
	WithoutPassword bool `json:"withoutPassword"`
}

type Radius struct {
	Reply []RadiusReply `json:"reply"`
}

type RadiusReply struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type LDAPGroup struct {
	Name string `json:"name"`
}

type POSIXGroup struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// MemberRule makes a group dynamic.
type MemberRule struct {
	Query          json.RawMessage // v2 query: {"filters": [...]}
	ReviewRequired bool
	Notify         bool
	ExemptUserIDs  []string
}

// UserGroupChange is what the provider sets on a user group. On update, fields it does
// not mention are sent back unchanged.
type UserGroupChange struct {
	Name        string
	Description string
	Email       *string     // nil keeps the current email
	Rule        *MemberRule // nil makes a dynamic group static
	// Attributes replaces the named attributes; a nil value removes one. Others are kept.
	Attributes map[string]any
}

// applyTo writes the change into a user group request body.
func (ch UserGroupChange) applyTo(body map[string]json.RawMessage) error {
	set := map[string]any{"name": ch.Name, "description": ch.Description}
	if ch.Email != nil {
		set["email"] = *ch.Email
	}

	// On create the body is empty, so the method stays "" and the group counts as static.
	var current UserGroup
	_ = json.Unmarshal(body["membershipMethod"], &current.MembershipMethod)
	switch {
	case ch.Rule != nil:
		exemptions := make([]exemption, 0, len(ch.Rule.ExemptUserIDs))
		for _, id := range ch.Rule.ExemptUserIDs {
			exemptions = append(exemptions, exemption{ID: id, Type: exemptionTypeUser})
		}
		set["membershipMethod"] = MembershipDynamicAutomated
		if ch.Rule.ReviewRequired {
			set["membershipMethod"] = MembershipDynamicReviewRequired
		}
		set["memberSuggestionsNotify"] = ch.Rule.Notify
		set["memberQuery"] = ch.Rule.Query
		set["memberQueryExemptions"] = exemptions
	case current.Dynamic():
		// Setting STATIC alone keeps the rule and exemptions on the group.
		set["membershipMethod"] = MembershipStatic
		set["memberQuery"] = nil
		set["memberQueryExemptions"] = []exemption{}
	}

	if len(ch.Attributes) > 0 {
		var attrs map[string]json.RawMessage
		if raw, ok := body["attributes"]; ok {
			if err := json.Unmarshal(raw, &attrs); err != nil {
				return err
			}
		}
		if attrs == nil {
			attrs = map[string]json.RawMessage{}
		}
		for k, v := range ch.Attributes {
			if v == nil {
				delete(attrs, k)
				continue
			}
			raw, err := json.Marshal(v)
			if err != nil {
				return err
			}
			attrs[k] = raw
		}
		set["attributes"] = attrs
	}

	for k, v := range set {
		raw, err := json.Marshal(v)
		if err != nil {
			return err
		}
		body[k] = raw
	}
	return nil
}

// queryDSLHeader returns the header JumpCloud requires to write a member query in the v2
// format, which has no queryType. Without it the write fails with "memberQuery.queryType
// is required"; v1 queries, which have a queryType, are sent without it.
func queryDSLHeader(query json.RawMessage) http.Header {
	var q map[string]json.RawMessage
	if json.Unmarshal(query, &q) != nil || q == nil {
		return nil
	}
	if _, v1 := q["queryType"]; v1 {
		return nil
	}
	return http.Header{"X-Query-Dsl": {"v2"}}
}

// readOnlyUserGroupFields appear in UserGroup responses but not in the UserGroupPut schema.
var readOnlyUserGroupFields = []string{"id", "type", "organizationObjectId", "suggestionCounts", "memberQueryErrorFlags"}

// CreateUserGroup creates a user group and returns it with its ID.
func (c *Client) CreateUserGroup(ctx context.Context, ch UserGroupChange) (*UserGroup, error) {
	body := map[string]json.RawMessage{}
	if err := ch.applyTo(body); err != nil {
		return nil, fmt.Errorf("jumpcloud: encoding user group: %w", err)
	}
	var out UserGroup
	if err := c.doWithHeader(ctx, http.MethodPost, "/api/v2/usergroups", nil, queryDSLHeader(body["memberQuery"]), body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetUserGroup returns ErrNotFound when the group does not exist.
func (c *Client) GetUserGroup(ctx context.Context, id string) (*UserGroup, error) {
	return get[UserGroup](ctx, c, "/api/v2/usergroups/"+url.PathEscape(id))
}

// UpdateUserGroup applies the change. PUT is a full replace, so the current group is
// read first and every field the change does not mention is sent back unchanged.
func (c *Client) UpdateUserGroup(ctx context.Context, id string, ch UserGroupChange) (*UserGroup, error) {
	path := "/api/v2/usergroups/" + url.PathEscape(id)
	var current map[string]json.RawMessage
	if err := c.do(ctx, http.MethodGet, path, nil, nil, &current); err != nil {
		return nil, err
	}
	for _, k := range readOnlyUserGroupFields {
		delete(current, k)
	}
	if err := ch.applyTo(current); err != nil {
		return nil, fmt.Errorf("jumpcloud: encoding user group %s: %w", id, err)
	}

	var out UserGroup
	if err := c.doWithHeader(ctx, http.MethodPut, path, nil, queryDSLHeader(current["memberQuery"]), current, &out); err != nil {
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
	return list[UserGroup](ctx, c, "/api/v2/usergroups", nil, false)
}

// FindUserGroupsByName returns groups whose name equals name exactly. JumpCloud's
// filter values accept wildcards, so results are re-checked for an exact match. The
// filter parameter is comma-separated, so names with commas are matched client-side.
func (c *Client) FindUserGroupsByName(ctx context.Context, name string) ([]UserGroup, error) {
	query := url.Values{"filter": {"name:eq:" + name}}
	if strings.Contains(name, ",") {
		query = nil
	}
	groups, err := list[UserGroup](ctx, c, "/api/v2/usergroups", query, false)
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

// UserGroupMemberIDs returns the IDs of the group's direct user members.
func (c *Client) UserGroupMemberIDs(ctx context.Context, groupID string) ([]string, error) {
	type graphConnection struct {
		To struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"to"`
	}
	conns, err := list[graphConnection](ctx, c, "/api/v2/usergroups/"+url.PathEscape(groupID)+"/members", nil, false)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, conn := range conns {
		if conn.To.Type == "user" {
			ids = append(ids, conn.To.ID)
		}
	}
	return ids, nil
}

// UserGroupIDs returns the IDs of groups the user belongs to directly. The memberof
// endpoint also reports inherited memberships; a direct one has a single-hop path.
func (c *Client) UserGroupIDs(ctx context.Context, userID string) ([]string, error) {
	type graphObjectWithPaths struct {
		ID    string              `json:"id"`
		Type  string              `json:"type"`
		Paths [][]json.RawMessage `json:"paths"`
	}
	objs, err := list[graphObjectWithPaths](ctx, c, "/api/v2/users/"+url.PathEscape(userID)+"/memberof", nil, false)
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
