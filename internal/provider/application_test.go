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
					resource.TestCheckResourceAttr(addr, "static_claims.%", "0"),
					resource.TestCheckResourceAttr(addr, "access_token_format", "opaque"),
					resource.TestCheckResourceAttr(addr, "audiences.#", "0"),
					resource.TestCheckResourceAttr(addr, "subject_field", "userId"),
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
	config := func(label, loginURL, lifetime string) string {
		return f.providerConfig() + fmt.Sprintf(`
resource "jumpcloud_oidc_application" "test" {
  display_label = %q
  redirect_uris = ["https://example.com"]
  login_url     = %q
  grant_types   = ["authorization_code"]
  %s
}`, label, loginURL, lifetime)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{Config: config("terraform-test", "https://example.com", "")},
			{
				// Label only: the SSO settings must be left alone entirely.
				Config: config("Terraform Test", "https://example.com", ""),
				Check:  resource.TestCheckResourceAttr(addr, "display_label", "Terraform Test"),
			},
			{
				// Settings change: the lifespan JumpCloud set must not be sent back.
				Config: config("Terraform Test", "https://app.example.com", ""),
				Check:  resource.TestCheckResourceAttr(addr, "login_url", "https://app.example.com"),
			},
			{
				Config: config("Terraform Test", "https://app.example.com", "access_token_lifetime_seconds = 3661"),
				Check:  resource.TestCheckResourceAttr(addr, "access_token_lifetime_seconds", "3661"),
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

func TestOIDCApplicationStaticClaims(t *testing.T) {
	f := newFakeJumpCloud(t)
	const addr = "jumpcloud_oidc_application.test"
	config := func(claims, static string) string {
		return f.providerConfig() + fmt.Sprintf(`
resource "jumpcloud_oidc_application" "test" {
  display_label = "app"
  redirect_uris = ["https://app.example.com/callback"]
  login_url     = "https://app.example.com"
  claims        = { %s }
  %s
}`, claims, static)
	}
	var id string
	stored := func(want []any) resource.TestCheckFunc {
		return func(*terraform.State) error {
			_, sso, _ := f.app(id)
			if !reflect.DeepEqual(sso.OIDC["staticClaims"], want) {
				return fmt.Errorf("staticClaims = %#v, want %#v", sso.OIDC["staticClaims"], want)
			}
			return nil
		}
	}
	verified := []any{map[string]any{"name": "email_verified", "value": "true"}}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config(`email = "email"`, `static_claims = { email_verified = "true" }`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith(addr, "id", func(v string) error { id = v; return nil }),
					resource.TestCheckResourceAttr(addr, "static_claims.%", "1"),
					resource.TestCheckResourceAttr(addr, "static_claims.email_verified", "true"),
					resource.TestCheckResourceAttr(addr, "claims.email", "email"),
					stored(verified),
				),
			},
			{
				// Not configured: kept as is, also when an update writes the other settings.
				Config: config(`email = "email", name = "fullname"`, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "static_claims.email_verified", "true"),
					resource.TestCheckResourceAttr(addr, "claims.%", "2"),
					stored(verified),
				),
			},
			{
				// A claim added in the admin console shows up in state and is kept.
				PreConfig: func() {
					f.setOIDC(id, "staticClaims", []any{
						map[string]any{"name": "email_verified", "value": "true"},
						map[string]any{"name": "tenant", "value": "loadsmart"},
					})
				},
				Config: config(`email = "email"`, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "static_claims.%", "2"),
					resource.TestCheckResourceAttr(addr, "static_claims.tenant", "loadsmart"),
				),
			},
			{
				Config: config(`email = "email"`, "static_claims = {}"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "static_claims.%", "0"),
					stored([]any{}),
				),
			},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"client_secret"}},
		},
	})
}

func TestOIDCApplicationAccessTokenFormat(t *testing.T) {
	f := newFakeJumpCloud(t)
	const addr = "jumpcloud_oidc_application.test"
	config := func(login, extra string) string {
		return f.providerConfig() + fmt.Sprintf(`
resource "jumpcloud_oidc_application" "test" {
  display_label = "app"
  redirect_uris = ["https://app.example.com/callback"]
  login_url     = %q
  %s
}`, login, extra)
	}
	var id string
	stored := func(format string, audience []any) resource.TestCheckFunc {
		return func(*terraform.State) error {
			_, sso, _ := f.app(id)
			if sso.OIDC["accessTokenStrategy"] != format || !reflect.DeepEqual(sso.OIDC["audience"], audience) {
				return fmt.Errorf("JumpCloud has %v %#v, want %v %#v", sso.OIDC["accessTokenStrategy"], sso.OIDC["audience"], format, audience)
			}
			return nil
		}
	}
	api := []any{"https://api.example.com"}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config("https://app.example.com", `
  access_token_format = "jwt"
  audiences           = ["https://api.example.com"]`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith(addr, "id", func(v string) error { id = v; return nil }),
					resource.TestCheckResourceAttr(addr, "access_token_format", "jwt"),
					resource.TestCheckResourceAttr(addr, "audiences.#", "1"),
					stored("jwt", api),
				),
			},
			{
				// Not configured: kept as is, also when an update writes the other settings.
				Config: config("https://app.example.com/login", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "access_token_format", "jwt"),
					resource.TestCheckResourceAttr(addr, "audiences.0", "https://api.example.com"),
					stored("jwt", api),
				),
			},
			{
				// Changed in the admin console: shows up in state and is kept.
				PreConfig: func() { f.setOIDC(id, "accessTokenStrategy", "opaque") },
				Config:    config("https://app.example.com/login", ""),
				Check:     resource.TestCheckResourceAttr(addr, "access_token_format", "opaque"),
			},
			{
				Config: config("https://app.example.com/login", `
  access_token_format = "jwt"
  audiences           = []`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "audiences.#", "0"),
					stored("jwt", []any{}),
				),
			},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"client_secret"}},
		},
	})
}

func TestOIDCApplicationSubjectField(t *testing.T) {
	f := newFakeJumpCloud(t)
	const addr = "jumpcloud_oidc_application.test"
	config := func(login, extra string) string {
		return f.providerConfig() + fmt.Sprintf(`
resource "jumpcloud_oidc_application" "test" {
  display_label = "app"
  redirect_uris = ["https://app.example.com/callback"]
  login_url     = %q
  %s
}`, login, extra)
	}
	var id string
	stored := func(want string) resource.TestCheckFunc {
		return func(*terraform.State) error {
			if _, sso, _ := f.app(id); sso.OIDC["subjectField"] != want {
				return fmt.Errorf("JumpCloud has subjectField %v, want %v", sso.OIDC["subjectField"], want)
			}
			return nil
		}
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config("https://app.example.com", `subject_field = "email"`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith(addr, "id", func(v string) error { id = v; return nil }),
					resource.TestCheckResourceAttr(addr, "subject_field", "email"),
					stored("email"),
				),
			},
			{
				// Not configured: kept as is, also when an update writes the other settings.
				Config: config("https://app.example.com/login", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "subject_field", "email"),
					stored("email"),
				),
			},
			{
				// Changed in the admin console: shows up in state and is kept.
				PreConfig: func() { f.setOIDC(id, "subjectField", "userId") },
				Config:    config("https://app.example.com/login", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "subject_field", "userId"),
					stored("userId"),
				),
			},
			{
				Config: config("https://app.example.com/login", `subject_field = "email"`),
				Check:  stored("email"),
			},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"client_secret"}},
		},
	})
}

func TestOIDCApplicationTokenLifetimes(t *testing.T) {
	f := newFakeJumpCloud(t)
	const addr = "jumpcloud_oidc_application.test"
	config := func(login, extra string) string {
		return f.providerConfig() + fmt.Sprintf(`
resource "jumpcloud_oidc_application" "test" {
  display_label = "app"
  redirect_uris = ["https://app.example.com/callback"]
  login_url     = %q
  %s
}`, login, extra)
	}
	var id string
	stored := func(access, identity, refresh string) resource.TestCheckFunc {
		return func(*terraform.State) error {
			_, sso, _ := f.app(id)
			for key, want := range map[string]string{
				"accessTokenLifespan":  access,
				"idTokenLifespan":      identity,
				"refreshTokenLifespan": refresh,
			} {
				if sso.OIDC[key] != want {
					return fmt.Errorf("JumpCloud %s = %v, want %s", key, sso.OIDC[key], want)
				}
			}
			return nil
		}
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config("https://app.example.com", `
  access_token_lifetime_seconds  = 3661
  id_token_lifetime_seconds      = 3540
  refresh_token_lifetime_seconds = 2592061`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith(addr, "id", func(v string) error { id = v; return nil }),
					resource.TestCheckResourceAttr(addr, "access_token_lifetime_seconds", "3661"),
					resource.TestCheckResourceAttr(addr, "id_token_lifetime_seconds", "3540"),
					resource.TestCheckResourceAttr(addr, "refresh_token_lifetime_seconds", "2592061"),
					stored("3661s", "59m", "2592061s"),
				),
			},
			{
				Config: config("https://app.example.com/login", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "access_token_lifetime_seconds", "3661"),
					stored("3661s", "59m", "2592061s"),
				),
			},
			{
				// Earlier provider versions wrote Go's multi-unit form, which the admin console shows as empty.
				PreConfig: func() {
					f.setOIDC(id, "accessTokenLifespan", "1h1m1s")
					f.setOIDC(id, "idTokenLifespan", "59m0s")
				},
				Config: config("https://app.example.com/login", `
  access_token_lifetime_seconds  = 3661
  refresh_token_lifetime_seconds = 2592061`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "access_token_lifetime_seconds", "3661"),
					// Not configured, so JumpCloud's value is kept as is and is unknown to Terraform.
					resource.TestCheckNoResourceAttr(addr, "id_token_lifetime_seconds"),
					stored("3661s", "59m0s", "2592061s"),
				),
			},
			{
				PreConfig: func() { f.setOIDC(id, "accessTokenLifespan", "45m") },
				Config:    config("https://app.example.com/login", ""),
				Check:     resource.TestCheckResourceAttr(addr, "access_token_lifetime_seconds", "2700"),
			},
			{
				Config: config("https://app.example.com/login", `
  access_token_lifetime_seconds  = 3600
  id_token_lifetime_seconds      = 3600
  refresh_token_lifetime_seconds = 2592000`),
				Check: stored("1h", "1h", "720h"),
			},
			{
				Config: config("https://app.example.com/login", `
  refresh_token_lifetime_seconds = 43200`),
				Check: stored("1h", "1h", "12h"),
			},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"client_secret"}},
		},
	})
}

func TestOIDCApplicationInvalidTokenLifetimes(t *testing.T) {
	for _, tc := range []struct {
		name, config, errorText string
	}{
		{"access too short", "access_token_lifetime_seconds = 599", "Invalid token lifetime"},
		{"access too long", "access_token_lifetime_seconds = 86401", "Invalid token lifetime"},
		{"id too long", "id_token_lifetime_seconds = 3601", "Invalid token lifetime"},
		{"refresh too long", "refresh_token_lifetime_seconds = 7776001", "Invalid token lifetime"},
		{"refresh grant missing", "grant_types = [\"authorization_code\"]\nrefresh_token_lifetime_seconds = 600", "Invalid refresh token lifetime"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeJumpCloud(t)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: testProviderFactories,
				Steps: []resource.TestStep{{
					Config: f.providerConfig() + fmt.Sprintf(`
resource "jumpcloud_oidc_application" "test" {
  display_label = "app"
  redirect_uris = ["https://app.example.com/callback"]
  login_url     = "https://app.example.com"
  %s
}`, tc.config),
					ExpectError: regexp.MustCompile(tc.errorText),
				}},
			})
		})
	}
}
