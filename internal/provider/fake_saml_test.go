package provider

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"

	"github.com/loadsmart/terraform-provider-jumpcloud/internal/client"
)

// fakeSAMLTemplate is a catalog template: each has its own config keys and defaults,
// and catalog apps have fewer keys than custom ones.
type fakeSAMLTemplate struct {
	displayName, ssoType string
	config               map[string]any
}

// fakeSAMLApp is a SAML app as stored: its document and its config values.
type fakeSAMLApp struct {
	doc    map[string]any
	config map[string]any
}

var fakeSAMLTemplates = map[string]fakeSAMLTemplate{
	"saml2": {"SAML2.0", "saml", map[string]any{
		"idpEntityId": "", "idpPrivateKey": "", "idpCertificate": "", "spEntityId": "", "acsUrl": "https://YOUR_ACS_URL/",
		"subjectField": "email", "overrideNameIdFormat": "urn:oasis:names:tc:SAML:1.0:nameid-format:unspecified",
		"signResponse": false, "signAssertion": true, "signatureAlgorithm": "RSA-SHA256",
		"includeGroups": false, "groupsAttributeName": "", "constantAttributes": []any{}, "databaseAttributes": []any{},
		"defaultTargetUrl": "", "idpInitUrl": "",
	}},
	"aws-sso": {"AWS IAM Identity Center", "saml", map[string]any{
		"idpEntityId": "JumpCloud", "idpPrivateKey": "", "idpCertificate": "",
		"spEntityId": "https://YOUR_AWS_REGION.aws.amazon.com/platform/saml/YOUR_SUBDOMAIN",
		"acsUrl":     "https://YOUR_AWS_REGION.signin.aws.amazon.com/platform/saml/acs/YOUR_SSO_ID",
		"idpInitUrl": "https://YOUR_SUBDOMAIN.awsapps.com/start", "authClaimConfiguration": nil,
	}},
	"oidc": {"OpenID Connect", "oidc", map[string]any{}},
}

func (f *fakeJumpCloud) listTemplates(w http.ResponseWriter, r *http.Request) {
	var out []map[string]any
	for name, t := range fakeSAMLTemplates {
		out = append(out, map[string]any{"name": name, "displayName": t.displayName, "sso": map[string]any{"type": t.ssoType}, "config": wrapValues(t.config)})
	}
	writeFake(w, http.StatusOK, map[string]any{"results": filtered(r, out)})
}

// writeSAMLApp handles create and update, which both replace the whole app like
// JumpCloud: settings not sent get the template's default, and an IdP certificate not
// sent is generated again.
func (f *fakeJumpCloud) writeSAMLApp(w http.ResponseWriter, id string, body map[string]any) {
	if body["active"] != true {
		writeFake(w, http.StatusBadRequest, map[string]string{"message": "test fake: SAML writes must set active"})
		return
	}
	if u, _ := body["ssoUrl"].(string); u == "" {
		writeFake(w, http.StatusBadRequest, map[string]any{"status": 400, "error": "The IdP URL is required"})
		return
	}
	template, ok := fakeSAMLTemplates[fmt.Sprint(body["name"])]
	if !ok {
		writeFake(w, http.StatusBadRequest, map[string]string{"message": "unknown template"})
		return
	}
	config := maps.Clone(template.config)
	sent, _ := body["config"].(map[string]any)
	for k, v := range sent {
		setting, isObject := v.(map[string]any)
		if _, known := config[k]; !known || !isObject {
			writeFake(w, http.StatusBadRequest, map[string]string{"message": "test fake: bad config key " + k})
			return
		}
		config[k] = setting["value"]
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if cert, _ := config["idpCertificate"].(string); cert == "" {
		f.nextID++
		config["idpCertificate"] = base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf("-----BEGIN CERTIFICATE-----\ncert%d\n-----END CERTIFICATE-----\n", f.nextID)))
	}
	sso, _ := body["sso"].(map[string]any)
	doc := map[string]any{
		"_id": id, "name": body["name"], "displayName": template.displayName, "displayLabel": body["displayLabel"],
		"ssoUrl": body["ssoUrl"], "active": true, "config": wrapValues(config),
		"sso": map[string]any{"type": "saml", "hidden": sso["hidden"] == true},
	}
	f.saml[id] = fakeSAMLApp{doc: doc, config: config}
	f.apps[id] = client.Application{ID: id, Name: fmt.Sprint(body["name"]), DisplayLabel: fmt.Sprint(body["displayLabel"]), SSOURL: fmt.Sprint(body["ssoUrl"])}
	writeFake(w, http.StatusOK, doc)
}

// samlConfig returns an app's config values as JSON types, for test checks.
func (f *fakeJumpCloud) samlConfig(id string) map[string]any {
	f.mu.Lock()
	app, ok := f.saml[id]
	f.mu.Unlock()
	if !ok {
		return nil
	}
	var out map[string]any
	raw, _ := json.Marshal(app.config)
	_ = json.Unmarshal(raw, &out)
	return out
}

func wrapValues(values map[string]any) map[string]any {
	out := make(map[string]any, len(values))
	for k, v := range values {
		out[k] = map[string]any{"value": v, "label": k + ":"}
	}
	return out
}
