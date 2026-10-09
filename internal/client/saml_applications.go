package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// SAML applications use the v1 applications API. Their settings live in "config" as
// {"<key>": {"value": ...}}, and the keys a template supports differ between templates.

// SAMLTemplateCustom is the template of custom SAML applications.
const SAMLTemplateCustom = "saml2"

// SAML config keys the provider manages.
const (
	SAMLIdPEntityID       = "idpEntityId"
	SAMLSPEntityID        = "spEntityId"
	SAMLACSURL            = "acsUrl"
	SAMLIdPInitURL        = "idpInitUrl"
	SAMLDefaultRelayState = "defaultTargetUrl"
	SAMLNameID            = "subjectField"
	SAMLNameIDFormat      = "overrideNameIdFormat"
	SAMLSignResponse      = "signResponse"
	SAMLSignAssertion     = "signAssertion"
	SAMLIncludeGroups     = "includeGroups"
	SAMLGroupsAttribute   = "groupsAttributeName"
	SAMLConstantAttrs     = "constantAttributes"
	SAMLUserAttrs         = "databaseAttributes"
	samlIdPCertificate    = "idpCertificate"
)

// ErrNotSAML is returned when an application exists but is not a SAML application.
var ErrNotSAML = errors.New("jumpcloud: application is not a SAML application")

// SAMLTemplate is an application template from the catalog.
type SAMLTemplate struct {
	Name        string
	DisplayName string
	// Defaults are the template's config values by key, so they also list the keys it supports.
	Defaults map[string]json.RawMessage
}

// Supports reports whether the template has the config key.
func (t SAMLTemplate) Supports(key string) bool {
	_, ok := t.Defaults[key]
	return ok
}

// SAMLApplication is a SAML application with its config values by key.
type SAMLApplication struct {
	ID           string
	Template     string
	DisplayLabel string
	SSOURL       string
	Hidden       bool
	Config       map[string]json.RawMessage
}

// Supports reports whether the application's template has the config key.
func (a SAMLApplication) Supports(key string) bool {
	_, ok := a.Config[key]
	return ok
}

// Certificate returns the IdP certificate in PEM. JumpCloud stores it base64-encoded.
func (a SAMLApplication) Certificate() string {
	var encoded string
	_ = json.Unmarshal(a.Config[samlIdPCertificate], &encoded)
	pem, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return encoded
	}
	return string(pem)
}

// SAMLAttribute is a constant or user attribute sent in the assertion.
type SAMLAttribute struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// ACSURLs returns the assertion consumer service URLs. Custom apps store them as a JSON
// list in a string; templates such as aws-sso store a single plain URL.
func ACSURLs(raw json.RawMessage) []string {
	var value string
	if json.Unmarshal(raw, &value) != nil || value == "" {
		return nil
	}
	var entries []struct {
		URL string `json:"url"`
	}
	if !strings.HasPrefix(value, "[") || json.Unmarshal([]byte(value), &entries) != nil {
		return []string{value}
	}
	urls := make([]string, 0, len(entries))
	for _, e := range entries {
		urls = append(urls, e.URL)
	}
	return urls
}

// ACSURLValue encodes ACS URLs for the acsUrl setting: one URL as is, several as the
// JSON list custom apps use, with the first as the default.
func ACSURLValue(urls []string) string {
	if len(urls) == 1 {
		return urls[0]
	}
	type entry struct {
		Index     string `json:"index"`
		IsDefault string `json:"isDefault"`
		URL       string `json:"url"`
	}
	entries := make([]entry, 0, len(urls))
	for i, u := range urls {
		entries = append(entries, entry{Index: fmt.Sprint(i), IsDefault: fmt.Sprint(i == 0), URL: u})
	}
	raw, _ := json.Marshal(entries) // strings always marshal
	return string(raw)
}

type samlAppDocument struct {
	ID           string `json:"_id"`
	Name         string `json:"name"`
	DisplayLabel string `json:"displayLabel"`
	DisplayName  string `json:"displayName"`
	SSOURL       string `json:"ssoUrl"`
	SSO          struct {
		Type   string `json:"type"`
		Hidden bool   `json:"hidden"`
	} `json:"sso"`
	Config map[string]struct {
		Value json.RawMessage `json:"value"`
	} `json:"config"`
}

func (d samlAppDocument) application() *SAMLApplication {
	label := d.DisplayLabel
	if label == "" {
		label = d.DisplayName
	}
	a := &SAMLApplication{ID: d.ID, Template: d.Name, DisplayLabel: label, SSOURL: d.SSOURL, Hidden: d.SSO.Hidden, Config: map[string]json.RawMessage{}}
	for k, v := range d.Config {
		a.Config[k] = v.Value
	}
	return a
}

// GetSAMLTemplate returns the catalog template with the given name, such as saml2 or
// aws-sso, and errors when it does not exist or is not a SAML template.
func (c *Client) GetSAMLTemplate(ctx context.Context, name string) (*SAMLTemplate, error) {
	type template struct {
		Name        string `json:"name"`
		DisplayName string `json:"displayName"`
		SSO         struct {
			Type string `json:"type"`
		} `json:"sso"`
		Config map[string]struct {
			Value json.RawMessage `json:"value"`
		} `json:"config"`
	}
	templates, err := list[template](ctx, c, "/api/application-templates", v1Filters(map[string]string{"name": name}), true)
	if err != nil {
		return nil, err
	}
	for _, t := range templates {
		if t.Name != name {
			continue
		}
		if t.SSO.Type != "saml" {
			return nil, fmt.Errorf("jumpcloud: application template %q is not a SAML template (type %q)", name, t.SSO.Type)
		}
		out := &SAMLTemplate{Name: t.Name, DisplayName: t.DisplayName, Defaults: map[string]json.RawMessage{}}
		for k, v := range t.Config {
			out.Defaults[k] = v.Value
		}
		return out, nil
	}
	return nil, fmt.Errorf("jumpcloud: no application template named %q", name)
}

// CreateSAMLApplication creates an application from the template. Settings not in
// config get the template's defaults, and JumpCloud generates the IdP certificate.
func (c *Client) CreateSAMLApplication(ctx context.Context, t SAMLTemplate, label, ssoURL string, hidden bool, config map[string]any) (*SAMLApplication, error) {
	values := map[string]any{}
	for k, v := range config {
		values[k] = map[string]any{"value": v}
	}
	body := map[string]any{
		"active":       true,
		"name":         t.Name,
		"displayName":  t.DisplayName,
		"displayLabel": label,
		"ssoUrl":       ssoURL,
		"sso":          map[string]any{"type": "saml", "hidden": hidden},
		"config":       values,
	}
	var created samlAppDocument
	if err := c.do(ctx, http.MethodPost, "/api/applications", nil, body, &created); err != nil {
		return nil, err
	}
	if created.ID == "" {
		return nil, errors.New("jumpcloud: POST /api/applications returned no application ID")
	}
	return created.application(), nil
}

// GetSAMLApplication returns ErrNotFound when the application does not exist and
// ErrNotSAML when it is another kind of application.
func (c *Client) GetSAMLApplication(ctx context.Context, id string) (*SAMLApplication, error) {
	doc, err := get[samlAppDocument](ctx, c, "/api/applications/"+url.PathEscape(id))
	if err != nil {
		return nil, err
	}
	if doc.SSO.Type != "saml" {
		return nil, fmt.Errorf("application %s: %w", id, ErrNotSAML)
	}
	return doc.application(), nil
}

// UpdateSAMLApplication changes the label, SSO URL, portal visibility, and the given
// config values. PUT is a full replace that resets omitted settings to the template
// defaults and regenerates an omitted IdP certificate, which breaks sign-in until the
// service provider gets the new one. So the current app is read first and every setting
// is sent back, changed or not.
func (c *Client) UpdateSAMLApplication(ctx context.Context, id, label, ssoURL string, hidden bool, config map[string]any) (*SAMLApplication, error) {
	path := "/api/applications/" + url.PathEscape(id)
	var current map[string]json.RawMessage
	if err := c.do(ctx, http.MethodGet, path, nil, nil, &current); err != nil {
		return nil, err
	}
	var settings map[string]map[string]any
	if err := json.Unmarshal(current["config"], &settings); err != nil {
		return nil, fmt.Errorf("jumpcloud: decoding application %s config: %w", id, err)
	}
	for k, v := range config {
		if settings[k] == nil {
			settings[k] = map[string]any{}
		}
		settings[k]["value"] = v
	}

	body := map[string]any{
		"active":       true,
		"displayLabel": label,
		"ssoUrl":       ssoURL,
		"sso":          map[string]any{"type": "saml", "hidden": hidden},
		"config":       settings,
	}
	for _, k := range []string{"name", "displayName", "description"} {
		if v, ok := current[k]; ok {
			body[k] = v
		}
	}
	if err := c.do(ctx, http.MethodPut, path, nil, body, nil); err != nil {
		return nil, err
	}
	return c.GetSAMLApplication(ctx, id)
}
