package client

import (
	"context"
	"net/http"
	"net/url"
)

// Application is a JumpCloud SSO application (v1 API), limited to the fields the provider exposes.
type Application struct {
	ID           string `json:"_id"`
	Name         string `json:"name"`
	DisplayName  string `json:"displayName"`
	DisplayLabel string `json:"displayLabel"`
	Description  string `json:"description"`
	SSOURL       string `json:"ssoUrl"`
	Active       bool   `json:"active"`
	SSO          struct {
		Type string `json:"type"`
	} `json:"sso"`
}

// Association target types accepted by the application associations endpoint.
const (
	TargetUser      = "user"
	TargetUserGroup = "user_group"
)

// GetApplication returns ErrNotFound when the application does not exist.
func (c *Client) GetApplication(ctx context.Context, id string) (*Application, error) {
	var out Application
	if err := c.do(ctx, http.MethodGet, "/api/applications/"+url.PathEscape(id), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListApplications returns applications whose fields equal every value in filters
// (e.g. {"displayLabel": "Grafana"}). An empty map returns every application.
func (c *Client) ListApplications(ctx context.Context, filters map[string]string) ([]Application, error) {
	return listV1[Application](ctx, c, "/api/applications", v1Filters(filters))
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
	conns, err := listV2[graphConnection](ctx, c, path, url.Values{"targets": {targetType}})
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
