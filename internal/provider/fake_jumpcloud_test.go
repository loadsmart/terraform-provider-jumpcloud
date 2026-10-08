package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"

	"github.com/loadsmart/terraform-provider-jumpcloud/internal/client"
)

// fakeJumpCloud serves the subset of the JumpCloud API the provider uses, so resources
// can be tested through Terraform without credentials.
type fakeJumpCloud struct {
	url string

	mu      sync.Mutex
	nextID  int
	groups  map[string]client.UserGroup
	members map[string][]string // group ID -> user IDs
	users   []client.User
}

func newFakeJumpCloud(t *testing.T) *fakeJumpCloud {
	t.Helper()
	f := &fakeJumpCloud{groups: map[string]client.UserGroup{}, members: map[string][]string{}}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v2/usergroups", f.createGroup)
	mux.HandleFunc("GET /api/v2/usergroups", f.listGroups)
	mux.HandleFunc("GET /api/v2/usergroups/{id}", f.getGroup)
	mux.HandleFunc("PUT /api/v2/usergroups/{id}", f.updateGroup)
	mux.HandleFunc("DELETE /api/v2/usergroups/{id}", f.removeGroup)
	mux.HandleFunc("POST /api/v2/usergroups/{id}/members", f.changeMember)
	mux.HandleFunc("GET /api/v2/users/{id}/memberof", f.memberOf)
	mux.HandleFunc("GET /api/systemusers", f.listUsers)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	f.url = srv.URL
	return f
}

func (f *fakeJumpCloud) providerConfig() string {
	return fmt.Sprintf("provider \"jumpcloud\" {\n  api_key = \"test\"\n  api_url = %q\n}\n", f.url)
}

func (f *fakeJumpCloud) addGroup(name, description string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	id := fmt.Sprintf("g%d", f.nextID)
	f.groups[id] = client.UserGroup{ID: id, Name: name, Description: description}
	return id
}

func (f *fakeJumpCloud) deleteGroup(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.groups, id)
	delete(f.members, id)
}

func (f *fakeJumpCloud) group(id string) (client.UserGroup, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	g, ok := f.groups[id]
	return g, ok
}

func (f *fakeJumpCloud) groupCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.groups)
}

func (f *fakeJumpCloud) addUser(u client.User) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users = append(f.users, u)
}

func (f *fakeJumpCloud) addMember(groupID, userID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.members[groupID] = append(f.members[groupID], userID)
}

func (f *fakeJumpCloud) isMember(groupID, userID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Contains(f.members[groupID], userID)
}

func (f *fakeJumpCloud) createGroup(w http.ResponseWriter, r *http.Request) {
	var g client.UserGroup
	if err := json.NewDecoder(r.Body).Decode(&g); err != nil {
		writeFake(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	g.ID = f.addGroup(g.Name, g.Description)
	writeFake(w, http.StatusCreated, g)
}

func (f *fakeJumpCloud) getGroup(w http.ResponseWriter, r *http.Request) {
	g, ok := f.group(r.PathValue("id"))
	if !ok {
		writeFake(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	writeFake(w, http.StatusOK, g)
}

func (f *fakeJumpCloud) updateGroup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body client.UserGroup
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeFake(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	f.mu.Lock()
	_, ok := f.groups[id]
	if ok {
		f.groups[id] = client.UserGroup{ID: id, Name: body.Name, Description: body.Description}
	}
	f.mu.Unlock()
	if !ok {
		writeFake(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	f.getGroup(w, r)
}

func (f *fakeJumpCloud) removeGroup(w http.ResponseWriter, r *http.Request) {
	if _, ok := f.group(r.PathValue("id")); !ok {
		writeFake(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	f.deleteGroup(r.PathValue("id"))
	w.WriteHeader(http.StatusNoContent)
}

func (f *fakeJumpCloud) listGroups(w http.ResponseWriter, r *http.Request) {
	name, filtered := strings.CutPrefix(r.URL.Query().Get("filter"), "name:eq:")
	f.mu.Lock()
	var out []client.UserGroup
	for _, g := range f.groups {
		if !filtered || g.Name == name {
			out = append(out, g)
		}
	}
	f.mu.Unlock()
	slices.SortFunc(out, func(a, b client.UserGroup) int { return strings.Compare(a.ID, b.ID) })
	writeFake(w, http.StatusOK, page(r, out))
}

func (f *fakeJumpCloud) changeMember(w http.ResponseWriter, r *http.Request) {
	groupID := r.PathValue("id")
	var op struct{ Op, Type, ID string }
	if err := json.NewDecoder(r.Body).Decode(&op); err != nil || op.Type != "user" {
		writeFake(w, http.StatusBadRequest, map[string]string{"message": "invalid operation"})
		return
	}
	if _, ok := f.group(groupID); !ok {
		writeFake(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	member := f.isMember(groupID, op.ID)
	switch {
	case op.Op == "add" && member:
		writeFake(w, http.StatusConflict, map[string]string{"message": "Already Exists"})
	case op.Op == "add":
		f.addMember(groupID, op.ID)
		w.WriteHeader(http.StatusNoContent)
	case op.Op == "remove" && !member:
		writeFake(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
	default:
		f.mu.Lock()
		f.members[groupID] = slices.DeleteFunc(f.members[groupID], func(u string) bool { return u == op.ID })
		f.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}
}

func (f *fakeJumpCloud) memberOf(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	f.mu.Lock()
	known := slices.ContainsFunc(f.users, func(u client.User) bool { return u.ID == userID })
	type hop struct{ Type, ID string }
	var out []map[string]any
	for groupID, users := range f.members {
		if slices.Contains(users, userID) {
			path := []map[string]hop{{"from": {"user", userID}, "to": {"user_group", groupID}}}
			out = append(out, map[string]any{"id": groupID, "type": "user_group", "paths": [][]map[string]hop{path}})
		}
	}
	f.mu.Unlock()
	if !known {
		writeFake(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	writeFake(w, http.StatusOK, page(r, out))
}

// listUsers supports the filter[i]=field:$eq:value form the client sends.
func (f *fakeJumpCloud) listUsers(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	users := slices.Clone(f.users)
	f.mu.Unlock()

	var out []client.User
	for _, u := range users {
		raw, _ := json.Marshal(u)
		var fields map[string]any
		_ = json.Unmarshal(raw, &fields)
		match := true
		for key, values := range r.URL.Query() {
			if !strings.HasPrefix(key, "filter[") {
				continue
			}
			field, value, _ := strings.Cut(values[0], ":$eq:")
			match = match && fmt.Sprint(fields[field]) == value
		}
		if match {
			out = append(out, u)
		}
	}
	writeFake(w, http.StatusOK, map[string]any{"results": page(r, out), "totalCount": len(out)})
}

func page[T any](r *http.Request, items []T) []T {
	skip, _ := strconv.Atoi(r.URL.Query().Get("skip"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if skip >= len(items) {
		return []T{}
	}
	return items[skip:min(skip+limit, len(items))]
}

func writeFake(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

var testProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"jumpcloud": providerserver.NewProtocol6WithError(New("test")()),
}
