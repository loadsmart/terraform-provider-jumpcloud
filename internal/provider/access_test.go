package provider

import (
	"fmt"
	"reflect"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/loadsmart/terraform-provider-jumpcloud/internal/client"
)

func TestUserResource(t *testing.T) {
	f := newFakeJumpCloud(t)
	const addr = "jumpcloud_user.test"
	var id string
	config := func(extra string) string {
		return f.providerConfig() + fmt.Sprintf(`
resource "jumpcloud_user" "test" {
  username   = "synthetic.test"
  email      = "synthetic.test@example.com"
  first_name = "Synthetic"
  last_name  = "Test"
  %s
}`, extra)
	}
	jumpcloud := func(check func(client.User) error) resource.TestCheckFunc {
		return func(*terraform.State) error {
			u, ok := f.user(id)
			if !ok {
				return fmt.Errorf("user %s not found", id)
			}
			return check(u)
		}
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			if _, ok := f.user(id); ok {
				return fmt.Errorf("user %s left after destroy", id)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config(""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith(addr, "id", func(v string) error { id = v; return nil }),
					resource.TestCheckResourceAttr(addr, "state", "ACTIVATED"),
					resource.TestCheckResourceAttr(addr, "department", ""),
					resource.TestCheckResourceAttr(addr, "attributes.%", "0"),
				),
			},
			{
				// Attributes set in the console are kept while attributes is not configured.
				PreConfig: func() {
					f.mu.Lock()
					for i := range f.users {
						if f.users[i].ID == id {
							f.users[i].Attributes = []client.UserAttribute{{Name: "team", Value: "Platform"}}
						}
					}
					f.mu.Unlock()
				},
				Config: config(`department = "Engineering"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "department", "Engineering"),
					resource.TestCheckResourceAttr(addr, "attributes.team", "Platform"),
					jumpcloud(func(u client.User) error {
						if u.Department != "Engineering" || len(u.Attributes) != 1 {
							return fmt.Errorf("JumpCloud has %+v", u)
						}
						return nil
					}),
				),
			},
			{
				Config: config(`
  department = "Engineering"
  state      = "SUSPENDED"
  attributes = { division = "Platform" }`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "state", "SUSPENDED"),
					resource.TestCheckResourceAttr(addr, "attributes.%", "1"),
					resource.TestCheckResourceAttr(addr, "attributes.division", "Platform"),
					jumpcloud(func(u client.User) error {
						if u.State != "SUSPENDED" || !reflect.DeepEqual(u.Attributes, []client.UserAttribute{{Name: "division", Value: "Platform"}}) {
							return fmt.Errorf("JumpCloud has %+v", u)
						}
						return nil
					}),
				),
			},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true},
		},
	})
}

func TestUserResourceValidation(t *testing.T) {
	f := newFakeJumpCloud(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{{
			Config: f.providerConfig() + `
resource "jumpcloud_user" "test" {
  username = "x"
  email    = "x@example.com"
  state    = "DISABLED"
}`,
			ExpectError: regexp.MustCompile(`state must be one of`),
		}},
	})
}

func TestIPListResource(t *testing.T) {
	f := newFakeJumpCloud(t)
	const addr = "jumpcloud_ip_list.test"
	config := func(ips string) string {
		return f.providerConfig() + fmt.Sprintf(`
resource "jumpcloud_ip_list" "test" {
  name = "datadog-synthetics"
  ips  = [%s]
}`, ips)
	}
	var id string
	stored := func(want ...string) resource.TestCheckFunc {
		return func(*terraform.State) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			got := f.ipLists[id].IPs
			if !reflect.DeepEqual(got, want) {
				return fmt.Errorf("JumpCloud has %v, want %v", got, want)
			}
			return nil
		}
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.ipLists) != 0 {
				return fmt.Errorf("%d IP lists left after destroy", len(f.ipLists))
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config(`"203.0.113.7"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith(addr, "id", func(v string) error { id = v; return nil }),
					resource.TestCheckResourceAttr(addr, "description", ""),
					resource.TestCheckResourceAttr(addr, "ips.#", "1"),
					stored("203.0.113.7"),
				),
			},
			{
				Config: config(`"203.0.113.7", "198.51.100.0/24"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith(addr, "id", func(v string) error { return expect(v, id) }),
					resource.TestCheckResourceAttr(addr, "ips.#", "2"),
				),
			},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true},
		},
	})
}

func TestAuthenticationPolicyResource(t *testing.T) {
	f := newFakeJumpCloud(t)
	const addr = "jumpcloud_authentication_policy.test"
	group := f.addGroup("synthetic-test", "")
	var id string
	stored := func(check func(map[string]any) error) resource.TestCheckFunc {
		return func(*terraform.State) error { return check(f.policy(id)) }
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.policies) != 0 {
				return fmt.Errorf("%d policies left after destroy", len(f.policies))
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				// The Okta synthetic-login setup: no MFA for a test user from known IPs.
				Config: f.providerConfig() + fmt.Sprintf(`
resource "jumpcloud_ip_list" "synthetics" {
  name = "datadog-synthetics"
  ips  = ["203.0.113.7"]
}

resource "jumpcloud_authentication_policy" "test" {
  name           = "Synthetic test - MFA not required"
  type           = "user_portal"
  action         = "allow"
  user_group_ids = [%q]
  conditions     = jsonencode({ ipAddressIn = [jumpcloud_ip_list.synthetics.id] })
}`, group),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith(addr, "id", func(v string) error { id = v; return nil }),
					resource.TestCheckResourceAttr(addr, "mfa_required", "false"),
					resource.TestCheckResourceAttr(addr, "all_users", "false"),
					resource.TestCheckResourceAttr(addr, "application_ids.#", "0"),
					resource.TestCheckResourceAttr(addr, "user_group_ids.#", "1"),
					stored(func(p map[string]any) error {
						want := map[string]any{"action": "allow", "obligations": map[string]any{"mfa": map[string]any{"required": false}}}
						if !reflect.DeepEqual(p["effect"], want) {
							return fmt.Errorf("effect = %v", p["effect"])
						}
						targets := field(p, "targets")
						if !reflect.DeepEqual(targets["resources"], []any{map[string]any{"type": "user_portal", "id": ""}}) {
							return fmt.Errorf("resources = %v", targets["resources"])
						}
						if _, ok := targets["excludedApplications"]; ok {
							return fmt.Errorf("user portal policy has excludedApplications")
						}
						return nil
					}),
				),
			},
			{
				// Settings made in the console survive an update.
				PreConfig: func() {
					f.setPolicyField(id, func(p map[string]any) {
						field(p, "effect", "obligations")["mfaFactors"] = []any{map[string]any{"type": "TOTP"}}
						p["custom_error_message"] = map[string]any{"enabled": true, "message": "Use the VPN"}
					})
				},
				Config: f.providerConfig() + `
resource "jumpcloud_authentication_policy" "test" {
  name         = "Require MFA on the portal"
  type         = "user_portal"
  action       = "allow"
  mfa_required = true
  all_users    = true
}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith(addr, "id", func(v string) error { return expect(v, id) }),
					resource.TestCheckResourceAttr(addr, "mfa_required", "true"),
					resource.TestCheckResourceAttr(addr, "all_users", "true"),
					resource.TestCheckResourceAttr(addr, "user_group_ids.#", "0"),
					resource.TestCheckNoResourceAttr(addr, "conditions"),
					stored(func(p map[string]any) error {
						obligations := field(p, "effect", "obligations")
						if obligations["mfaFactors"] == nil || p["custom_error_message"] == nil {
							return fmt.Errorf("console settings lost: %v", p)
						}
						if !reflect.DeepEqual(p["conditions"], map[string]any{}) {
							return fmt.Errorf("conditions = %v", p["conditions"])
						}
						return nil
					}),
				),
			},
			{
				Config: f.providerConfig() + `
resource "jumpcloud_authentication_policy" "test" {
  name            = "Block outside US and BR"
  type            = "application"
  action          = "deny"
  all_users       = true
  monitor_only    = true
  application_ids = ["a2", "a1"]
  conditions      = jsonencode({ not = { locationIn = { countries = ["US", "BR"] } } })
}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					// Changing type replaces the policy.
					resource.TestCheckResourceAttrWith(addr, "id", func(v string) error {
						if v == id {
							return fmt.Errorf("expected a new policy")
						}
						id = v
						return nil
					}),
					resource.TestCheckResourceAttr(addr, "monitor_only", "true"),
					resource.TestCheckResourceAttr(addr, "application_ids.#", "2"),
					resource.TestCheckResourceAttr(addr, "excluded_application_ids.#", "0"),
					stored(func(p map[string]any) error {
						if !reflect.DeepEqual(p["effect"], map[string]any{"action": "deny"}) {
							return fmt.Errorf("deny policy has effect %v", p["effect"])
						}
						want := []any{map[string]any{"type": "application", "id": "a1"}, map[string]any{"type": "application", "id": "a2"}}
						if got := field(p, "targets")["resources"]; !reflect.DeepEqual(got, want) {
							return fmt.Errorf("resources = %v", got)
						}
						return nil
					}),
				),
			},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true},
		},
	})
}

func TestAuthenticationPolicyValidation(t *testing.T) {
	f := newFakeJumpCloud(t)
	config := func(body string) string {
		return f.providerConfig() + `resource "jumpcloud_authentication_policy" "test" {
  name = "x"
` + body + "\n}"
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{Config: config(`type = "vpn"` + "\n" + `action = "allow"`), ExpectError: regexp.MustCompile(`type must be one of`)},
			{Config: config(`type = "user_portal"` + "\n" + `action = "deny"` + "\n" + `mfa_required = true`), ExpectError: regexp.MustCompile(`Only allow policies can require MFA`)},
			{Config: config(`type = "user_portal"` + "\n" + `action = "deny"` + "\n" + `application_ids = ["a1"]`), ExpectError: regexp.MustCompile(`only valid when type is application`)},
		},
	})
}

func TestApplicationDataSourceDetails(t *testing.T) {
	f := newFakeJumpCloud(t)
	bookmark := f.addApp("The Hub", "bookmark")
	const data = "data.jumpcloud_application"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{{
			Config: f.providerConfig() + `
resource "jumpcloud_saml_application" "n8n" {
  display_label  = "n8n"
  show_in_portal = true
  sp_entity_id   = "https://n8n.example.com/metadata"
  acs_urls       = ["https://n8n.example.com/acs"]
}

data "jumpcloud_application" "saml" {
  display_label = jumpcloud_saml_application.n8n.display_label
}

resource "jumpcloud_oidc_application" "grafana" {
  display_label = "grafana"
  redirect_uris = ["https://grafana.example.com/login"]
  login_url     = "https://grafana.example.com"
}

data "jumpcloud_application" "oidc" {
  display_label = jumpcloud_oidc_application.grafana.display_label
}

data "jumpcloud_application" "bookmark" {
  display_label = "The Hub"
}`,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr(data+".saml", "type", "saml"),
				resource.TestCheckResourceAttr(data+".saml", "template", "saml2"),
				resource.TestCheckResourceAttr(data+".saml", "show_in_portal", "true"),
				resource.TestCheckResourceAttrPair(data+".saml", "sso_url", "jumpcloud_saml_application.n8n", "sso_url"),
				resource.TestCheckResourceAttrPair(data+".saml", "idp_entity_id", "jumpcloud_saml_application.n8n", "idp_entity_id"),
				resource.TestCheckResourceAttrPair(data+".saml", "idp_certificate", "jumpcloud_saml_application.n8n", "idp_certificate"),
				resource.TestCheckResourceAttrPair(data+".oidc", "id", "jumpcloud_oidc_application.grafana", "id"),
				resource.TestCheckResourceAttr(data+".oidc", "type", "oidc"),
				resource.TestCheckResourceAttr(data+".oidc", "idp_certificate", ""),
				resource.TestCheckResourceAttr(data+".bookmark", "id", bookmark),
				resource.TestCheckResourceAttr(data+".bookmark", "type", "bookmark"),
			),
		}},
	})
}

// field follows keys through nested JSON objects, returning an empty map when one is missing.
func field(doc map[string]any, keys ...string) map[string]any {
	for _, k := range keys {
		next, ok := doc[k].(map[string]any)
		if !ok {
			return map[string]any{}
		}
		doc = next
	}
	return doc
}
