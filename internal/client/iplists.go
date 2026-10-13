package client

import (
	"context"
	"net/http"
	"net/url"
)

// IPList is a named list of IP addresses and CIDR ranges that authentication policy
// conditions refer to.
type IPList struct {
	ID          string   `json:"id,omitempty"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	IPs         []string `json:"ips"`
}

func ipListPath(id string) string { return "/api/v2/iplists/" + url.PathEscape(id) }

// CreateIPList creates an IP list.
func (c *Client) CreateIPList(ctx context.Context, l IPList) (*IPList, error) {
	l.ID = ""
	var out IPList
	if err := c.do(ctx, http.MethodPost, "/api/v2/iplists", nil, l, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetIPList returns ErrNotFound when the list does not exist.
func (c *Client) GetIPList(ctx context.Context, id string) (*IPList, error) {
	return get[IPList](ctx, c, ipListPath(id))
}

// ReplaceIPList replaces the name, description, and addresses of an IP list.
func (c *Client) ReplaceIPList(ctx context.Context, id string, l IPList) (*IPList, error) {
	l.ID = ""
	var out IPList
	if err := c.do(ctx, http.MethodPut, ipListPath(id), nil, l, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteIPList returns ErrNotFound when the list is already gone.
func (c *Client) DeleteIPList(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodDelete, ipListPath(id), nil, nil, nil)
}
