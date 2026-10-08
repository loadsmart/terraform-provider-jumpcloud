package provider

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/loadsmart/terraform-provider-jumpcloud/internal/client"
)

func TestOIDCApplicationResource(t *testing.T) {
	f := newFakeJumpCloud(t)
	const addr = "jumpcloud_oidc_application.test"
	config := func(label, method, claims string) string {
		return f.providerConfig() + fmt.Sprintf(`
resource "jumpcloud_oidc_application" "test" {
  display_label              = %q
  show_in_portal             = true
  redirect_uris              = ["https://grafana.example.com/login/generic_oauth", "https://grafana.example.com/alt"]
  login_url                  = "https://grafana.example.com"
  token_endpoint_auth_method = %q
  %s
}`, label, method, claims)
	}
	var id string // current application ID
	sameID := resource.TestCheckResourceAttrWith(addr, "id", func(v string) error { return expect(v, id) })
	newID := resource.TestCheckResourceAttrWith(addr, "id", func(v string) error {
		if v == id {
			return errors.New("expected a new application")
		}
		id = v
		return nil
	})
	renames := func(want int) resource.TestCheckFunc {
		return func(*terraform.State) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.renames != want {
				return fmt.Errorf("renames = %d, want %d", f.renames, want)
			}
			return nil
		}
	}
	jumpcloud := func(check func(a client.Application, sso fakeSSO) error) resource.TestCheckFunc {
		return func(*terraform.State) error {
			a, sso, _ := f.app(id)
			return check(a, sso)
		}
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			if n := f.appCount(); n != 0 {
				return fmt.Errorf("%d applications left after destroy", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: f.providerConfig() + `
resource "jumpcloud_oidc_application" "test" {
  display_label = "grafana-qa"
  redirect_uris = ["https://grafana.example.com/login/generic_oauth"]
  login_url     = "https://grafana.example.com"
}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					newID,
					resource.TestCheckResourceAttr(addr, "show_in_portal", "false"),
					resource.TestCheckResourceAttr(addr, "token_endpoint_auth_method", "client_secret_basic"),
					resource.TestCheckResourceAttr(addr, "grant_types.#", "2"),
					resource.TestCheckResourceAttr(addr, "claims.%", "0"),
					jumpcloud(func(a client.Application, sso fakeSSO) error {
						if a.Name != "oidc" || a.DisplayLabel != "grafana-qa" || !sso.Hidden || sso.OIDC["consent"] != "trusted" {
							return fmt.Errorf("JumpCloud has %+v %+v", a, sso)
						}
						return nil
					}),
					resource.TestCheckResourceAttrWith(addr, "client_id", func(v string) error { return expect(v, "client-"+id) }),
					resource.TestCheckResourceAttrWith(addr, "client_secret", func(v string) error { return expect(v, "secret-"+id) }),
				),
			},
			{
				// Label and settings change together.
				Config: config("grafana-production", "client_secret_post", `claims = { groups = "groups" }`),
				Check: resource.ComposeAggregateTestCheckFunc(
					sameID,
					renames(1),
					resource.TestCheckResourceAttr(addr, "redirect_uris.#", "2"),
					resource.TestCheckResourceAttr(addr, "claims.groups", "groups"),
					// The secret only comes back on create, so it must survive updates in state.
					resource.TestCheckResourceAttrWith(addr, "client_secret", func(v string) error { return expect(v, "secret-"+id) }),
					jumpcloud(func(a client.Application, sso fakeSSO) error {
						want := []any{map[string]any{"name": "groups", "value": "groups"}}
						switch {
						case a.DisplayLabel != "grafana-production", sso.Hidden:
							return fmt.Errorf("JumpCloud has %+v %+v", a, sso)
						case !reflect.DeepEqual(sso.OIDC["dynamicClaims"], want):
							return fmt.Errorf("claims = %v", sso.OIDC["dynamicClaims"])
						case sso.OIDC["accessTokenLifespan"] != "1h":
							return errors.New("update dropped a setting the provider does not manage")
						}
						return nil
					}),
				),
			},
			{
				// Settings only: removing claims clears them, without a rename.
				Config: config("grafana-production", "client_secret_post", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					sameID,
					renames(1),
					resource.TestCheckResourceAttr(addr, "claims.%", "0"),
					jumpcloud(func(_ client.Application, sso fakeSSO) error {
						if !reflect.DeepEqual(sso.OIDC["dynamicClaims"], []any{}) {
							return fmt.Errorf("claims = %#v, want an empty list", sso.OIDC["dynamicClaims"])
						}
						return nil
					}),
				),
			},
			{
				// Label only.
				Config: config("grafana", "client_secret_post", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					sameID,
					renames(2),
					jumpcloud(func(a client.Application, sso fakeSSO) error {
						if a.DisplayLabel != "grafana" || sso.Hidden {
							return fmt.Errorf("JumpCloud has %+v %+v", a, sso)
						}
						return nil
					}),
				),
			},
			{
				ResourceName:            addr,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"client_secret"},
			},
			{
				// Switching to a public client replaces the app; public clients have no secret.
				Config: config("grafana", "none", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					newID,
					resource.TestCheckResourceAttr(addr, "client_secret", ""),
					resource.TestCheckResourceAttrWith(addr, "client_id", func(v string) error { return expect(v, "client-"+id) }),
				),
			},
			{
				// Switching back replaces it again to get a secret.
				Config: config("grafana", "client_secret_basic", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					newID,
					resource.TestCheckResourceAttrWith(addr, "client_secret", func(v string) error { return expect(v, "secret-"+id) }),
				),
			},
			{
				// An application deleted outside Terraform is created again.
				PreConfig: func() {
					f.mu.Lock()
					delete(f.apps, id)
					f.mu.Unlock()
				},
				Config: config("grafana", "client_secret_basic", ""),
				Check:  newID,
			},
		},
	})
}

func TestOIDCApplicationImportRejectsOtherApps(t *testing.T) {
	f := newFakeJumpCloud(t)
	saml := f.addApp("AWS", "saml2")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{{
			Config: f.providerConfig() + `
resource "jumpcloud_oidc_application" "test" {
  display_label = "AWS"
  redirect_uris = ["https://example.com/callback"]
  login_url     = "https://example.com"
}`,
			ResourceName:  "jumpcloud_oidc_application.test",
			ImportState:   true,
			ImportStateId: saml,
			ExpectError:   regexp.MustCompile(`no OIDC settings`),
		}},
	})
}

// Applications created in the console have an empty displayLabel, so importing one used
// to store an empty label and plan a change that was not real.
func TestOIDCApplicationImportConsoleCreatedApp(t *testing.T) {
	f := newFakeJumpCloud(t)
	id := f.addConsoleOIDCApp()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{{
			Config: f.providerConfig() + `
resource "jumpcloud_oidc_application" "test" {
  display_label              = "OpenID Connect"
  show_in_portal             = true
  redirect_uris              = ["https://example.com"]
  login_url                  = "https://example.com"
  grant_types                = ["authorization_code"]
  token_endpoint_auth_method = "client_secret_post"
}`,
			ResourceName:  "jumpcloud_oidc_application.test",
			ImportState:   true,
			ImportStateId: id,
			ImportStateCheck: func(states []*terraform.InstanceState) error {
				return expect(states[0].Attributes["display_label"], "OpenID Connect")
			},
		}},
	})
}

// JumpCloud sets a refresh token lifespan even when refresh_token is not granted, and
// then rejects it on write. Updates used to resend it and fail with 422.
func TestOIDCApplicationUpdateWithoutRefreshTokenGrant(t *testing.T) {
	f := newFakeJumpCloud(t)
	const addr = "jumpcloud_oidc_application.test"
	config := func(label, loginURL string) string {
		return f.providerConfig() + fmt.Sprintf(`
resource "jumpcloud_oidc_application" "test" {
  display_label = %q
  redirect_uris = ["https://example.com"]
  login_url     = %q
  grant_types   = ["authorization_code"]
}`, label, loginURL)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{Config: config("terraform-test", "https://example.com")},
			{
				// Label only: the SSO settings must be left alone entirely.
				Config: config("Terraform Test", "https://example.com"),
				Check:  resource.TestCheckResourceAttr(addr, "display_label", "Terraform Test"),
			},
			{
				// Settings change: the lifespan JumpCloud set must not be sent back.
				Config: config("Terraform Test", "https://app.example.com"),
				Check:  resource.TestCheckResourceAttr(addr, "login_url", "https://app.example.com"),
			},
		},
	})
}

func TestApplicationAssociationResource(t *testing.T) {
	f := newFakeJumpCloud(t)
	app := f.addApp("grafana", "oidc")
	group := f.addGroup("devs", "")
	const addr = "jumpcloud_application_association.test"
	config := f.providerConfig() + fmt.Sprintf(`
resource "jumpcloud_application_association" "test" {
  application_id = %q
  type           = "user_group"
  target_id      = %q
}`, app, group)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			if f.associated(app, "user_group", group) {
				return errors.New("association left after destroy")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "id", app+"/user_group/"+group),
					func(*terraform.State) error {
						if !f.associated(app, "user_group", group) {
							return errors.New("JumpCloud has no association")
						}
						return nil
					},
				),
			},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true},
			{
				// An association removed outside Terraform is added again.
				PreConfig: func() {
					f.mu.Lock()
					f.assocs[app+"/user_group"] = nil
					f.mu.Unlock()
				},
				Config: config,
				Check: func(*terraform.State) error {
					if !f.associated(app, "user_group", group) {
						return errors.New("association was not restored")
					}
					return nil
				},
			},
			{ResourceName: addr, ImportState: true, ImportStateId: "only/two", ExpectError: regexp.MustCompile(`Invalid import ID`)},
		},
	})
}

func TestApplicationAssociationForUser(t *testing.T) {
	f := newFakeJumpCloud(t)
	app := f.addApp("grafana", "oidc")
	f.mu.Lock()
	f.assocs[app+"/user"] = []string{"u1"} // already associated before Terraform runs
	f.mu.Unlock()

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			if f.associated(app, "user", "u1") {
				return errors.New("association left after destroy")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				// An existing association (409) is adopted.
				Config: f.providerConfig() + fmt.Sprintf(`
resource "jumpcloud_application_association" "test" {
  application_id = %q
  type           = "user"
  target_id      = "u1"
}`, app),
				Check: resource.TestCheckResourceAttr("jumpcloud_application_association.test", "id", app+"/user/u1"),
			},
			{
				Config: f.providerConfig() + `
resource "jumpcloud_application_association" "test" {
  application_id = "missing"
  type           = "user"
  target_id      = "u1"
}`,
				ExpectError: regexp.MustCompile(`Application missing or user u1 does not exist`),
			},
		},
	})
}

func TestApplicationDataSource(t *testing.T) {
	f := newFakeJumpCloud(t)
	grafana := f.addApp("Grafana", "oidc")
	f.addApp("dup", "saml2")
	f.addApp("dup", "saml2")

	lookup := func(label string) string {
		return f.providerConfig() + fmt.Sprintf(`data "jumpcloud_application" "test" { display_label = %q }`, label)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: lookup("Grafana"),
				Check:  resource.TestCheckResourceAttr("data.jumpcloud_application.test", "id", grafana),
			},
			{Config: lookup("dup"), ExpectError: regexp.MustCompile(`found 2`)},
			{Config: lookup("missing"), ExpectError: regexp.MustCompile(`found 0`)},
		},
	})
}

func expect(got, want string) error {
	if got != want {
		return fmt.Errorf("got %q, want %q", got, want)
	}
	return nil
}
