package provider

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestSAMLApplicationResource(t *testing.T) {
	f := newFakeJumpCloud(t)
	const addr = "jumpcloud_saml_application.test"
	const oneACS, twoACS = `["https://n8n.example.com/acs"]`, `["https://n8n.example.com/acs", "https://n8n.example.com/acs2"]`
	config := func(label, acs, extra string) string {
		return f.providerConfig() + fmt.Sprintf(`
resource "jumpcloud_saml_application" "test" {
  display_label = %q
  sp_entity_id  = "https://n8n.example.com/metadata"
  acs_urls      = %s
  %s
}`, label, acs, extra)
	}
	updated := `
  show_in_portal      = true
  name_id             = "username"
  sign_response       = true
  groups_attribute    = "groups"
  constant_attributes = { Role = "reader" }
  user_attributes     = { FirstName = "firstname" }`
	var id, cert string
	jumpcloud := func(check func(map[string]any) error) resource.TestCheckFunc {
		return func(*terraform.State) error { return check(f.samlConfig(id)) }
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
				Config: config("n8n Production", oneACS, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith(addr, "id", func(v string) error { id = v; return nil }),
					resource.TestCheckResourceAttr(addr, "template", "saml2"),
					resource.TestCheckResourceAttr(addr, "sso_url", "https://sso.jumpcloud.com/saml2/n8n-production"),
					// The custom template's IdP entity ID is empty, which service providers reject.
					resource.TestCheckResourceAttr(addr, "idp_entity_id", "n8n-production"),
					resource.TestCheckResourceAttr(addr, "show_in_portal", "false"),
					resource.TestCheckResourceAttr(addr, "name_id", "email"),
					resource.TestCheckResourceAttr(addr, "groups_attribute", ""),
					resource.TestCheckResourceAttr(addr, "acs_urls.#", "1"),
					resource.TestCheckResourceAttrWith(addr, "idp_certificate", func(v string) error {
						cert = v
						if !strings.HasPrefix(v, "-----BEGIN CERTIFICATE-----") {
							return fmt.Errorf("idp_certificate = %q, want PEM", v)
						}
						return nil
					}),
					jumpcloud(func(c map[string]any) error {
						if c["acsUrl"] != "https://n8n.example.com/acs" {
							return fmt.Errorf("acsUrl = %v", c["acsUrl"])
						}
						return nil
					}),
				),
			},
			{
				Config: config("n8n", twoACS, updated),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith(addr, "id", func(v string) error { return expect(v, id) }),
					// The SSO URL is what the service provider trusts, so a new label keeps it.
					resource.TestCheckResourceAttr(addr, "sso_url", "https://sso.jumpcloud.com/saml2/n8n-production"),
					resource.TestCheckResourceAttr(addr, "idp_entity_id", "n8n-production"),
					// An update must not regenerate the certificate the service provider has.
					resource.TestCheckResourceAttrWith(addr, "idp_certificate", func(v string) error { return expect(v, cert) }),
					resource.TestCheckResourceAttr(addr, "acs_urls.1", "https://n8n.example.com/acs2"),
					resource.TestCheckResourceAttr(addr, "constant_attributes.Role", "reader"),
					jumpcloud(func(c map[string]any) error {
						switch {
						case !strings.HasPrefix(fmt.Sprint(c["acsUrl"]), `[{"index":"0","isDefault":"true","url":"https://n8n.example.com/acs"}`):
							return fmt.Errorf("acsUrl = %v", c["acsUrl"])
						case c["includeGroups"] != true, c["groupsAttributeName"] != "groups", c["subjectField"] != "username":
							return fmt.Errorf("config = %v", c)
						case fmt.Sprint(c["databaseAttributes"]) != "[map[name:FirstName value:firstname]]":
							return fmt.Errorf("databaseAttributes = %v", c["databaseAttributes"])
						}
						return nil
					}),
				),
			},
			{
				// Settings left out keep their values.
				Config: config("n8n", twoACS, "show_in_portal = true"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "name_id", "username"),
					jumpcloud(func(c map[string]any) error {
						if c["includeGroups"] != true || c["subjectField"] != "username" || c["signResponse"] != true {
							return fmt.Errorf("config = %v", c)
						}
						return nil
					}),
				),
			},
			{
				Config: config("n8n", twoACS, `
  show_in_portal      = true
  groups_attribute    = ""
  constant_attributes = {}`),
				Check: jumpcloud(func(c map[string]any) error {
					if c["includeGroups"] != false || fmt.Sprint(c["constantAttributes"]) != "[]" {
						return fmt.Errorf("config = %v", c)
					}
					return nil
				}),
			},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true},
			{
				// An application deleted outside Terraform is created again.
				PreConfig: func() {
					f.mu.Lock()
					delete(f.apps, id)
					delete(f.saml, id)
					f.mu.Unlock()
				},
				Config: config("n8n", twoACS, "show_in_portal = true"),
				Check: resource.TestCheckResourceAttrWith(addr, "id", func(v string) error {
					if v == id {
						return errors.New("expected a new application")
					}
					return nil
				}),
			},
		},
	})
}

func TestSAMLApplicationCatalogTemplate(t *testing.T) {
	f := newFakeJumpCloud(t)
	const addr = "jumpcloud_saml_application.aws"
	config := func(template, extra string) string {
		return f.providerConfig() + fmt.Sprintf(`
resource "jumpcloud_saml_application" "aws" {
  display_label = "AWS IAM Identity Center"
  template      = %q
  sp_entity_id  = "https://us-east-1.signin.aws.amazon.com/platform/saml/d-123"
  acs_urls      = ["https://us-east-1.signin.aws.amazon.com/platform/saml/acs/abc"]
  idp_init_url  = "https://d-123.awsapps.com/start"
  %s
}`, template, extra)
	}
	var id string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{Config: config("oidc", ""), ExpectError: regexp.MustCompile(`not a SAML template`)},
			{Config: config("nope", ""), ExpectError: regexp.MustCompile(`no application template named "nope"`)},
			{Config: config("aws-sso", `name_id = "email"`), ExpectError: regexp.MustCompile(`template "aws-sso" has no name_id setting`)},
			{
				Config: config("aws-sso", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith(addr, "id", func(v string) error { id = v; return nil }),
					resource.TestCheckResourceAttr(addr, "idp_entity_id", "JumpCloud"),
					resource.TestCheckNoResourceAttr(addr, "name_id"),
					resource.TestCheckNoResourceAttr(addr, "groups_attribute"),
					func(*terraform.State) error {
						// Catalog apps store a single plain URL.
						if c := f.samlConfig(id); c["acsUrl"] != "https://us-east-1.signin.aws.amazon.com/platform/saml/acs/abc" {
							return fmt.Errorf("acsUrl = %v", c["acsUrl"])
						}
						return nil
					},
				),
			},
			{Config: config("aws-sso", `groups_attribute = "groups"`), ExpectError: regexp.MustCompile(`has no groups_attribute setting`)},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true},
		},
	})
}

func TestSAMLApplicationImportRejectsOtherApps(t *testing.T) {
	f := newFakeJumpCloud(t)
	oidc := f.addConsoleOIDCApp()
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{{
			Config: f.providerConfig() + `
resource "jumpcloud_saml_application" "test" {
  display_label = "x"
  sp_entity_id  = "x"
  acs_urls      = ["https://x/acs"]
}`,
			ResourceName:  "jumpcloud_saml_application.test",
			ImportState:   true,
			ImportStateId: oidc,
			ExpectError:   regexp.MustCompile(`not a SAML application`),
		}},
	})
}

func TestSAMLApplicationTemplateSwitchCheckedAtPlan(t *testing.T) {
	f := newFakeJumpCloud(t)
	const addr = "jumpcloud_saml_application.test"
	config := func(template string) string {
		return f.providerConfig() + fmt.Sprintf(`
resource "jumpcloud_saml_application" "test" {
  display_label = "app"
  template      = %q
  sp_entity_id  = "https://sp.example.com/m"
  acs_urls      = ["https://sp.example.com/acs"]
  name_id       = "email"
}`, template)
	}
	var id string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{Config: config("saml2"), Check: resource.TestCheckResourceAttrWith(addr, "id", func(v string) error { id = v; return nil })},
			{
				// Replacing the app with a template that lacks name_id fails before the old app is destroyed.
				Config:      config("aws-sso"),
				ExpectError: regexp.MustCompile(`template "aws-sso" has no name_id setting`),
			},
			{
				Config:   config("saml2"),
				PlanOnly: true,
				Check: func(*terraform.State) error {
					if _, _, ok := f.app(id); !ok {
						return fmt.Errorf("application %s was deleted", id)
					}
					return nil
				},
			},
		},
	})
}

func TestSAMLApplicationUnsupportedSettingCheckedAtPlan(t *testing.T) {
	f := newFakeJumpCloud(t)
	config := func(extra string) string {
		return f.providerConfig() + fmt.Sprintf(`
resource "jumpcloud_saml_application" "aws" {
  display_label = "AWS"
  template      = "aws-sso"
  sp_entity_id  = "https://sp.example.com/m"
  acs_urls      = ["https://sp.example.com/acs"]
  %s
}`, extra)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{Config: config("")},
			{
				Config:      config(`groups_attribute = "groups"`),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`template "aws-sso" has no groups_attribute setting`),
			},
		},
	})
}
