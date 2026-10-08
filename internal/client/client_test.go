package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

// recorded is one request seen by the fake JumpCloud server.
type recorded struct {
	Method string
	Path   string
	Query  map[string][]string
	Header http.Header
	Body   string
}

type fakeServer struct {
	mu       sync.Mutex
	requests []recorded
}

func (f *fakeServer) all() []recorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]recorded(nil), f.requests...)
}

// newTestClient starts a server that records every request before calling handler.
func newTestClient(t *testing.T, orgID string, handler http.HandlerFunc) (*Client, *fakeServer) {
	t.Helper()
	fake := &fakeServer{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fake.mu.Lock()
		fake.requests = append(fake.requests, recorded{r.Method, r.URL.EscapedPath(), r.URL.Query(), r.Header.Clone(), string(body)})
		fake.mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(srv.Close)

	c := New(srv.URL+"/", "test-key", orgID, "terraform-provider-jumpcloud/test")
	c.retryWait = time.Millisecond
	c.maxWait = 5 * time.Millisecond
	return c, fake
}

func writeJSON(t *testing.T, w http.ResponseWriter, status int, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("encoding response: %v", err)
	}
}

func TestHeaders(t *testing.T) {
	c, fake := newTestClient(t, "org-1", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusCreated, map[string]string{"id": "g1", "name": "devs"})
	})
	if _, err := c.CreateUserGroup(context.Background(), UserGroup{Name: "devs"}); err != nil {
		t.Fatal(err)
	}

	h := fake.all()[0].Header
	want := map[string]string{
		"X-Api-Key":    "test-key",
		"X-Org-Id":     "org-1",
		"User-Agent":   "terraform-provider-jumpcloud/test",
		"Accept":       "application/json",
		"Content-Type": "application/json",
	}
	for k, v := range want {
		if got := h.Get(k); got != v {
			t.Errorf("header %s = %q, want %q", k, got, v)
		}
	}
}

func TestNoOrgHeaderOrContentTypeWhenUnset(t *testing.T) {
	c, fake := newTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]string{"id": "g1"})
	})
	if _, err := c.GetUserGroup(context.Background(), "g1"); err != nil {
		t.Fatal(err)
	}
	h := fake.all()[0].Header
	if _, ok := h["X-Org-Id"]; ok {
		t.Error("x-org-id should not be sent without an org ID")
	}
	if _, ok := h["Content-Type"]; ok {
		t.Error("Content-Type should not be sent without a body")
	}
}

func TestListV2Paginates(t *testing.T) {
	const total = 250
	c, fake := newTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		skip, _ := strconv.Atoi(r.URL.Query().Get("skip"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		page := []UserGroup{}
		for i := skip; i < min(skip+limit, total); i++ {
			page = append(page, UserGroup{ID: fmt.Sprint(i), Name: fmt.Sprint("g", i)})
		}
		writeJSON(t, w, http.StatusOK, page)
	})

	groups, err := c.ListUserGroups(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != total || groups[total-1].ID != "249" {
		t.Fatalf("got %d groups, last %+v", len(groups), groups[len(groups)-1])
	}
	var skips []string
	for _, r := range fake.all() {
		if r.Query["limit"][0] != "100" {
			t.Errorf("limit = %s, want 100", r.Query["limit"][0])
		}
		skips = append(skips, r.Query["skip"][0])
	}
	if !reflect.DeepEqual(skips, []string{"0", "100", "200"}) {
		t.Errorf("skips = %v", skips)
	}
}

func TestListV1Pagination(t *testing.T) {
	tests := []struct {
		name         string
		total        int
		wantRequests int
	}{
		{"single short page", 50, 1},
		{"pages until a short page", 150, 2},
		{"exact multiple ends on an empty page", 100, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, fake := newTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
				skip, _ := strconv.Atoi(r.URL.Query().Get("skip"))
				results := []User{}
				for i := skip; i < min(skip+pageSize, tt.total); i++ {
					results = append(results, User{ID: fmt.Sprint(i)})
				}
				writeJSON(t, w, http.StatusOK, map[string]any{"results": results, "totalCount": tt.total})
			})

			users, err := c.ListUsers(context.Background(), nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(users) != tt.total || len(fake.all()) != tt.wantRequests {
				t.Fatalf("got %d users in %d requests, want %d in %d", len(users), len(fake.all()), tt.total, tt.wantRequests)
			}
		})
	}
}

func TestRetriesRateLimit(t *testing.T) {
	calls := 0
	c, fake := newTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.Header().Set("Retry-After", "0")
			writeJSON(t, w, http.StatusTooManyRequests, map[string]string{"message": "slow down"})
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	// POST is not idempotent, but a 429 means JumpCloud did not process it.
	if err := c.AddUserToGroup(context.Background(), "g1", "u1"); err != nil {
		t.Fatal(err)
	}
	reqs := fake.all()
	if len(reqs) != 3 {
		t.Fatalf("calls = %d, want 3", len(reqs))
	}
	for i, r := range reqs {
		if r.Body != `{"op":"add","type":"user","id":"u1"}` {
			t.Errorf("attempt %d body = %q, want the request body resent", i+1, r.Body)
		}
	}
}

func TestBackoff(t *testing.T) {
	c := New("https://example.com", "k", "", "ua")
	c.retryWait = 100 * time.Millisecond
	c.maxWait = 5 * time.Second

	if got := c.backoff(1, "2"); got != 2*time.Second {
		t.Errorf("Retry-After 2 = %v, want 2s", got)
	}
	if got := c.backoff(1, "60"); got != 5*time.Second {
		t.Errorf("Retry-After 60 = %v, want capped at 5s", got)
	}
	// Without a usable Retry-After, attempt n waits between half and all of retryWait*2^(n-1).
	for _, tt := range []struct {
		attempt int
		header  string
		lo, hi  time.Duration
	}{
		{1, "", 50 * time.Millisecond, 100 * time.Millisecond},
		{3, "soon", 200 * time.Millisecond, 400 * time.Millisecond},
		{10, "-1", 5 * time.Second, 5 * time.Second},
	} {
		for range 50 {
			if got := c.backoff(tt.attempt, tt.header); got < tt.lo || got > tt.hi {
				t.Fatalf("backoff(%d, %q) = %v, want within [%v, %v]", tt.attempt, tt.header, got, tt.lo, tt.hi)
			}
		}
	}
}

func TestRetryPolicy(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name      string
		status    int
		call      func(*Client) error
		wantCalls int
	}{
		{"GET retries 503 until attempts run out", http.StatusServiceUnavailable, func(c *Client) error {
			_, err := c.GetUserGroup(ctx, "g1")
			return err
		}, 5},
		{"PUT retries 502", http.StatusBadGateway, func(c *Client) error {
			return c.do(ctx, http.MethodPut, "/api/v2/usergroups/g1", nil, map[string]string{"name": "x"}, nil)
		}, 5},
		{"DELETE retries 504", http.StatusGatewayTimeout, func(c *Client) error { return c.DeleteUserGroup(ctx, "g1") }, 5},
		{"POST does not retry 503", http.StatusServiceUnavailable, func(c *Client) error {
			_, err := c.CreateUserGroup(ctx, UserGroup{Name: "x"})
			return err
		}, 1},
		{"GET does not retry 500", http.StatusInternalServerError, func(c *Client) error {
			_, err := c.GetUserGroup(ctx, "g1")
			return err
		}, 1},
		{"persistent 429 gives up", http.StatusTooManyRequests, func(c *Client) error { return c.AddUserToGroup(ctx, "g1", "u1") }, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, fake := newTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, tt.status, map[string]string{"message": "nope"})
			})
			err := tt.call(c)
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != tt.status || apiErr.Message != "nope" {
				t.Fatalf("error = %v, want %d APIError", err, tt.status)
			}
			if n := len(fake.all()); n != tt.wantCalls {
				t.Fatalf("calls = %d, want %d", n, tt.wantCalls)
			}
		})
	}
}

func TestEmptySuccessBodyIsAnError(t *testing.T) {
	c, _ := newTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	if g, err := c.CreateUserGroup(context.Background(), UserGroup{Name: "x"}); err == nil {
		t.Fatalf("create = %+v, want an error instead of a group without an ID", g)
	}
}

func TestWaitStopsOnContextCancel(t *testing.T) {
	c, _ := newTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusTooManyRequests, nil)
	})
	c.retryWait, c.maxWait = time.Hour, time.Hour

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := c.GetUserGroup(ctx, "g1"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
}

func TestNotFound(t *testing.T) {
	c, _ := newTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusNotFound, map[string]string{"message": "not found"})
	})
	if _, err := c.GetUser(context.Background(), "u1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
	if err := c.DeleteUserGroup(context.Background(), "g1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete error = %v, want ErrNotFound", err)
	}
}

func TestErrorMessage(t *testing.T) {
	tests := map[string]string{
		`{"code":400,"message":"name is required","status":"Bad Request"}`: "name is required",
		`{"error":"Unauthorized"}`: "Unauthorized",
		"upstream connect error":   "upstream connect error",
		"":                         "empty response body",
	}
	for body, want := range tests {
		if got := errorMessage([]byte(body)); got != want {
			t.Errorf("errorMessage(%q) = %q, want %q", body, got, want)
		}
	}

	// A long body is cut at 512 bytes without leaving half of a multi-byte character.
	long := strings.Repeat("a", 511) + "é" + strings.Repeat("b", 100)
	got := errorMessage([]byte(long))
	if !utf8.ValidString(got) || got != strings.Repeat("a", 511)+"..." {
		t.Errorf("truncated message = %q", got)
	}
}

func TestFindUserGroupsByNameRequiresExactMatch(t *testing.T) {
	c, fake := newTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, []UserGroup{{ID: "1", Name: "data-squad"}, {ID: "2", Name: "data-squad-leads"}})
	})

	groups, err := c.FindUserGroupsByName(context.Background(), "data-squad")
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 1 || groups[0].ID != "1" {
		t.Fatalf("groups = %+v", groups)
	}
	if got := fake.all()[0].Query["filter"]; !reflect.DeepEqual(got, []string{"name:eq:data-squad"}) {
		t.Errorf("filter = %v", got)
	}
}

func TestUserGroupWrites(t *testing.T) {
	c, fake := newTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v2/usergroups/g1/members", r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		default:
			writeJSON(t, w, http.StatusOK, map[string]string{"id": "g1", "name": "devs", "description": "d"})
		}
	})
	ctx := context.Background()

	g, err := c.CreateUserGroup(ctx, UserGroup{Name: "devs", Description: "d"})
	if err != nil || g.ID != "g1" {
		t.Fatalf("create = %+v, %v", g, err)
	}
	if err := c.AddUserToGroup(ctx, "g1", "u1"); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveUserFromGroup(ctx, "g1", "u1"); err != nil {
		t.Fatal(err)
	}
	if err := c.DeleteUserGroup(ctx, "g1"); err != nil {
		t.Fatal(err)
	}

	assertRequests(t, fake.all(), []recorded{
		{Method: "POST", Path: "/api/v2/usergroups", Body: `{"name":"devs","description":"d"}`},
		{Method: "POST", Path: "/api/v2/usergroups/g1/members", Body: `{"op":"add","type":"user","id":"u1"}`},
		{Method: "POST", Path: "/api/v2/usergroups/g1/members", Body: `{"op":"remove","type":"user","id":"u1"}`},
		{Method: "DELETE", Path: "/api/v2/usergroups/g1"},
	})
}

func TestUpdateUserGroupKeepsUnmanagedFields(t *testing.T) {
	c, fake := newTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			writeJSON(t, w, http.StatusOK, map[string]any{
				"id": "g1", "type": "user_group", "name": "old", "description": "old desc",
				"email":            "devs@loadsmart.com",
				"attributes":       map[string]any{"sudo": map[string]bool{"enabled": true}},
				"membershipMethod": "STATIC",
				"suggestionCounts": map[string]int{"add": 1},
			})
			return
		}
		writeJSON(t, w, http.StatusOK, map[string]string{"id": "g1", "name": "new"})
	})

	g, err := c.UpdateUserGroup(context.Background(), "g1", UserGroup{Name: "new"})
	if err != nil || g.Name != "new" {
		t.Fatalf("update = %+v, %v", g, err)
	}

	reqs := fake.all()
	if len(reqs) != 2 || reqs[0].Method != http.MethodGet || reqs[1].Method != http.MethodPut || reqs[1].Path != "/api/v2/usergroups/g1" {
		t.Fatalf("requests = %+v, want GET then PUT", reqs)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(reqs[1].Body), &sent); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"name":             "new",
		"description":      "", // an empty description clears it
		"email":            "devs@loadsmart.com",
		"attributes":       map[string]any{"sudo": map[string]any{"enabled": true}},
		"membershipMethod": "STATIC",
	}
	if !reflect.DeepEqual(sent, want) {
		t.Fatalf("PUT body = %v\nwant %v", sent, want)
	}
}

func TestUserGroupIDsReturnsDirectMembershipsOnly(t *testing.T) {
	c, fake := newTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		hop := map[string]any{"from": map[string]string{"type": "user", "id": "u1"}, "to": map[string]string{"type": "user_group", "id": "x"}}
		writeJSON(t, w, http.StatusOK, []map[string]any{
			{"id": "direct", "type": "user_group", "paths": [][]any{{hop}}},
			{"id": "inherited", "type": "user_group", "paths": [][]any{{hop, hop}}},
			{"id": "both", "type": "user_group", "paths": [][]any{{hop, hop}, {hop}}},
			{"id": "app", "type": "application", "paths": [][]any{{hop}}},
		})
	})

	ids, err := c.UserGroupIDs(context.Background(), "u1")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ids, []string{"direct", "both"}) {
		t.Fatalf("ids = %v", ids)
	}
	if p := fake.all()[0].Path; p != "/api/v2/users/u1/memberof" {
		t.Errorf("path = %s", p)
	}
}

func TestListUsersFilters(t *testing.T) {
	c, fake := newTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]any{
			"results": []map[string]any{{
				"_id": "u1", "email": "a@loadsmart.com", "department": "Engineering",
				"attributes": []map[string]string{{"name": "team", "value": "platform"}},
			}},
			"totalCount": 1,
		})
	})

	users, err := c.ListUsers(context.Background(), map[string]string{"email": "a@loadsmart.com", "department": "Engineering"})
	if err != nil {
		t.Fatal(err)
	}
	want := User{ID: "u1", Email: "a@loadsmart.com", Department: "Engineering", Attributes: []UserAttribute{{"team", "platform"}}}
	if len(users) != 1 || !reflect.DeepEqual(users[0], want) {
		t.Fatalf("users = %+v", users)
	}

	q := fake.all()[0].Query
	if q["filter[0]"][0] != "department:$eq:Engineering" || q["filter[1]"][0] != "email:$eq:a@loadsmart.com" {
		t.Errorf("filters = %v", q)
	}
}

func TestApplications(t *testing.T) {
	c, fake := newTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/applications":
			writeJSON(t, w, http.StatusOK, map[string]any{
				"results":    []map[string]any{{"_id": "a1", "displayLabel": "Grafana"}},
				"totalCount": 1,
			})
		case r.Method == http.MethodGet:
			writeJSON(t, w, http.StatusOK, []map[string]any{
				{"from": map[string]string{"type": "application", "id": "a1"}, "to": map[string]string{"type": "user_group", "id": "g1"}},
				{"from": map[string]string{"type": "application", "id": "a1"}, "to": map[string]string{"type": "user_group", "id": "g2"}},
			})
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	ctx := context.Background()

	apps, err := c.ListApplications(ctx, map[string]string{"displayLabel": "Grafana"})
	if err != nil || len(apps) != 1 || apps[0].ID != "a1" || apps[0].DisplayLabel != "Grafana" {
		t.Fatalf("apps = %+v, %v", apps, err)
	}
	ids, err := c.ApplicationAssociationIDs(ctx, "a1", TargetUserGroup)
	if err != nil || !reflect.DeepEqual(ids, []string{"g1", "g2"}) {
		t.Fatalf("ids = %v, %v", ids, err)
	}
	if err := c.AddApplicationAssociation(ctx, "a1", TargetUserGroup, "g3"); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveApplicationAssociation(ctx, "a1", TargetUser, "u1"); err != nil {
		t.Fatal(err)
	}

	reqs := fake.all()
	if q := reqs[0].Query["filter[0]"]; !reflect.DeepEqual(q, []string{"displayLabel:$eq:Grafana"}) {
		t.Errorf("application filter = %v", q)
	}
	if q := reqs[1].Query["targets"]; !reflect.DeepEqual(q, []string{"user_group"}) {
		t.Errorf("targets = %v", q)
	}
	assertRequests(t, reqs[2:], []recorded{
		{Method: "POST", Path: "/api/v2/applications/a1/associations", Body: `{"op":"add","type":"user_group","id":"g3"}`},
		{Method: "POST", Path: "/api/v2/applications/a1/associations", Body: `{"op":"remove","type":"user","id":"u1"}`},
	})
}

func TestPathSegmentsAreEscaped(t *testing.T) {
	c, fake := newTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, map[string]string{"_id": "x"})
	})
	if _, err := c.GetUser(context.Background(), "../usergroups"); err != nil {
		t.Fatal(err)
	}
	if p := fake.all()[0].Path; p != "/api/systemusers/..%2Fusergroups" {
		t.Errorf("path = %q, want the ID kept as one escaped segment", p)
	}
}

// assertRequests compares method, path, and body; headers and query are checked elsewhere.
func assertRequests(t *testing.T, got, want []recorded) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d requests, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		g := got[i]
		if g.Method != w.Method || g.Path != w.Path || g.Body != w.Body {
			t.Errorf("request %d = %s %s %s, want %s %s %s", i, g.Method, g.Path, g.Body, w.Method, w.Path, w.Body)
		}
	}
}

func TestFindUserGroupsByNameWithComma(t *testing.T) {
	c, fake := newTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, http.StatusOK, []UserGroup{{ID: "1", Name: "Sales, EMEA"}, {ID: "2", Name: "Sales"}})
	})
	groups, err := c.FindUserGroupsByName(context.Background(), "Sales, EMEA")
	if err != nil || len(groups) != 1 || groups[0].ID != "1" {
		t.Fatalf("groups = %+v, %v", groups, err)
	}
	if q := fake.all()[0].Query; q["filter"] != nil {
		t.Errorf("filter = %v, want none for names with commas", q["filter"])
	}
}

func TestCreateOIDCApplicationDeletesAppWhenSettingsFail(t *testing.T) {
	c, fake := newTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /api/applications":
			writeJSON(t, w, http.StatusOK, map[string]string{"_id": "a1"})
		case "POST /api/v2/applications/a1/sso":
			writeJSON(t, w, http.StatusBadRequest, map[string]string{"message": "invalid redirect URI"})
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})

	_, err := c.CreateOIDCApplication(context.Background(), OIDCApplication{DisplayLabel: "app", OIDC: OIDCSettings{DynamicClaims: []OIDCClaim{}}})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Message != "invalid redirect URI" {
		t.Fatalf("error = %v, want the settings error", err)
	}
	reqs := fake.all()
	if len(reqs) != 3 || reqs[2].Method != http.MethodDelete || reqs[2].Path != "/api/applications/a1" {
		t.Fatalf("requests = %+v, want create, settings, delete", reqs)
	}
	if !strings.Contains(reqs[0].Body, `"active":true`) {
		t.Errorf("create body %s must activate the app, or sign-ins fail", reqs[0].Body)
	}
	if strings.Contains(reqs[0].Body, "ssoUrl") {
		t.Errorf("create body %s must not send ssoUrl, which JumpCloud rejects for OIDC apps", reqs[0].Body)
	}
	if !strings.Contains(reqs[1].Body, `"consent":"trusted"`) {
		t.Errorf("settings body %s should set consent to trusted", reqs[1].Body)
	}
}
