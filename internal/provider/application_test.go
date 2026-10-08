package provider

import (
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestOIDCApplicationResource(t *testing.T) {
	f := newFakeJumpCloud(t)
	const addr = "jumpcloud_oidc_application.test"
	updated := f.providerConfig() + `
resource "jumpcloud_oidc_application" "test" {
  display_label              = "grafana-production"
  show_in_portal             = true
  redirect_uris              = ["https://grafana.example.com/login/generic_oauth", "https://grafana.example.com/alt"]
  login_url                  = "https://grafana.example.com"
  token_endpoint_auth_method = "client_secret_post"
  claims                     = { groups = "groups" }
}`
	var firstID string

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
					resource.TestCheckResourceAttr(addr, "show_in_portal", "false"),
					resource.TestCheckResourceAttr(addr, "token_endpoint_auth_method", "client_secret_basic"),
					resource.TestCheckResourceAttr(addr, "grant_types.#", "2"),
					resource.TestCheckResourceAttr(addr, "claims.%", "0"),
					resource.TestCheckResourceAttrWith(addr, "id", func(id string) error {
						firstID = id
						a, sso, _ := f.app(id)
						if a.Name != "oidc" || a.DisplayLabel != "grafana-qa" || !sso.Hidden || sso.OIDC["consent"] != "trusted" {
							return fmt.Errorf("JumpCloud has %+v %+v", a, sso)
						}
						return nil
					}),
					resource.TestCheckResourceAttrWith(addr, "client_id", func(v string) error { return expect(v, "client-"+firstID) }),
					resource.TestCheckResourceAttrWith(addr, "client_secret", func(v string) error { return expect(v, "secret-"+firstID) }),
				),
			},
			{
				Config: updated,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "display_label", "grafana-production"),
					resource.TestCheckResourceAttr(addr, "redirect_uris.#", "2"),
					resource.TestCheckResourceAttr(addr, "claims.groups", "groups"),
					resource.TestCheckResourceAttrWith(addr, "id", func(id string) error { return expect(id, firstID) }),
					resource.TestCheckResourceAttrWith(addr, "client_id", func(v string) error { return expect(v, "client-"+firstID) }),
					// The secret only comes back on create, so it must survive updates in state.
					resource.TestCheckResourceAttrWith(addr, "client_secret", func(v string) error { return expect(v, "secret-"+firstID) }),
					func(*terraform.State) error {
						a, sso, _ := f.app(firstID)
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
					},
				),
			},
			{
				ResourceName:            addr,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"client_secret"},
			},
			{
				// An application deleted outside Terraform is created again.
				PreConfig: func() {
					f.mu.Lock()
					delete(f.apps, firstID)
					f.mu.Unlock()
				},
				Config: updated,
				Check: resource.TestCheckResourceAttrWith(addr, "id", func(id string) error {
					if id == firstID {
						return errors.New("expected a new application")
					}
					return nil
				}),
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
