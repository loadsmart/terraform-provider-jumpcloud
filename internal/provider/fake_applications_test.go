package provider

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"

	"github.com/loadsmart/terraform-provider-jumpcloud/internal/client"
)

// fakeSSO is an app's /sso document. OIDC keeps unknown fields, as JumpCloud does, so
// tests can check that updates send back fields the provider does not manage.
type fakeSSO struct {
	Hidden bool           `json:"hidden"`
	OIDC   map[string]any `json:"oidc"`
}

func (f *fakeJumpCloud) addApp(label, template string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	id := fmt.Sprintf("a%d", f.nextID)
	f.apps[id] = client.Application{ID: id, Name: template, DisplayLabel: label}
	return id
}

// addConsoleOIDCApp mimics an app created in the admin console: JumpCloud leaves
// displayLabel empty and shows displayName instead.
func (f *fakeJumpCloud) addConsoleOIDCApp() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	id := fmt.Sprintf("a%d", f.nextID)
	f.apps[id] = client.Application{ID: id, Name: "oidc", DisplayName: "OpenID Connect"}
	f.sso[id] = fakeSSO{OIDC: map[string]any{
		"clientId":                "client-" + id,
		"redirectUris":            []any{"https://example.com"},
		"grantTypes":              []any{"authorization_code"},
		"relyingPartyUrl":         "https://example.com",
		"tokenEndpointAuthMethod": "client_secret_post",
		"dynamicClaims":           []any{},
		"accessTokenLifespan":     "1h",
	}}
	return id
}

func (f *fakeJumpCloud) app(id string) (client.Application, fakeSSO, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.apps[id]
	return a, f.sso[id], ok
}

func (f *fakeJumpCloud) appCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.apps)
}

func (f *fakeJumpCloud) associated(appID, targetType, targetID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Contains(f.assocs[appID+"/"+targetType], targetID)
}

func (f *fakeJumpCloud) createApp(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	_ = json.NewDecoder(r.Body).Decode(&body)
	if _, ok := body["ssoUrl"]; ok && body["name"] == "oidc" {
		writeFake(w, http.StatusBadRequest, map[string]any{"status": 400, "error": "ssoUrl is not allowed for OIDC apps"})
		return
	}
	if body["active"] != true {
		writeFake(w, http.StatusBadRequest, map[string]string{"message": "test fake: create must set active"})
		return
	}
	id := f.addApp(fmt.Sprint(body["displayLabel"]), fmt.Sprint(body["name"]))
	a, _, _ := f.app(id)
	writeFake(w, http.StatusOK, a)
}

func (f *fakeJumpCloud) listApps(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	var apps []client.Application
	for _, a := range f.apps {
		apps = append(apps, a)
	}
	f.mu.Unlock()
	writeFake(w, http.StatusOK, map[string]any{"results": filtered(r, apps)})
}

func (f *fakeJumpCloud) getApp(w http.ResponseWriter, r *http.Request) {
	a, _, ok := f.app(r.PathValue("id"))
	if !ok {
		writeFake(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	writeFake(w, http.StatusOK, a)
}

func (f *fakeJumpCloud) renameApp(w http.ResponseWriter, r *http.Request) {
	var body struct {
		client.Application
		Active bool `json:"active"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if !body.Active {
		writeFake(w, http.StatusBadRequest, map[string]string{"message": "test fake: rename must keep active"})
		return
	}
	f.mu.Lock()
	a, ok := f.apps[r.PathValue("id")]
	if ok {
		a.DisplayLabel = body.DisplayLabel
		f.apps[a.ID] = a
		f.renames++
	}
	f.mu.Unlock()
	if !ok {
		writeFake(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	writeFake(w, http.StatusOK, a)
}

func (f *fakeJumpCloud) deleteApp(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	f.mu.Lock()
	_, ok := f.apps[id]
	delete(f.apps, id)
	delete(f.sso, id)
	f.mu.Unlock()
	if !ok {
		writeFake(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (f *fakeJumpCloud) createSSO(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body fakeSSO
	_ = json.NewDecoder(r.Body).Decode(&body)
	if _, _, ok := f.app(id); !ok {
		writeFake(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	body.OIDC["clientId"] = "client-" + id
	body.OIDC["accessTokenLifespan"] = "1h"
	f.mu.Lock()
	f.sso[id] = body
	f.mu.Unlock()

	oidc := maps.Clone(body.OIDC)
	if oidc["tokenEndpointAuthMethod"] != "none" {
		oidc["clientSecret"] = "secret-" + id
	}
	writeFake(w, http.StatusOK, map[string]any{"type": "oidc", "hidden": body.Hidden, "oidc": oidc})
}

func (f *fakeJumpCloud) getSSO(w http.ResponseWriter, r *http.Request) {
	_, sso, ok := f.app(r.PathValue("id"))
	if !ok || sso.OIDC == nil {
		writeFake(w, http.StatusNotFound, map[string]string{"message": "Not Found: oidc config not found"})
		return
	}
	writeFake(w, http.StatusOK, map[string]any{"type": "oidc", "hidden": sso.Hidden, "oidc": sso.OIDC})
}

func (f *fakeJumpCloud) updateSSO(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body fakeSSO
	_ = json.NewDecoder(r.Body).Decode(&body)
	f.mu.Lock()
	defer f.mu.Unlock()
	current, ok := f.sso[id]
	if !ok {
		writeFake(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	// JumpCloud replaces the whole OIDC object but keeps the client ID.
	body.OIDC["clientId"] = current.OIDC["clientId"]
	f.sso[id] = body
	writeFake(w, http.StatusOK, map[string]any{"type": "oidc", "hidden": body.Hidden, "oidc": body.OIDC})
}

func (f *fakeJumpCloud) listAssocs(w http.ResponseWriter, r *http.Request) {
	id, target := r.PathValue("id"), r.URL.Query().Get("targets")
	if _, _, ok := f.app(id); !ok {
		writeFake(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	f.mu.Lock()
	out := []map[string]any{}
	for _, t := range f.assocs[id+"/"+target] {
		out = append(out, map[string]any{"to": map[string]string{"type": target, "id": t}})
	}
	f.mu.Unlock()
	writeFake(w, http.StatusOK, out)
}

func (f *fakeJumpCloud) changeAssoc(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var op struct{ Op, Type, ID string }
	_ = json.NewDecoder(r.Body).Decode(&op)
	if _, _, ok := f.app(id); !ok || (op.Type != "user" && op.Type != "user_group") {
		writeFake(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
		return
	}
	key := id + "/" + op.Type
	f.mu.Lock()
	defer f.mu.Unlock()
	exists := slices.Contains(f.assocs[key], op.ID)
	switch {
	case op.Op == "add" && exists:
		writeFake(w, http.StatusConflict, map[string]string{"message": "Already Exists"})
	case op.Op == "add":
		f.assocs[key] = append(f.assocs[key], op.ID)
		w.WriteHeader(http.StatusNoContent)
	case !exists:
		writeFake(w, http.StatusNotFound, map[string]string{"message": "Not Found"})
	default:
		f.assocs[key] = slices.DeleteFunc(f.assocs[key], func(t string) bool { return t == op.ID })
		w.WriteHeader(http.StatusNoContent)
	}
}
