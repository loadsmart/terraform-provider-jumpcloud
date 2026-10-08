package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// Application is a JumpCloud SSO application (v1 API), limited to the fields the provider exposes.
type Application struct {
	ID           string `json:"_id"`
	Name         string `json:"name"`
	DisplayName  string `json:"displayName"`
	DisplayLabel string `json:"displayLabel"`
	SSOURL       string `json:"ssoUrl"`
}

// Association target types accepted by the application associations endpoint.
const (
	TargetUser      = "user"
	TargetUserGroup = "user_group"
)

// GetApplication returns ErrNotFound when the application does not exist.
func (c *Client) GetApplication(ctx context.Context, id string) (*Application, error) {
	return get[Application](ctx, c, "/api/applications/"+url.PathEscape(id))
}

// ListApplications returns applications whose fields equal every value in filters
// (e.g. {"displayLabel": "Grafana"}). An empty map returns every application.
func (c *Client) ListApplications(ctx context.Context, filters map[string]string) ([]Application, error) {
	return list[Application](ctx, c, "/api/applications", v1Filters(filters), true)
}

// AddApplicationAssociation binds a user or user group to an application.
func (c *Client) AddApplicationAssociation(ctx context.Context, appID, targetType, targetID string) error {
	return c.associationOp(ctx, "add", appID, targetType, targetID)
}

// RemoveApplicationAssociation unbinds a user or user group from an application.
func (c *Client) RemoveApplicationAssociation(ctx context.Context, appID, targetType, targetID string) error {
	return c.associationOp(ctx, "remove", appID, targetType, targetID)
}

func (c *Client) associationOp(ctx context.Context, op, appID, targetType, targetID string) error {
	path := "/api/v2/applications/" + url.PathEscape(appID) + "/associations"
	return c.do(ctx, http.MethodPost, path, nil, graphOperation{Op: op, Type: targetType, ID: targetID}, nil)
}

// ApplicationAssociationIDs returns the IDs of users or user groups bound directly to an application.
func (c *Client) ApplicationAssociationIDs(ctx context.Context, appID, targetType string) ([]string, error) {
	type graphConnection struct {
		To struct {
			ID   string `json:"id"`
			Type string `json:"type"`
		} `json:"to"`
	}
	path := "/api/v2/applications/" + url.PathEscape(appID) + "/associations"
	conns, err := list[graphConnection](ctx, c, path, url.Values{"targets": {targetType}}, false)
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(conns))
	for _, conn := range conns {
		if conn.To.Type == targetType {
			ids = append(ids, conn.To.ID)
		}
	}
	return ids, nil
}

// The OIDC endpoints below follow JumpCloud's SSO migration utility: the v1 create
// makes the app from the "oidc" template, and /api/v2/applications/{id}/sso holds its
// OIDC settings. The /sso endpoint is not in the published v2 spec.
const oidcTemplate = "oidc"

// OIDCApplication is a custom OIDC application and its SSO settings.
type OIDCApplication struct {
	ID           string
	DisplayLabel string
	// Hidden keeps the app off the user portal; users can still sign in through it.
	Hidden bool
	OIDC   OIDCSettings
}

// OIDCSettings are the OIDC fields the provider manages. JumpCloud returns ClientSecret
// only when the settings are first created.
type OIDCSettings struct {
	ClientID                string      `json:"clientId,omitempty"`
	ClientSecret            string      `json:"clientSecret,omitempty"`
	Consent                 string      `json:"consent,omitempty"`
	RedirectURIs            []string    `json:"redirectUris"`
	GrantTypes              []string    `json:"grantTypes"`
	TokenEndpointAuthMethod string      `json:"tokenEndpointAuthMethod"`
	RelyingPartyURL         string      `json:"relyingPartyUrl"`
	DynamicClaims           []OIDCClaim `json:"dynamicClaims"`
}

// OIDCClaim maps a token claim name to a JumpCloud user attribute.
type OIDCClaim struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type ssoSettings struct {
	Hidden bool         `json:"hidden"`
	OIDC   OIDCSettings `json:"oidc"`
}

// CreateOIDCApplication creates the app and its OIDC settings. If the settings fail, the
// app is deleted so a retry starts clean.
func (c *Client) CreateOIDCApplication(ctx context.Context, a OIDCApplication) (*OIDCApplication, error) {
	var created Application
	if err := c.do(ctx, http.MethodPost, "/api/applications", nil, oidcAppBody(a.DisplayLabel), &created); err != nil {
		return nil, err
	}

	settings := a.OIDC
	settings.Consent = "trusted"
	body := map[string]any{"type": oidcTemplate, "hidden": a.Hidden, "oidc": settings}
	var sso ssoSettings
	if err := c.do(ctx, http.MethodPost, ssoPath(created.ID), nil, body, &sso); err != nil {
		if delErr := c.DeleteApplication(ctx, created.ID); delErr != nil {
			return nil, fmt.Errorf("%w (and deleting the half-created application %s failed: %v)", err, created.ID, delErr)
		}
		return nil, err
	}
	return &OIDCApplication{ID: created.ID, DisplayLabel: a.DisplayLabel, Hidden: sso.Hidden, OIDC: sso.OIDC}, nil
}

// GetOIDCApplication returns ErrNotFound when the app or its OIDC settings do not exist.
func (c *Client) GetOIDCApplication(ctx context.Context, id string) (*OIDCApplication, error) {
	app, err := c.GetApplication(ctx, id)
	if err != nil {
		return nil, err
	}
	sso, err := get[ssoSettings](ctx, c, ssoPath(id))
	if err != nil {
		return nil, err
	}
	return &OIDCApplication{ID: id, DisplayLabel: app.DisplayLabel, Hidden: sso.Hidden, OIDC: sso.OIDC}, nil
}

// RenameApplication changes the label shown in the console and user portal. It does not
// touch the OIDC settings.
func (c *Client) RenameApplication(ctx context.Context, id, displayLabel string) error {
	return c.do(ctx, http.MethodPut, "/api/applications/"+url.PathEscape(id), nil, oidcAppBody(displayLabel), nil)
}

// UpdateOIDCSettings changes the managed OIDC fields. PUT replaces the whole OIDC object,
// so the current settings are read first and fields set elsewhere, such as token
// lifetimes, are sent back unchanged. The client ID and secret stay the same.
func (c *Client) UpdateOIDCSettings(ctx context.Context, id string, hidden bool, s OIDCSettings) error {
	var current struct {
		OIDC map[string]any `json:"oidc"`
	}
	if err := c.do(ctx, http.MethodGet, ssoPath(id), nil, nil, &current); err != nil {
		return err
	}
	for _, readOnly := range []string{"clientId", "clientSecret", "metadata"} {
		delete(current.OIDC, readOnly)
	}
	// Overlay the managed fields; omitempty leaves the client ID, secret, and consent as they are.
	raw, _ := json.Marshal(s) // strings and slices always marshal
	if err := json.Unmarshal(raw, &current.OIDC); err != nil {
		return err
	}

	body := map[string]any{"type": oidcTemplate, "hidden": hidden, "oidc": current.OIDC}
	return c.do(ctx, http.MethodPut, ssoPath(id), nil, body, nil)
}

// DeleteApplication returns ErrNotFound when the application is already gone.
func (c *Client) DeleteApplication(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, "/api/applications/"+url.PathEscape(id), nil, nil, nil)
}

func oidcAppBody(displayLabel string) map[string]any {
	// ssoUrl must be absent: JumpCloud rejects it for OIDC apps.
	return map[string]any{
		"name":         oidcTemplate,
		"displayName":  "OpenID Connect",
		"displayLabel": displayLabel,
		"config":       map[string]any{},
		"sso":          map[string]string{"type": oidcTemplate},
	}
}

func ssoPath(id string) string { return "/api/v2/applications/" + url.PathEscape(id) + "/sso" }
