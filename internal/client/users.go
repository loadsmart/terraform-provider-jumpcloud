package client

import (
	"context"
	"net/url"
)

// User is a JumpCloud system user (v1 API), limited to the fields the provider exposes.
type User struct {
	ID         string          `json:"_id"`
	Username   string          `json:"username"`
	Email      string          `json:"email"`
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
