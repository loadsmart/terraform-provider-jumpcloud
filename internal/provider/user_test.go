package provider

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/loadsmart/terraform-provider-jumpcloud/internal/client"
)

func TestUserGroupMembershipsResource(t *testing.T) {
	f := newFakeJumpCloud(t)
	f.addUser(client.User{ID: "u1", Email: "ana@loadsmart.com"})
	a, b := f.addGroup("a", ""), f.addGroup("b", "")
	outside := f.addGroup("outside", "")
	f.addMember(outside, "u1")

	const addr = "jumpcloud_user_group_memberships.test"
	config := func(groups ...string) string {
		return f.providerConfig() + fmt.Sprintf(`
resource "jumpcloud_user_group_memberships" "test" {
  user_id   = "u1"
  group_ids = ["%s"]
}`, strings.Join(groups, `", "`))
	}
	members := func(want map[string]bool) resource.TestCheckFunc {
		return func(*terraform.State) error {
			for group, member := range want {
				if f.isMember(group, "u1") != member {
					return fmt.Errorf("membership in %s: got %t, want %t", group, !member, member)
				}
			}
			return nil
		}
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		// Destroy removes only the managed membership.
		CheckDestroy: members(map[string]bool{a: false, b: false, outside: true}),
		Steps: []resource.TestStep{
			{
				Config: config(a, b),
				Check: resource.ComposeAggregateTestCheckFunc(
					members(map[string]bool{a: true, b: true, outside: true}),
					resource.TestCheckResourceAttr(addr, "id", "u1"),
					resource.TestCheckResourceAttr(addr, "group_ids.#", "2"),
				),
			},
			{
				Config: config(b),
				Check: resource.ComposeAggregateTestCheckFunc(
					members(map[string]bool{a: false, b: true, outside: true}),
					resource.TestCheckResourceAttr(addr, "group_ids.#", "1"),
				),
			},
			{
				// A membership removed outside Terraform is added again.
				PreConfig: func() {
					f.mu.Lock()
					f.members[b] = nil
					f.mu.Unlock()
				},
				Config: config(b),
				Check:  members(map[string]bool{b: true}),
			},
			{
				// Import adopts every direct membership of the user.
				ResourceName:  addr,
				ImportState:   true,
				ImportStateId: "u1",
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					if n := states[0].Attributes["group_ids.#"]; n != "2" {
						return fmt.Errorf("imported %s groups, want 2", n)
					}
					return nil
				},
			},
		},
	})
}

func TestUserDataSources(t *testing.T) {
	f := newFakeJumpCloud(t)
	f.addUser(client.User{
		ID: "u1", Email: "ana@loadsmart.com", Username: "ana", State: "ACTIVATED",
		Department: "Engineering", JobTitle: "SRE", Attributes: []client.UserAttribute{{Name: "team", Value: "platform"}},
	})
	f.addUser(client.User{ID: "u2", Email: "bo@loadsmart.com", Department: "Sales"})

	lookup := func(email string) string {
		return f.providerConfig() + fmt.Sprintf(`data "jumpcloud_user" "test" { email = %q }`, email)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: lookup("ana@loadsmart.com") + `
data "jumpcloud_users" "engineering" {
  filter = { department = "Engineering" }
}

data "jumpcloud_users" "all" {}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.jumpcloud_user.test", "id", "u1"),
					resource.TestCheckResourceAttr("data.jumpcloud_user.test", "username", "ana"),
					resource.TestCheckResourceAttr("data.jumpcloud_user.test", "state", "ACTIVATED"),
					resource.TestCheckResourceAttr("data.jumpcloud_user.test", "job_title", "SRE"),
					resource.TestCheckResourceAttr("data.jumpcloud_user.test", "attributes.team", "platform"),
					resource.TestCheckResourceAttr("data.jumpcloud_users.engineering", "users.#", "1"),
					resource.TestCheckResourceAttr("data.jumpcloud_users.engineering", "users.0.email", "ana@loadsmart.com"),
					resource.TestCheckResourceAttr("data.jumpcloud_users.all", "users.#", "2"),
				),
			},
			{Config: lookup("nobody@loadsmart.com"), ExpectError: regexp.MustCompile(`found 0`)},
		},
	})
}
