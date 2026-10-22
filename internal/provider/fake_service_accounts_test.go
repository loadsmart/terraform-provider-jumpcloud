package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/loadsmart/terraform-provider-jumpcloud/internal/client"
)

// The service account handlers mirror what the real API was observed to do. Create answers
// with the client secret; reads leave it out. Both wrap the account in "serviceAccount",
// though the API reference shows GET returning it at the top level.

func (f *fakeJumpCloud) createServiceAccount(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name       string `json:"name"`
		RoleID     string `json:"roleId"`
		AuthConfig struct {
			AuthType           string `json:"authType"`
			ClientSecretConfig struct{ Lifetime string }
		} `json:"authConfig"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" || req.RoleID == "" {
		writeFake(w, http.StatusBadRequest, map[string]string{"message": "name and roleId are required"})
		return
	}
	days, err := strconv.Atoi(strings.TrimSuffix(req.AuthConfig.ClientSecretConfig.Lifetime, " Days"))
	if req.AuthConfig.AuthType != "CLIENT_SECRET" || err != nil {
		writeFake(w, http.StatusBadRequest, map[string]string{"message": "invalid auth config"})
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if !slices.ContainsFunc(f.roles, func(role client.Role) bool { return role.ID == req.RoleID }) {
		writeFake(w, http.StatusBadRequest, map[string]string{"message": "role not found"})
		return
	}
	f.nextID++
	expires := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, days).Format(time.RFC3339)
	sa := client.ServiceAccount{
		ID: fmt.Sprintf("sa%d", f.nextID), Name: req.Name, RoleID: req.RoleID, Status: "ACTIVE", ExpiresAt: expires,
		AuthConfigs: []client.AuthConfig{{AuthType: "CLIENT_SECRET", ClientSecret: &client.ClientSecret{
			ID: fmt.Sprintf("ac%d", f.nextID), ClientID: fmt.Sprintf("client-%d", f.nextID),
			Lifetime: req.AuthConfig.ClientSecretConfig.Lifetime, ExpiresAt: expires, Status: "ACTIVE",
		}}},
	}
	f.accounts[sa.ID] = sa

	created := sa
	secret := *sa.AuthConfigs[0].ClientSecret
	secret.Secret = fmt.Sprintf("secret-%d", f.nextID)
	created.AuthConfigs = []client.AuthConfig{{AuthType: "CLIENT_SECRET", ClientSecret: &secret}}
	writeFake(w, http.StatusCreated, map[string]any{"serviceAccount": created})
}

func (f *fakeJumpCloud) getServiceAccount(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	sa, ok := f.accounts[r.PathValue("id")]
	f.mu.Unlock()
	if !ok {
		writeFake(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	writeFake(w, http.StatusOK, map[string]any{"serviceAccount": sa})
}

func (f *fakeJumpCloud) deleteServiceAccount(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.accounts[r.PathValue("id")]; !ok {
		writeFake(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	delete(f.accounts, r.PathValue("id"))
	writeFake(w, http.StatusOK, map[string]any{})
}

func (f *fakeJumpCloud) listRoles(w http.ResponseWriter, _ *http.Request) {
	f.mu.Lock()
	roles := slices.Clone(f.roles)
	f.mu.Unlock()
	writeFake(w, http.StatusOK, map[string]any{"results": roles, "totalCount": len(roles)})
}

func (f *fakeJumpCloud) addRole(name string, custom bool) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	id := fmt.Sprintf("r%d", f.nextID)
	f.roles = append(f.roles, client.Role{ID: id, Name: name, Description: name + " role", IsCustom: custom})
	return id
}

func (f *fakeJumpCloud) hasServiceAccount(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.accounts[id]
	return ok
}
