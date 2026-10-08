package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
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
// can be tested through Terraform without credentials. User group behavior mirrors what
// the real API was observed to do, including its quirks.
type fakeJumpCloud struct {
	url string

	mu      sync.Mutex
	nextID  int
	groups  map[string]fakeGroup
	members map[string][]string // group ID -> user IDs
	users   []client.User
	failAdd string // user ID whose member adds fail with 500
	apps    map[string]client.Application
	sso     map[string]fakeSSO
	assocs  map[string][]string // "<app ID>/<type>" -> target IDs
	renames int                 // v1 PUT /api/applications/{id} calls
}

// fakeGroup is a user group as JumpCloud returns it.
type fakeGroup struct {
	ID                      string                     `json:"id"`
	Type                    string                     `json:"type"`
	Name                    string                     `json:"name"`
	Description             string                     `json:"description"`
	Email                   string                     `json:"email"`
	MembershipMethod        string                     `json:"membershipMethod"`
	MemberQuery             json.RawMessage            `json:"memberQuery"`
	MemberQueryExemptions   []map[string]string        `json:"memberQueryExemptions"`
	MemberSuggestionsNotify bool                       `json:"memberSuggestionsNotify"`
	MemberQueryErrorFlags   []string                   `json:"memberQueryErrorFlags"`
	Attributes              map[string]json.RawMessage `json:"attributes"`
}

func (g fakeGroup) dynamic() bool { return strings.HasPrefix(g.MembershipMethod, "DYNAMIC") }

func (g fakeGroup) exempt(userID string) bool {
	return slices.ContainsFunc(g.MemberQueryExemptions, func(e map[string]string) bool { return e["id"] == userID })
}

func newFakeJumpCloud(t *testing.T) *fakeJumpCloud {
	t.Helper()
	f := &fakeJumpCloud{
		groups: map[string]fakeGroup{}, members: map[string][]string{},
		apps: map[string]client.Application{}, sso: map[string]fakeSSO{}, assocs: map[string][]string{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v2/usergroups", f.createGroup)
	mux.HandleFunc("GET /api/v2/usergroups", f.listGroups)
	mux.HandleFunc("GET /api/v2/usergroups/{id}", f.getGroup)
	mux.HandleFunc("PUT /api/v2/usergroups/{id}", f.updateGroup)
	mux.HandleFunc("DELETE /api/v2/usergroups/{id}", f.removeGroup)
	mux.HandleFunc("GET /api/v2/usergroups/{id}/members", f.listMembers)
	mux.HandleFunc("POST /api/v2/usergroups/{id}/members", f.changeMember)
	mux.HandleFunc("GET /api/v2/users/{id}/memberof", f.memberOf)
	mux.HandleFunc("GET /api/systemusers", f.listUsers)
	mux.HandleFunc("GET /api/systemusers/{id}", f.getUser)
	mux.HandleFunc("POST /api/applications", f.createApp)
	mux.HandleFunc("GET /api/applications", f.listApps)
	mux.HandleFunc("GET /api/applications/{id}", f.getApp)
	mux.HandleFunc("PUT /api/applications/{id}", f.renameApp)
	mux.HandleFunc("DELETE /api/applications/{id}", f.deleteApp)
	mux.HandleFunc("POST /api/v2/applications/{id}/sso", f.createSSO)
	mux.HandleFunc("GET /api/v2/applications/{id}/sso", f.getSSO)
	mux.HandleFunc("PUT /api/v2/applications/{id}/sso", f.updateSSO)
	mux.HandleFunc("GET /api/v2/applications/{id}/associations", f.listAssocs)
	mux.HandleFunc("POST /api/v2/applications/{id}/associations", f.changeAssoc)

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
	f.groups[id] = fakeGroup{
		ID: id, Type: "user_group", Name: name, Description: description, MembershipMethod: "STATIC",
		MemberQueryExemptions: []map[string]string{}, MemberQueryErrorFlags: []string{},
		Attributes: map[string]json.RawMessage{"ldapGroups": fakeJSON([]map[string]string{{"name": name}})},
	}
	return id
}

// editGroup changes a group as the admin console would, then reevaluates its rule.
func (f *fakeJumpCloud) editGroup(id string, edit func(*fakeGroup)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	g := f.groups[id]
	edit(&g)
	f.groups[id] = g
	f.evaluate(id)
}

func (f *fakeJumpCloud) deleteGroup(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.groups, id)
	delete(f.members, id)
}

func (f *fakeJumpCloud) group(id string) (fakeGroup, bool) {
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

func (f *fakeJumpCloud) knownUser(id string) bool {
	return slices.ContainsFunc(f.users, func(u client.User) bool { return u.ID == id })
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

// fakeRuleFields maps the rule fields the fake evaluates to user fields. JumpCloud accepts
// any field; unknown ones match nobody.
var fakeRuleFields = map[string]func(client.User) string{
	"user.email":           func(u client.User) string { return u.Email },
	"user.username":        func(u client.User) string { return u.Username },
	"user.user_department": func(u client.User) string { return u.Department },
}

// evaluate applies a dynamic group's rule: exempt users keep their current membership and
// every other user is a member exactly when they match. JumpCloud does this within
// seconds; the fake does it at once. Callers hold f.mu.
func (f *fakeJumpCloud) evaluate(id string) {
	g := f.groups[id]
	if !g.dynamic() {
		return
	}
	var query struct {
		Filters []struct{ Field, Operation, Value string }
	}
	_ = json.Unmarshal(g.MemberQuery, &query)
	var members []string
	for _, u := range f.users {
		match := len(query.Filters) > 0
		for _, filter := range query.Filters {
			field := fakeRuleFields[filter.Field]
			match = match && filter.Operation == "equals" && field != nil && field(u) == filter.Value
		}
		if g.exempt(u.ID) && slices.Contains(f.members[id], u.ID) || !g.exempt(u.ID) && match {
			members = append(members, u.ID)
		}
	}
	f.members[id] = members
}

func (f *fakeJumpCloud) createGroup(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.nextID++
	id := fmt.Sprintf("g%d", f.nextID)
	f.mu.Unlock()
	f.writeGroup(w, r, id, http.StatusCreated)
}

func (f *fakeJumpCloud) updateGroup(w http.ResponseWriter, r *http.Request) {
	if _, ok := f.group(r.PathValue("id")); !ok {
		writeFake(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	f.writeGroup(w, r, r.PathValue("id"), http.StatusOK)
}

// writeGroup handles POST and PUT, which both replace the whole group.
func (f *fakeJumpCloud) writeGroup(w http.ResponseWriter, r *http.Request, id string, status int) {
	var g fakeGroup
	if err := json.NewDecoder(r.Body).Decode(&g); err != nil {
		writeFake(w, http.StatusBadRequest, map[string]string{"message": err.Error()})
		return
	}
	var query map[string]json.RawMessage
	_ = json.Unmarshal(g.MemberQuery, &query)
	if _, v1 := query["queryType"]; query != nil && !v1 && r.Header.Get("x-query-dsl") != "v2" {
		writeFake(w, http.StatusBadRequest, map[string]string{"message": "memberQuery.queryType is required"})
		return
	}
	if g.MemberQueryExemptions == nil {
		g.MemberQueryExemptions = []map[string]string{}
	}
	for _, e := range g.MemberQueryExemptions {
		if e["type"] != "USER" {
			writeFake(w, http.StatusBadRequest, map[string]string{"message": "member query exemptions must be of type user"})
			return
		}
		f.mu.Lock()
		known := f.knownUser(e["id"])
		f.mu.Unlock()
		if !known {
			writeFake(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
			return
		}
		e["organizationId"] = "org"
	}
	if g.Attributes == nil {
		g.Attributes = map[string]json.RawMessage{}
	}
	if _, ok := g.Attributes["ldapGroups"]; !ok {
		g.Attributes["ldapGroups"] = fakeJSON([]map[string]string{{"name": g.Name}})
	}
	if g.MembershipMethod == "" {
		g.MembershipMethod = "STATIC"
	}
	g.ID, g.Type, g.MemberQueryErrorFlags = id, "user_group", []string{}

	f.mu.Lock()
	if old, ok := f.groups[id]; ok && old.Attributes["posixGroups"] != nil && !jsonEqual(old.Attributes["posixGroups"], g.Attributes["posixGroups"]) {
		f.mu.Unlock()
		writeFake(w, http.StatusBadRequest, map[string]string{"message": "existing attribute posixGroups may not be changed"})
		return
	}
	f.groups[id] = g
	f.evaluate(id)
	f.mu.Unlock()
	writeFake(w, status, g)
}

func (f *fakeJumpCloud) getGroup(w http.ResponseWriter, r *http.Request) {
	g, ok := f.group(r.PathValue("id"))
	if !ok {
		writeFake(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	writeFake(w, http.StatusOK, g)
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
	var out []fakeGroup
	for _, g := range f.groups {
		if !filtered || g.Name == name {
			out = append(out, g)
		}
	}
	f.mu.Unlock()
	slices.SortFunc(out, func(a, b fakeGroup) int { return strings.Compare(a.ID, b.ID) })
	// Like JumpCloud, the list omits membership fields; only GET by ID returns them.
	summaries := make([]map[string]any, 0, len(out))
	for _, g := range out {
		summaries = append(summaries, map[string]any{
			"id": g.ID, "name": g.Name, "description": g.Description, "email": g.Email, "type": "user_group",
		})
	}
	writeFake(w, http.StatusOK, summaries)
}

func (f *fakeJumpCloud) listMembers(w http.ResponseWriter, r *http.Request) {
	groupID := r.PathValue("id")
	if _, ok := f.group(groupID); !ok {
		writeFake(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	f.mu.Lock()
	users := slices.Clone(f.members[groupID])
	f.mu.Unlock()
	skip, _ := strconv.Atoi(r.URL.Query().Get("skip"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	out := []map[string]any{}
	for _, u := range users[min(skip, len(users)):min(skip+limit, len(users))] {
		out = append(out, map[string]any{"to": map[string]string{"id": u, "type": "user"}})
	}
	writeFake(w, http.StatusOK, out)
}

func (f *fakeJumpCloud) changeMember(w http.ResponseWriter, r *http.Request) {
	groupID := r.PathValue("id")
	var op struct{ Op, Type, ID string }
	if err := json.NewDecoder(r.Body).Decode(&op); err != nil || op.Type != "user" {
		writeFake(w, http.StatusBadRequest, map[string]string{"message": "invalid operation"})
		return
	}
	f.mu.Lock()
	g, ok := f.groups[groupID]
	known := f.knownUser(op.ID)
	f.mu.Unlock()
	if !ok {
		writeFake(w, http.StatusBadRequest, map[string]string{"error": "INVALID_ARGUMENT", "message": "user_group not found"})
		return
	}
	if !known {
		writeFake(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	member := f.isMember(groupID, op.ID)
	switch {
	case op.Op == "add" && op.ID == f.failAdd:
		writeFake(w, http.StatusInternalServerError, map[string]string{"message": "Internal Server Error"})
	case op.Op == "add" && member:
		writeFake(w, http.StatusConflict, map[string]string{"message": "Already Exists"})
	case op.Op == "add" && g.dynamic() && !g.exempt(op.ID):
		// JumpCloud accepts the add but the rule keeps deciding, so nothing changes.
		w.WriteHeader(http.StatusNoContent)
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

// memberOf answers 200 with an empty list for unknown users, as JumpCloud does.
func (f *fakeJumpCloud) memberOf(w http.ResponseWriter, r *http.Request) {
	userID := r.PathValue("id")
	f.mu.Lock()
	type hop struct{ Type, ID string }
	out := []map[string]any{}
	for groupID, users := range f.members {
		if slices.Contains(users, userID) {
			path := []map[string]hop{{"from": {"user", userID}, "to": {"user_group", groupID}}}
			out = append(out, map[string]any{"id": groupID, "type": "user_group", "paths": [][]map[string]hop{path}})
		}
	}
	f.mu.Unlock()
	writeFake(w, http.StatusOK, out)
}

func (f *fakeJumpCloud) getUser(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	i := slices.IndexFunc(f.users, func(u client.User) bool { return u.ID == r.PathValue("id") })
	var u client.User
	if i >= 0 {
		u = f.users[i]
	}
	f.mu.Unlock()
	if i < 0 {
		writeFake(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	writeFake(w, http.StatusOK, u)
}

func (f *fakeJumpCloud) deleteUser(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users = slices.DeleteFunc(f.users, func(u client.User) bool { return u.ID == id })
	for g := range f.members {
		f.members[g] = slices.DeleteFunc(f.members[g], func(u string) bool { return u == id })
	}
}

func fakeJSON(v any) json.RawMessage {
	raw, _ := json.Marshal(v)
	return raw
}

func jsonEqual(a, b json.RawMessage) bool {
	var x, y any
	_ = json.Unmarshal(a, &x)
	_ = json.Unmarshal(b, &y)
	return reflect.DeepEqual(x, y)
}

func (f *fakeJumpCloud) listUsers(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	users := slices.Clone(f.users)
	f.mu.Unlock()
	writeFake(w, http.StatusOK, map[string]any{"results": filtered(r, users)})
}

// filtered applies the filter[i]=field:$eq:value query the client sends to v1 lists.
func filtered[T any](r *http.Request, items []T) []T {
	var out []T
	for _, item := range items {
		raw, _ := json.Marshal(item)
		var fields map[string]any
		_ = json.Unmarshal(raw, &fields)
		match := true
		for key, values := range r.URL.Query() {
			if strings.HasPrefix(key, "filter[") {
				field, value, _ := strings.Cut(values[0], ":$eq:")
				match = match && fmt.Sprint(fields[field]) == value
			}
		}
		if match {
			out = append(out, item)
		}
	}
	return out
}

func writeFake(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

var testProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"jumpcloud": providerserver.NewProtocol6WithError(New("test")()),
}
