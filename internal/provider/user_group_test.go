package provider

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/loadsmart/terraform-provider-jumpcloud/internal/client"
)

func TestUserGroupResource(t *testing.T) {
	f := newFakeJumpCloud(t)
	const addr = "jumpcloud_user_group.test"
	renamed := f.providerConfig() + `resource "jumpcloud_user_group" "test" { name = "developers" }`
	var firstID string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			if n := f.groupCount(); n != 0 {
				return fmt.Errorf("%d user groups left after destroy", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: f.providerConfig() + `
resource "jumpcloud_user_group" "test" {
  name        = "devs"
  description = "Developers"
}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "name", "devs"),
					resource.TestCheckResourceAttr(addr, "description", "Developers"),
					resource.TestCheckResourceAttrWith(addr, "id", func(id string) error {
						firstID = id
						if g, ok := f.group(id); !ok || g.Name != "devs" || g.Description != "Developers" {
							return fmt.Errorf("JumpCloud has %+v", g)
						}
						return nil
					}),
				),
			},
			{
				Config: renamed,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "name", "developers"),
					resource.TestCheckResourceAttr(addr, "description", ""),
					resource.TestCheckResourceAttrWith(addr, "id", func(id string) error {
						if id != firstID {
							return fmt.Errorf("rename replaced the group: %s != %s", id, firstID)
						}
						return nil
					}),
				),
			},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true},
			{
				// A group deleted outside Terraform is created again.
				PreConfig: func() { f.deleteGroup(firstID) },
				Config:    renamed,
				Check: resource.TestCheckResourceAttrWith(addr, "id", func(id string) error {
					if id == firstID {
						return errors.New("expected a new group")
					}
					return nil
				}),
			},
		},
	})
}

func TestUserGroupDataSources(t *testing.T) {
	f := newFakeJumpCloud(t)
	devs := f.addGroup("devs", "Developers")
	f.editGroup(devs, func(g *fakeGroup) {
		g.Email = "devs@example.com"
		g.MembershipMethod = "DYNAMIC_AUTOMATED"
		g.MemberQuery = json.RawMessage(`{"filters":[]}`)
	})
	f.addGroup("dup", "")
	f.addGroup("dup", "")

	lookup := func(name string) string {
		return f.providerConfig() + fmt.Sprintf(`data "jumpcloud_user_group" "test" { name = %q }`, name)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: lookup("devs") + `
data "jumpcloud_user_groups" "all" {}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.jumpcloud_user_group.test", "id", devs),
					resource.TestCheckResourceAttr("data.jumpcloud_user_group.test", "description", "Developers"),
					resource.TestCheckResourceAttr("data.jumpcloud_user_group.test", "email", "devs@example.com"),
					resource.TestCheckResourceAttr("data.jumpcloud_user_group.test", "membership_method", "DYNAMIC_AUTOMATED"),
					resource.TestCheckResourceAttr("data.jumpcloud_user_group.test", "query", `{"filters":[]}`),
					resource.TestCheckResourceAttr("data.jumpcloud_user_groups.all", "groups.#", "3"),
					resource.TestCheckResourceAttr("data.jumpcloud_user_groups.all", "groups.0.name", "devs"),
					resource.TestCheckResourceAttr("data.jumpcloud_user_groups.all", "groups.0.email", "devs@example.com"),
				),
			},
			{Config: lookup("dup"), ExpectError: regexp.MustCompile(`found 2`)},
			{Config: lookup("missing"), ExpectError: regexp.MustCompile(`found 0`)},
		},
	})
}

func TestUserGroupMembershipRule(t *testing.T) {
	f := newFakeJumpCloud(t)
	for id, dept := range map[string]string{"u1": "Engineering", "u2": "Engineering", "u3": "Sales", "u4": "Sales"} {
		f.addUser(client.User{ID: id, Department: dept})
	}
	const addr = "jumpcloud_user_group.test"
	config := func(rule string) string {
		return f.providerConfig() + fmt.Sprintf(`
resource "jumpcloud_user_group" "test" {
  name = "eng"
  %s
}`, rule)
	}
	const engineering = `jsonencode({ filters = [{ field = "user.user_department", operation = "equals", value = "Engineering" }] })`
	dynamic := config(`
  membership_rule = {
    query            = ` + engineering + `
    include_user_ids = ["u3"]
    exclude_user_ids = ["u2"]
  }`)
	var id string
	members := func(want ...string) resource.TestCheckFunc {
		return func(*terraform.State) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			got := slices.Sorted(slices.Values(f.members[id]))
			if !slices.Equal(got, want) {
				return fmt.Errorf("members = %v, want %v", got, want)
			}
			return nil
		}
	}
	exemptions := func(want ...string) resource.TestCheckFunc {
		return func(*terraform.State) error {
			g, _ := f.group(id)
			var got []string
			for _, e := range g.MemberQueryExemptions {
				got = append(got, e["id"])
			}
			slices.Sort(got)
			if !slices.Equal(got, want) {
				return fmt.Errorf("exemptions = %v, want %v", got, want)
			}
			return nil
		}
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: dynamic,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith(addr, "id", func(v string) error { id = v; return nil }),
					resource.TestCheckResourceAttr(addr, "membership_method", "DYNAMIC_AUTOMATED"),
					resource.TestCheckResourceAttr(addr, "membership_rule.review_required", "false"),
					resource.TestCheckResourceAttr(addr, "rule_errors.#", "0"),
					members("u1", "u3"),
					exemptions("u2", "u3"),
				),
			},
			{
				// Updating a dynamic group needs the x-query-dsl header, which the fake requires.
				Config: config(`
  description = "Engineering"
  membership_rule = {
    query              = ` + engineering + `
    review_required    = true
    notify_suggestions = true
    include_user_ids   = ["u3"]
    exclude_user_ids   = ["u2"]
  }`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "membership_method", "DYNAMIC_REVIEW_REQUIRED"),
					resource.TestCheckResourceAttr(addr, "membership_rule.notify_suggestions", "true"),
					members("u1", "u3"),
				),
			},
			{
				Config: config(`
  membership_rule = {
    query            = ` + engineering + `
    include_user_ids = ["u1"]
    exclude_user_ids = ["u1"]
  }`),
				ExpectError: regexp.MustCompile(`User u1 is in both include_user_ids and exclude_user_ids`),
			},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true},
			{
				// Exemptions are authoritative: one added in the console is removed.
				PreConfig: func() {
					f.editGroup(id, func(g *fakeGroup) {
						g.MemberQueryExemptions = append(g.MemberQueryExemptions, map[string]string{"id": "u4", "type": "USER"})
					})
					f.addMember(id, "u4")
				},
				Config: dynamic,
				Check:  resource.ComposeAggregateTestCheckFunc(exemptions("u2", "u3"), members("u1", "u3")),
			},
			{
				// Removing the rule keeps the members as direct members.
				Config: config(""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "membership_method", "STATIC"),
					resource.TestCheckNoResourceAttr(addr, "membership_rule"),
					exemptions(),
					members("u1", "u3"),
					func(*terraform.State) error {
						if g, _ := f.group(id); string(g.MemberQuery) != "null" {
							return fmt.Errorf("memberQuery = %s, want null", g.MemberQuery)
						}
						return nil
					},
				),
			},
			{
				// Back to dynamic: non-matching users that are not exempt are dropped.
				Config: config(`membership_rule = { query = ` + engineering + ` }`),
				Check:  members("u1", "u2"),
			},
		},
	})
}

func TestUserGroupAttributes(t *testing.T) {
	f := newFakeJumpCloud(t)
	const addr = "jumpcloud_user_group.test"
	config := func(name, attrs string) string {
		return f.providerConfig() + fmt.Sprintf(`
resource "jumpcloud_user_group" "test" {
  name = %q
  %s
}`, name, attrs)
	}
	var id string
	attribute := func(key, want string) resource.TestCheckFunc {
		return func(*terraform.State) error {
			g, _ := f.group(id)
			if got := string(g.Attributes[key]); got != want {
				return fmt.Errorf("attributes.%s = %s, want %s", key, got, want)
			}
			return nil
		}
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config("ops", `
  email         = "ops@example.com"
  sudo          = { enabled = true, without_password = true }
  radius_reply  = [{ name = "Filter-Id", value = "ops" }]
  samba_enabled = true
  posix_groups  = [{ id = 5000, name = "ops" }]`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith(addr, "id", func(v string) error { id = v; return nil }),
					resource.TestCheckResourceAttr(addr, "email", "ops@example.com"),
					resource.TestCheckResourceAttr(addr, "ldap_groups.#", "1"),
					resource.TestCheckResourceAttr(addr, "ldap_groups.0", "ops"),
					attribute("sudo", `{"enabled":true,"withoutPassword":true}`),
					attribute("radius", `{"reply":[{"name":"Filter-Id","value":"ops"}]}`),
					attribute("sambaEnabled", `true`),
					attribute("posixGroups", `[{"id":5000,"name":"ops"}]`),
				),
			},
			{
				// Omitted attributes keep their values, including the LDAP group across a rename.
				Config: config("operations", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "email", "ops@example.com"),
					resource.TestCheckResourceAttr(addr, "ldap_groups.0", "ops"),
					resource.TestCheckResourceAttr(addr, "sudo.enabled", "true"),
					attribute("ldapGroups", `[{"name":"ops"}]`),
					attribute("posixGroups", `[{"id":5000,"name":"ops"}]`),
				),
			},
			{
				// Values set in the console are kept when not configured.
				PreConfig: func() {
					f.editGroup(id, func(g *fakeGroup) {
						g.Attributes["sudo"] = fakeJSON(map[string]bool{"enabled": false, "withoutPassword": true})
					})
				},
				Config: config("operations", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "sudo.without_password", "true"),
					attribute("sudo", `{"enabled":false,"withoutPassword":true}`),
				),
			},
			{
				Config: config("operations", `
  email         = ""
  sudo          = { enabled = false, without_password = false }
  radius_reply  = []
  samba_enabled = false
  ldap_groups   = []`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "email", ""),
					attribute("sudo", ""),
					attribute("radius", ""),
					attribute("sambaEnabled", ""),
					attribute("ldapGroups", `[]`),
					attribute("posixGroups", `[{"id":5000,"name":"ops"}]`),
				),
			},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true},
			{
				Config:      config("operations", `posix_groups = [{ id = 5001, name = "ops" }]`),
				ExpectError: regexp.MustCompile(`JumpCloud does not allow changing POSIX groups once set`),
			},
			{
				Config:      config("operations", `posix_groups = []`),
				ExpectError: regexp.MustCompile(`JumpCloud does not allow changing POSIX groups once set`),
			},
			{
				// The error's advice works: a tainted group is replaced with the new POSIX groups.
				Taint:  []string{addr},
				Config: config("operations", `posix_groups = [{ id = 5001, name = "ops" }]`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith(addr, "id", func(v string) error {
						if v == id {
							return fmt.Errorf("expected a new group, got the same ID %s", v)
						}
						id = v
						return nil
					}),
					attribute("posixGroups", `[{"id":5001,"name":"ops"}]`),
				),
			},
			{
				Config:      config("operations", `radius_reply = [{ name = "Filter-Id" }]`),
				ExpectError: regexp.MustCompile(`The argument "value" is required`),
			},
			{Config: config("operations", ""), PlanOnly: true},
		},
	})
}

func TestUserGroupUnknownMembershipRule(t *testing.T) {
	f := newFakeJumpCloud(t)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{{
			// The rule is unknown when the configuration is validated during plan.
			Config: f.providerConfig() + `
resource "terraform_data" "rule" {
  input = { query = jsonencode({ filters = [] }) }
}

resource "jumpcloud_user_group" "test" {
  name            = "eng"
  membership_rule = terraform_data.rule.output
}`,
			Check: resource.TestCheckResourceAttr("jumpcloud_user_group.test", "membership_method", "DYNAMIC_AUTOMATED"),
		}},
	})
}

func TestUserGroupCreateKeepsGroupWhenIncludeFails(t *testing.T) {
	f := newFakeJumpCloud(t)
	f.addUser(client.User{ID: "u1", Department: "Engineering"})
	const addr = "jumpcloud_user_group.test"
	config := func(include string) string {
		return f.providerConfig() + fmt.Sprintf(`
resource "jumpcloud_user_group" "test" {
  name = "eng"
  membership_rule = {
    query            = jsonencode({ filters = [{ field = "user.user_department", operation = "equals", value = "Engineering" }] })
    include_user_ids = [%s]
  }
}`, include)
	}
	var id string

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				// An unknown user only warns, so the group is not tainted; the plan still shows the include.
				Config:             config(`"ghost"`),
				ExpectNonEmptyPlan: true,
				Check:              resource.TestCheckResourceAttrWith(addr, "id", func(v string) error { id = v; return nil }),
			},
			{
				Config: config(""),
				Check:  resource.TestCheckResourceAttrWith(addr, "id", func(v string) error { return expect(v, id) }),
			},
		},
	})
}
