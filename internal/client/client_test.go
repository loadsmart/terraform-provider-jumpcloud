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
	"sync"
	"testing"
	"time"
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

func TestListV1StopsAtTotalCount(t *testing.T) {
	c, fake := newTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
		results := make([]User, 100)
		for i := range results {
			results[i] = User{ID: fmt.Sprint(i)}
		}
		writeJSON(t, w, http.StatusOK, map[string]any{"results": results, "totalCount": 100})
	})

	users, err := c.ListUsers(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 100 || len(fake.all()) != 1 {
		t.Fatalf("got %d users in %d requests, want 100 in 1", len(users), len(fake.all()))
	}
}

func TestRetriesRateLimit(t *testing.T) {
	calls := 0
	c, _ := newTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
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
	if calls != 3 {
		t.Fatalf("calls = %d, want 3", calls)
	}
}

func TestServerErrorRetryPolicy(t *testing.T) {
	tests := []struct {
		name      string
		call      func(*Client) error
		wantCalls int
	}{
		{"GET retries until attempts run out", func(c *Client) error { _, err := c.GetUserGroup(context.Background(), "g1"); return err }, 5},
		{"POST is not retried", func(c *Client) error {
			_, err := c.CreateUserGroup(context.Background(), UserGroup{Name: "x"})
			return err
		}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, fake := newTestClient(t, "", func(w http.ResponseWriter, r *http.Request) {
				writeJSON(t, w, http.StatusServiceUnavailable, map[string]string{"message": "down"})
			})
			err := tt.call(c)
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusServiceUnavailable || apiErr.Message != "down" {
				t.Fatalf("error = %v, want 503 APIError", err)
			}
			if n := len(fake.all()); n != tt.wantCalls {
				t.Fatalf("calls = %d, want %d", n, tt.wantCalls)
			}
		})
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
		switch r.Method {
		case http.MethodPost, http.MethodPut:
			if r.URL.Path == "/api/v2/usergroups/g1/members" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			writeJSON(t, w, http.StatusOK, map[string]string{"id": "g1", "name": "devs", "description": "d"})
		case http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
		}
	})
	ctx := context.Background()

	g, err := c.CreateUserGroup(ctx, UserGroup{ID: "ignored", Name: "devs", Description: "d"})
	if err != nil || g.ID != "g1" {
		t.Fatalf("create = %+v, %v", g, err)
	}
	if _, err := c.UpdateUserGroup(ctx, "g1", UserGroup{Name: "devs"}); err != nil {
		t.Fatal(err)
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

	want := []recorded{
		{Method: "POST", Path: "/api/v2/usergroups", Body: `{"name":"devs","description":"d"}`},
		// An empty description is sent so updates can clear it.
		{Method: "PUT", Path: "/api/v2/usergroups/g1", Body: `{"name":"devs","description":""}`},
		{Method: "POST", Path: "/api/v2/usergroups/g1/members", Body: `{"op":"add","type":"user","id":"u1"}`},
		{Method: "POST", Path: "/api/v2/usergroups/g1/members", Body: `{"op":"remove","type":"user","id":"u1"}`},
		{Method: "DELETE", Path: "/api/v2/usergroups/g1"},
	}
	assertRequests(t, fake.all(), want)
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
				"results":    []map[string]any{{"_id": "a1", "displayLabel": "Grafana", "sso": map[string]string{"type": "saml"}}},
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
	if err != nil || len(apps) != 1 || apps[0].ID != "a1" || apps[0].SSO.Type != "saml" {
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
