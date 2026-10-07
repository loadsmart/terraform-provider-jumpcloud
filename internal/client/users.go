package client

import (
	"context"
	"net/http"
	"net/url"
)

// User is a JumpCloud system user (v1 API), limited to the fields the provider exposes.
type User struct {
	ID           string          `json:"_id"`
	Username     string          `json:"username"`
	Email        string          `json:"email"`
	Firstname    string          `json:"firstname"`
	Lastname     string          `json:"lastname"`
	Displayname  string          `json:"displayname"`
	State        string          `json:"state"`
	Suspended    bool            `json:"suspended"`
	Activated    bool            `json:"activated"`
	Department   string          `json:"department"`
	JobTitle     string          `json:"jobTitle"`
	Company      string          `json:"company"`
	CostCenter   string          `json:"costCenter"`
	EmployeeType string          `json:"employeeType"`
	Location     string          `json:"location"`
	Attributes   []UserAttribute `json:"attributes"`
}

// UserAttribute is a custom user attribute.
type UserAttribute struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// GetUser returns ErrNotFound when the user does not exist.
func (c *Client) GetUser(ctx context.Context, id string) (*User, error) {
	var out User
	if err := c.do(ctx, http.MethodGet, "/api/systemusers/"+url.PathEscape(id), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListUsers returns users whose fields equal every value in filters (e.g. {"email": "a@b.com"}).
// An empty map returns every user.
func (c *Client) ListUsers(ctx context.Context, filters map[string]string) ([]User, error) {
	return listV1[User](ctx, c, "/api/systemusers", v1Filters(filters))
}
