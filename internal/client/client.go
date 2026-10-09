// Package client is a minimal JumpCloud API client covering the endpoints the provider uses.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math/rand/v2"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-log/tflog"
)

const (
	// pageSize is the maximum page size JumpCloud accepts on list endpoints.
	pageSize = 100
	// maxResponseBytes bounds how much of a response body is read into memory.
	maxResponseBytes = 32 << 20
)

// ErrNotFound is returned when JumpCloud answers 404 for the requested object.
var ErrNotFound = errors.New("jumpcloud: not found")

// APIError describes a non-2xx JumpCloud response other than 404.
type APIError struct {
	Method     string
	Path       string
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("jumpcloud: %s %s returned %d: %s", e.Method, e.Path, e.StatusCode, e.Message)
}

// Client calls the JumpCloud v1 and v2 APIs with an admin API key.
type Client struct {
	baseURL    string
	apiKey     string
	orgID      string
	userAgent  string
	httpClient *http.Client

	maxAttempts int
	retryWait   time.Duration // first backoff interval, doubled on every retry
	maxWait     time.Duration // upper bound for any single wait, including Retry-After
}

// New returns a client for baseURL, e.g. https://console.jumpcloud.com. orgID may be empty.
func New(baseURL, apiKey, orgID, userAgent string) *Client {
	return &Client{
		baseURL:     strings.TrimRight(baseURL, "/"),
		apiKey:      apiKey,
		orgID:       orgID,
		userAgent:   userAgent,
		httpClient:  &http.Client{Timeout: 30 * time.Second},
		maxAttempts: 5,
		retryWait:   time.Second,
		maxWait:     30 * time.Second,
	}
}

// do sends a request and decodes a JSON response into out when out is non-nil.
// It retries rate-limited requests, and server errors on idempotent methods.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, in, out any) error {
	return c.doWithHeader(ctx, method, path, query, nil, in, out)
}

// doWithHeader is do with extra request headers.
func (c *Client) doWithHeader(ctx context.Context, method, path string, query url.Values, header http.Header, in, out any) error {
	var body []byte
	if in != nil {
		var err error
		if body, err = json.Marshal(in); err != nil {
			return fmt.Errorf("jumpcloud: encoding %s %s request: %w", method, path, err)
		}
	}

	target := c.baseURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}

	for attempt := 1; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, target, bytes.NewReader(body))
		if err != nil {
			return fmt.Errorf("jumpcloud: building %s %s request: %w", method, path, err)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("x-api-key", c.apiKey)
		req.Header.Set("User-Agent", c.userAgent)
		if c.orgID != "" {
			req.Header.Set("x-org-id", c.orgID)
		}
		if in != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		maps.Copy(req.Header, header)

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return fmt.Errorf("jumpcloud: %s %s: %w", method, path, err)
		}
		respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		_ = resp.Body.Close()
		if err != nil {
			return fmt.Errorf("jumpcloud: reading %s %s response: %w", method, path, err)
		}
		tflog.Debug(ctx, "JumpCloud API request", map[string]any{"method": method, "path": path, "status": resp.StatusCode, "attempt": attempt})

		switch {
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			if out == nil {
				return nil
			}
			if len(respBody) == 0 {
				return fmt.Errorf("jumpcloud: %s %s returned %d with an empty body", method, path, resp.StatusCode)
			}
			if err := json.Unmarshal(respBody, out); err != nil {
				return fmt.Errorf("jumpcloud: decoding %s %s response: %w", method, path, err)
			}
			return nil
		case resp.StatusCode == http.StatusNotFound:
			return ErrNotFound
		case attempt < c.maxAttempts && retryable(method, resp.StatusCode):
			if err := sleep(ctx, c.backoff(attempt, resp.Header.Get("Retry-After"))); err != nil {
				return fmt.Errorf("jumpcloud: %s %s: waiting to retry: %w", method, path, err)
			}
		default:
			return &APIError{Method: method, Path: path, StatusCode: resp.StatusCode, Message: errorMessage(respBody)}
		}
	}
}

// retryable reports whether a failed request is safe to send again. 429 means the
// request was not processed; 5xx is only retried when repeating it cannot duplicate work.
func retryable(method string, status int) bool {
	if status == http.StatusTooManyRequests {
		return true
	}
	idempotent := method == http.MethodGet || method == http.MethodPut || method == http.MethodDelete
	return idempotent && (status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout)
}

// backoff returns how long to wait before the next attempt. A Retry-After header in
// seconds wins; otherwise the wait doubles per attempt, with jitter so parallel
// Terraform operations that were throttled together do not retry in lockstep.
func (c *Client) backoff(attempt int, retryAfter string) time.Duration {
	var d time.Duration
	if secs, err := strconv.Atoi(retryAfter); err == nil && secs >= 0 {
		d = time.Duration(secs) * time.Second
	} else {
		d = c.retryWait << (attempt - 1)
		d = d/2 + rand.N(d/2+1)
	}
	return min(d, c.maxWait)
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// errorMessage extracts JumpCloud's error text; v1 and v2 use different shapes.
func errorMessage(body []byte) string {
	var parsed struct {
		Message string `json:"message"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(body, &parsed) == nil {
		if parsed.Message != "" {
			return parsed.Message
		}
		if parsed.Error != "" {
			return parsed.Error
		}
	}
	msg := strings.TrimSpace(string(body))
	if msg == "" {
		return "empty response body"
	}
	if len(msg) > 512 {
		// Drop any multi-byte character split by the cut.
		msg = strings.ToValidUTF8(msg[:512], "") + "..."
	}
	return msg
}

func get[T any](ctx context.Context, c *Client, path string) (*T, error) {
	var out T
	if err := c.do(ctx, http.MethodGet, path, nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// list pages through a list endpoint until it returns a short page. v2 endpoints
// return a bare JSON array; v1 endpoints wrap it in {"results": [...]}.
func list[T any](ctx context.Context, c *Client, path string, query url.Values, v1 bool) ([]T, error) {
	q := url.Values{}
	maps.Copy(q, query)
	q.Set("limit", strconv.Itoa(pageSize))

	var all []T
	for skip := 0; ; skip += pageSize {
		q.Set("skip", strconv.Itoa(skip))
		var page []T
		var err error
		if v1 {
			var wrapped struct {
				Results []T `json:"results"`
			}
			err = c.do(ctx, http.MethodGet, path, q, nil, &wrapped)
			page = wrapped.Results
		} else {
			err = c.do(ctx, http.MethodGet, path, q, nil, &page)
		}
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if len(page) < pageSize {
			return all, nil
		}
	}
}

// v1Filters encodes exact-match filters as filter[0]=field:$eq:value&filter[1]=...,
// which JumpCloud ANDs together. Keys are sorted so requests are deterministic.
func v1Filters(filters map[string]string) url.Values {
	q := url.Values{}
	for i, field := range slices.Sorted(maps.Keys(filters)) {
		q.Set(fmt.Sprintf("filter[%d]", i), field+":$eq:"+filters[field])
	}
	return q
}

// graphOperation is the body for v2 association and membership changes.
type graphOperation struct {
	Op   string `json:"op"`
	Type string `json:"type"`
	ID   string `json:"id"`
}
