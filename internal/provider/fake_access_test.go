package provider

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"

	"github.com/loadsmart/terraform-provider-jumpcloud/internal/client"
)

// createUser gives new users the organization's default state, ACTIVATED here, when
// the request does not set one.
func (f *fakeJumpCloud) createUser(w http.ResponseWriter, r *http.Request) {
	var u client.User
	_ = json.NewDecoder(r.Body).Decode(&u)
	if u.Username == "" || u.Email == "" {
		writeFake(w, http.StatusBadRequest, map[string]string{"message": "username and email are required"})
		return
	}
	if u.State == "" {
		u.State = "ACTIVATED"
	}
	if u.Attributes == nil {
		u.Attributes = []client.UserAttribute{}
	}
	f.mu.Lock()
	f.nextID++
	u.ID = fmt.Sprintf("u%d", f.nextID)
	f.users = append(f.users, u)
	f.mu.Unlock()
	writeFake(w, http.StatusOK, u)
}

// updateUser changes only the fields in the request, like the v1 PUT.
func (f *fakeJumpCloud) updateUser(w http.ResponseWriter, r *http.Request) {
	var body map[string]json.RawMessage
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	defer f.mu.Unlock()
	i := slices.IndexFunc(f.users, func(u client.User) bool { return u.ID == r.PathValue("id") })
	if i < 0 {
		writeFake(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	current := map[string]json.RawMessage{}
	_ = json.Unmarshal(fakeJSON(f.users[i]), &current)
	maps.Copy(current, body)
	var u client.User
	_ = json.Unmarshal(fakeJSON(current), &u)
	f.users[i] = u
	writeFake(w, http.StatusOK, u)
}

func (f *fakeJumpCloud) removeUser(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	f.mu.Lock()
	found := slices.ContainsFunc(f.users, func(u client.User) bool { return u.ID == id })
	f.mu.Unlock()
	if !found {
		writeFake(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	f.deleteUser(id)
	writeFake(w, http.StatusOK, map[string]string{"_id": id})
}

func (f *fakeJumpCloud) user(id string) (client.User, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := slices.IndexFunc(f.users, func(u client.User) bool { return u.ID == id })
	if i < 0 {
		return client.User{}, false
	}
	return f.users[i], true
}

func (f *fakeJumpCloud) createIPList(w http.ResponseWriter, r *http.Request) {
	var l client.IPList
	_ = json.NewDecoder(r.Body).Decode(&l)
	f.mu.Lock()
	f.nextID++
	l.ID = fmt.Sprintf("ip%d", f.nextID)
	f.ipLists[l.ID] = l
	f.mu.Unlock()
	writeFake(w, http.StatusCreated, l)
}

func (f *fakeJumpCloud) getIPList(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	l, ok := f.ipLists[r.PathValue("id")]
	f.mu.Unlock()
	if !ok {
		writeFake(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	writeFake(w, http.StatusOK, l)
}

func (f *fakeJumpCloud) replaceIPList(w http.ResponseWriter, r *http.Request) {
	var l client.IPList
	_ = json.NewDecoder(r.Body).Decode(&l)
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.ipLists[r.PathValue("id")]; !ok {
		writeFake(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	l.ID = r.PathValue("id")
	f.ipLists[l.ID] = l
	writeFake(w, http.StatusOK, l)
}

func (f *fakeJumpCloud) deleteIPList(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	_, ok := f.ipLists[r.PathValue("id")]
	delete(f.ipLists, r.PathValue("id"))
	f.mu.Unlock()
	if !ok {
		writeFake(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	writeFake(w, http.StatusOK, map[string]string{})
}

// storePolicy fills in what the real API was observed to return: resources targeting
// every app or portal come back with "id": "", and userGroups is always present.
func storePolicy(p map[string]any) map[string]any {
	targets, _ := p["targets"].(map[string]any)
	if targets == nil {
		targets = map[string]any{}
	}
	resources, _ := targets["resources"].([]any)
	for _, res := range resources {
		if m, ok := res.(map[string]any); ok {
			if _, has := m["id"]; !has {
				m["id"] = ""
			}
		}
	}
	if _, ok := targets["userGroups"]; !ok {
		targets["userGroups"] = map[string]any{"inclusions": []any{}, "exclusions": []any{}}
	}
	p["targets"] = targets
	return p
}

func (f *fakeJumpCloud) createPolicy(w http.ResponseWriter, r *http.Request) {
	var p map[string]any
	_ = json.NewDecoder(r.Body).Decode(&p)
	f.mu.Lock()
	f.nextID++
	id := fmt.Sprintf("p%d", f.nextID)
	p["id"] = id
	f.policies[id] = storePolicy(p)
	f.mu.Unlock()
	writeFake(w, http.StatusCreated, p)
}

func (f *fakeJumpCloud) getPolicy(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	p, ok := f.policies[r.PathValue("id")]
	f.mu.Unlock()
	if !ok {
		writeFake(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	writeFake(w, http.StatusOK, p)
}

// patchPolicy replaces the top-level keys in the request and keeps the others.
func (f *fakeJumpCloud) patchPolicy(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.policies[r.PathValue("id")]
	if !ok {
		writeFake(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	if _, set := body["id"]; set {
		writeFake(w, http.StatusBadRequest, map[string]string{"message": "test fake: id is read-only"})
		return
	}
	maps.Copy(p, body)
	f.policies[r.PathValue("id")] = storePolicy(p)
	writeFake(w, http.StatusOK, p)
}

func (f *fakeJumpCloud) deletePolicy(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	_, ok := f.policies[r.PathValue("id")]
	delete(f.policies, r.PathValue("id"))
	f.mu.Unlock()
	if !ok {
		writeFake(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	writeFake(w, http.StatusOK, map[string]string{})
}

// policy returns a stored policy as plain JSON types, for test checks.
func (f *fakeJumpCloud) policy(id string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out map[string]any
	_ = json.Unmarshal(fakeJSON(f.policies[id]), &out)
	return out
}

// setPolicyField changes a policy as the admin console would.
func (f *fakeJumpCloud) setPolicyField(id string, edit func(map[string]any)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	edit(f.policies[id])
}
