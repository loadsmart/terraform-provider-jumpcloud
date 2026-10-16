package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
)

// AuthnPolicy is a conditional access (authentication) policy, limited to the fields
// the provider manages. Other fields, such as MFA factors and custom error messages,
// are kept as they are on update.
type AuthnPolicy struct {
	ID          string
	Name        string
	Description string
	// Type is what the policy protects: user_portal, application, ldap, or admin_portal.
	Type        string
	Disabled    bool
	MonitorOnly bool
	// Action is allow or deny.
	Action      string
	MFARequired bool
	AllUsers    bool
	// UserGroupIDs and ExcludedUserGroupIDs select users by group.
	UserGroupIDs         []string
	ExcludedUserGroupIDs []string
	// ApplicationIDs limits an application policy to these apps; empty means every app.
	ApplicationIDs         []string
	ExcludedApplicationIDs []string
	// Conditions is the policy's condition expression, nil for none.
	Conditions json.RawMessage
}

type authnPolicyDocument struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Disabled    bool   `json:"disabled"`
	MonitorOnly bool   `json:"monitorOnly"`
	Type        string `json:"type"`
	Effect      struct {
		Action      string `json:"action"`
		Obligations struct {
			MFA struct {
				Required bool `json:"required"`
			} `json:"mfa"`
		} `json:"obligations"`
	} `json:"effect"`
	Targets struct {
		Resources []struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"resources"`
		Users struct {
			Inclusions []string `json:"inclusions"`
		} `json:"users"`
		UserGroups struct {
			Inclusions []string `json:"inclusions"`
			Exclusions []string `json:"exclusions"`
		} `json:"userGroups"`
		ExcludedApplications []string `json:"excludedApplications"`
	} `json:"targets"`
	Conditions json.RawMessage `json:"conditions"`
}

func (d authnPolicyDocument) policy() *AuthnPolicy {
	p := &AuthnPolicy{
		ID: d.ID, Name: d.Name, Description: d.Description, Type: d.Type, Disabled: d.Disabled, MonitorOnly: d.MonitorOnly,
		Action: d.Effect.Action, MFARequired: d.Effect.Obligations.MFA.Required,
		UserGroupIDs: d.Targets.UserGroups.Inclusions, ExcludedUserGroupIDs: d.Targets.UserGroups.Exclusions,
		ExcludedApplicationIDs: d.Targets.ExcludedApplications,
	}
	for _, u := range d.Targets.Users.Inclusions {
		if u == "all" {
			p.AllUsers = true
		}
	}
	// A resource without an ID targets every resource of its type.
	for _, r := range d.Targets.Resources {
		if r.ID != "" {
			p.ApplicationIDs = append(p.ApplicationIDs, r.ID)
		}
	}
	if !emptyJSONObject(d.Conditions) {
		p.Conditions = d.Conditions
	}
	return p
}

// emptyJSONObject reports whether raw is absent, null, or {}.
func emptyJSONObject(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return false
	}
	m, isObject := v.(map[string]any)
	return v == nil || (isObject && len(m) == 0)
}

func orEmpty(ids []string) []string {
	if ids == nil {
		return []string{}
	}
	return ids
}

// targets builds the targets document, keeping target keys the provider does not manage.
func (p AuthnPolicy) targets(current map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range current {
		out[k] = v
	}
	resources := []map[string]string{}
	for _, id := range p.ApplicationIDs {
		resources = append(resources, map[string]string{"type": p.Type, "id": id})
	}
	if len(resources) == 0 {
		resources = append(resources, map[string]string{"type": p.Type})
	}
	users := []string{}
	if p.AllUsers {
		users = []string{"all"}
	}
	out["resources"] = resources
	out["users"] = map[string]any{"inclusions": users}
	out["userGroups"] = map[string]any{"inclusions": orEmpty(p.UserGroupIDs), "exclusions": orEmpty(p.ExcludedUserGroupIDs)}
	if p.Type == "application" {
		out["excludedApplications"] = orEmpty(p.ExcludedApplicationIDs)
	} else {
		delete(out, "excludedApplications")
	}
	return out
}

// effect builds the effect document. Obligations other than mfa.required, such as the
// allowed MFA factors, are kept on allow policies; deny policies have none.
func (p AuthnPolicy) effect(current map[string]any) map[string]any {
	out := map[string]any{"action": p.Action}
	if p.Action != "allow" {
		return out
	}
	obligations, _ := current["obligations"].(map[string]any)
	if obligations == nil {
		obligations = map[string]any{}
	}
	mfa, _ := obligations["mfa"].(map[string]any)
	if mfa == nil {
		mfa = map[string]any{}
	}
	mfa["required"] = p.MFARequired
	obligations["mfa"] = mfa
	out["obligations"] = obligations
	return out
}

func (p AuthnPolicy) conditions() json.RawMessage {
	if p.Conditions == nil {
		return json.RawMessage(`{}`)
	}
	return p.Conditions
}

func authnPolicyPath(id string) string { return "/api/v2/authn/policies/" + url.PathEscape(id) }

// CreateAuthnPolicy creates a policy.
func (c *Client) CreateAuthnPolicy(ctx context.Context, p AuthnPolicy) (*AuthnPolicy, error) {
	body := map[string]any{
		"name": p.Name, "description": p.Description, "type": p.Type, "disabled": p.Disabled, "monitorOnly": p.MonitorOnly,
		"effect": p.effect(nil), "targets": p.targets(nil), "conditions": p.conditions(),
	}
	var created authnPolicyDocument
	if err := c.do(ctx, http.MethodPost, "/api/v2/authn/policies", nil, body, &created); err != nil {
		return nil, err
	}
	if created.ID == "" {
		return nil, errors.New("jumpcloud: POST /api/v2/authn/policies returned no policy ID")
	}
	// Read back so the result has what JumpCloud stored, not what was sent.
	return c.GetAuthnPolicy(ctx, created.ID)
}

// GetAuthnPolicy returns ErrNotFound when the policy does not exist.
func (c *Client) GetAuthnPolicy(ctx context.Context, id string) (*AuthnPolicy, error) {
	doc, err := get[authnPolicyDocument](ctx, c, authnPolicyPath(id))
	if err != nil {
		return nil, err
	}
	return doc.policy(), nil
}

// UpdateAuthnPolicy changes the managed fields. The current policy is read first and
// sent back with them, so fields the provider does not manage stay as they are.
func (c *Client) UpdateAuthnPolicy(ctx context.Context, id string, p AuthnPolicy) (*AuthnPolicy, error) {
	var current map[string]json.RawMessage
	if err := c.do(ctx, http.MethodGet, authnPolicyPath(id), nil, nil, &current); err != nil {
		return nil, err
	}
	var effect, targets map[string]any
	if err := decodeOptional(current["effect"], &effect); err != nil {
		return nil, fmt.Errorf("jumpcloud: decoding policy %s effect: %w", id, err)
	}
	if err := decodeOptional(current["targets"], &targets); err != nil {
		return nil, fmt.Errorf("jumpcloud: decoding policy %s targets: %w", id, err)
	}

	body := map[string]any{}
	for k, v := range current {
		body[k] = v
	}
	for _, readOnly := range []string{"id", "managedBy"} {
		delete(body, readOnly)
	}
	body["name"] = p.Name
	body["description"] = p.Description
	body["disabled"] = p.Disabled
	body["monitorOnly"] = p.MonitorOnly
	body["effect"] = p.effect(effect)
	body["targets"] = p.targets(targets)
	body["conditions"] = p.conditions()
	if err := c.do(ctx, http.MethodPatch, authnPolicyPath(id), nil, body, nil); err != nil {
		return nil, err
	}
	return c.GetAuthnPolicy(ctx, id)
}

// DeleteAuthnPolicy returns ErrNotFound when the policy is already gone.
func (c *Client) DeleteAuthnPolicy(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, authnPolicyPath(id), nil, nil, nil)
}

func decodeOptional(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}
