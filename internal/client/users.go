package client

import (
	"context"
	"errors"
	"net/http"
	"net/url"
)

// User is a JumpCloud system user (v1 API), limited to the fields the provider exposes.
type User struct {
	ID         string          `json:"_id"`
	Username   string          `json:"username"`
	Email      string          `json:"email"`
	Firstname  string          `json:"firstname"`
	Lastname   string          `json:"lastname"`
	State      string          `json:"state"`
	Department string          `json:"department"`
	JobTitle   string          `json:"jobTitle"`
	Attributes []UserAttribute `json:"attributes"`
}

// UserAttribute is a custom user attribute.
type UserAttribute struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// GetUser returns ErrNotFound when the user does not exist.
func (c *Client) GetUser(ctx context.Context, id string) (*User, error) {
	return get[User](ctx, c, "/api/systemusers/"+url.PathEscape(id))
}

// ListUsers returns users whose fields equal every value in filters (e.g. {"email": "a@b.com"}).
// An empty map returns every user.
func (c *Client) ListUsers(ctx context.Context, filters map[string]string) ([]User, error) {
	return list[User](ctx, c, "/api/systemusers", v1Filters(filters), true)
}

// UserFields are the user fields the provider writes.
type UserFields struct {
	Username   string `json:"username"`
	Email      string `json:"email"`
	Firstname  string `json:"firstname"`
	Lastname   string `json:"lastname"`
	Department string `json:"department"`
	JobTitle   string `json:"jobTitle"`
	// State is ACTIVATED, STAGED, or SUSPENDED. Empty keeps the current state, or uses
	// the organization's default on create.
	State string `json:"state,omitempty"`
	// Attributes replaces every custom attribute when not nil.
	Attributes *[]UserAttribute `json:"attributes,omitempty"`
}

func userPath(id string) string { return "/api/systemusers/" + url.PathEscape(id) }

// CreateUser creates a user.
func (c *Client) CreateUser(ctx context.Context, f UserFields) (*User, error) {
	var out User
	if err := c.do(ctx, http.MethodPost, "/api/systemusers", nil, f, &out); err != nil {
		return nil, err
	}
	if out.ID == "" {
		return nil, errors.New("jumpcloud: POST /api/systemusers returned no user ID")
	}
	return &out, nil
}

// UpdateUser changes the given fields. The v1 PUT leaves fields it is not sent as they are.
func (c *Client) UpdateUser(ctx context.Context, id string, f UserFields) (*User, error) {
	var out User
	if err := c.do(ctx, http.MethodPut, userPath(id), nil, f, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteUser returns ErrNotFound when the user is already gone.
func (c *Client) DeleteUser(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, userPath(id), nil, nil, nil)
}
