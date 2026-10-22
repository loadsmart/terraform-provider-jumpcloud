package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// ServiceAccount is a JumpCloud service account, which calls the admin API with an OAuth
// client ID and secret instead of an admin's API key. Its admin role decides what it can
// do. See https://jumpcloud.com/support/service-account-for-apis.
type ServiceAccount struct {
	ID          string       `json:"objectId"`
	Name        string       `json:"name"`
	RoleID      string       `json:"roleId"`
	Status      string       `json:"status"`
	ExpiresAt   string       `json:"expiresAt"`
	AuthConfigs []AuthConfig `json:"authConfigList"`
}

// AuthConfig is one credential of a service account. An account holds up to two client
// secrets at a time, so one can be rotated while the other is in use.
type AuthConfig struct {
	AuthType     string        `json:"authType"`
	ClientSecret *ClientSecret `json:"clientSecretConfig"`
}

type ClientSecret struct {
	ID       string `json:"objectId"`
	ClientID string `json:"clientId"`
	// Secret is only returned when the secret is created; reads leave it empty.
	Secret    string `json:"clientSecret"`
	Lifetime  string `json:"lifetime"`
	ExpiresAt string `json:"expiresAt"`
	Status    string `json:"status"`
}

const authTypeClientSecret = "CLIENT_SECRET"

// SecretLifetimes are the client secret lifetimes JumpCloud offers, in days.
var SecretLifetimes = []int64{30, 60, 90, 365}

// FirstClientSecret returns the account's first client secret, or nil if it has none.
func (s ServiceAccount) FirstClientSecret() *ClientSecret {
	for _, a := range s.AuthConfigs {
		if a.AuthType == authTypeClientSecret && a.ClientSecret != nil {
			return a.ClientSecret
		}
	}
	return nil
}

// LifetimeDays parses a lifetime such as "365 Days". It returns 0 for any other format.
func (s ClientSecret) LifetimeDays() int64 {
	days, _ := strconv.ParseInt(strings.TrimSuffix(s.Lifetime, " Days"), 10, 64)
	return days
}

type createServiceAccountRequest struct {
	Name       string               `json:"name"`
	RoleID     string               `json:"roleId"`
	AuthConfig authConfigParameters `json:"authConfig"`
}

type authConfigParameters struct {
	AuthType           string `json:"authType"`
	ClientSecretConfig struct {
		Lifetime string `json:"lifetime"`
	} `json:"clientSecretConfig"`
}

// CreateServiceAccount creates an account with one client secret that expires after
// lifetimeDays. The returned account is the only one whose secret is filled in.
func (c *Client) CreateServiceAccount(ctx context.Context, name, roleID string, lifetimeDays int64) (*ServiceAccount, error) {
	body := createServiceAccountRequest{Name: name, RoleID: roleID}
	body.AuthConfig.AuthType = authTypeClientSecret
	body.AuthConfig.ClientSecretConfig.Lifetime = fmt.Sprintf("%d Days", lifetimeDays)

	var raw json.RawMessage
	if err := c.do(ctx, http.MethodPost, serviceAccountsPath, nil, body, &raw); err != nil {
		return nil, err
	}
	sa, err := decodeServiceAccount(raw, "POST "+serviceAccountsPath)
	if err != nil {
		return nil, err
	}
	// Without the secret the account is unusable, and it cannot be fetched again.
	if secret := sa.FirstClientSecret(); secret == nil || secret.ClientID == "" || secret.Secret == "" {
		err := fmt.Errorf("jumpcloud: POST %s returned no client secret for new service account %s", serviceAccountsPath, sa.ID)
		// Clean up even if Terraform was interrupted, so a retry starts clean.
		if delErr := c.DeleteServiceAccount(context.WithoutCancel(ctx), sa.ID); delErr != nil {
			return nil, fmt.Errorf("%w (and deleting it failed: %v)", err, delErr)
		}
		return nil, err
	}
	return sa, nil
}

// GetServiceAccount returns ErrNotFound when the account does not exist.
func (c *Client) GetServiceAccount(ctx context.Context, id string) (*ServiceAccount, error) {
	raw, err := get[json.RawMessage](ctx, c, serviceAccountPath(id))
	if err != nil {
		return nil, err
	}
	return decodeServiceAccount(*raw, "GET "+serviceAccountPath(id))
}

// decodeServiceAccount accepts the account wrapped in {"serviceAccount": ...}, as
// JumpCloud returns it from create and get, or at the top level, as the API reference
// shows for get. A response without an ID is an error, so an unexpected shape cannot
// empty the Terraform state.
func decodeServiceAccount(raw json.RawMessage, op string) (*ServiceAccount, error) {
	var wrapped struct {
		ServiceAccount *ServiceAccount `json:"serviceAccount"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, fmt.Errorf("jumpcloud: decoding %s response: %w", op, err)
	}
	sa := wrapped.ServiceAccount
	if sa == nil {
		sa = new(ServiceAccount)
		if err := json.Unmarshal(raw, sa); err != nil {
			return nil, fmt.Errorf("jumpcloud: decoding %s response: %w", op, err)
		}
	}
	if sa.ID == "" {
		return nil, errors.New("jumpcloud: " + op + " returned no service account ID")
	}
	return sa, nil
}

// DeleteServiceAccount deletes the account and revokes its secrets.
func (c *Client) DeleteServiceAccount(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, serviceAccountPath(id), nil, nil, nil)
}

const serviceAccountsPath = "/api/v2/service-accounts"

func serviceAccountPath(id string) string {
	return serviceAccountsPath + "/" + url.PathEscape(id)
}

// Role is an admin role, such as Read Only or a custom role.
type Role struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	IsCustom    bool   `json:"isCustom"`
}

// ListRoles returns the organization's admin roles, built-in and custom.
func (c *Client) ListRoles(ctx context.Context) ([]Role, error) {
	// A v2 endpoint that wraps its results like v1 does.
	return list[Role](ctx, c, "/api/v2/roles", nil, true)
}
