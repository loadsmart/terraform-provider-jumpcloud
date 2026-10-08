package provider

import (
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/loadsmart/terraform-provider-jumpcloud/internal/client"
)

func TestUserGroupMembersResource(t *testing.T) {
	f := newFakeJumpCloud(t)
	for _, id := range []string{"u1", "u2", "u3"} {
		f.addUser(client.User{ID: id})
	}
	group := f.addGroup("devs", "")
	f.addMember(group, "u3")

	const addr = "jumpcloud_user_group_members.test"
	config := func(users ...string) string {
		return f.providerConfig() + fmt.Sprintf(`
resource "jumpcloud_user_group_members" "test" {
  group_id = %q
  user_ids = [%s]
}`, group, quoted(users))
	}
	members := func(want ...string) resource.TestCheckFunc {
		return func(*terraform.State) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			if got := slices.Sorted(slices.Values(f.members[group])); !slices.Equal(got, want) {
				return fmt.Errorf("members = %v, want %v", got, want)
			}
			return nil
		}
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		CheckDestroy:             members(),
		Steps: []resource.TestStep{
			{
				// Members not listed are removed.
				Config: config("u1", "u2"),
				Check: resource.ComposeAggregateTestCheckFunc(
					members("u1", "u2"),
					resource.TestCheckResourceAttr(addr, "id", group),
					resource.TestCheckResourceAttr(addr, "user_ids.#", "2"),
				),
			},
			{Config: config("u2"), Check: members("u2")},
			{
				// A member added in the console is removed.
				PreConfig: func() { f.addMember(group, "u3") },
				Config:    config("u2"),
				Check:     members("u2"),
			},
			{ResourceName: addr, ImportState: true, ImportStateId: group, ImportStateVerify: true},
			{
				// Partial failure: the unknown user is left out of state, so the next apply retries it.
				Config:      config("u1", "missing"),
				ExpectError: regexp.MustCompile(`User missing does not exist`),
			},
			{
				Config:             config("u1", "missing"),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{Config: config(), Check: resource.ComposeAggregateTestCheckFunc(members(), resource.TestCheckResourceAttr(addr, "user_ids.#", "0"))},
			{Config: config("u1"), Check: members("u1")},
		},
	})
}

func TestUserGroupMembersEdgeCases(t *testing.T) {
	f := newFakeJumpCloud(t)
	f.addUser(client.User{ID: "u1"})
	static, dynamic := f.addGroup("static", ""), f.addGroup("dynamic", "")
	f.editGroup(dynamic, func(g *fakeGroup) {
		g.MembershipMethod = "DYNAMIC_AUTOMATED"
		g.MemberQuery = json.RawMessage(`{"filters":[]}`)
	})
	config := func(group string) string {
		return f.providerConfig() + fmt.Sprintf(`
resource "jumpcloud_user_group_members" "test" {
  group_id = %q
  user_ids = ["u1"]
}`, group)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{Config: config(dynamic), ExpectError: regexp.MustCompile(`(?s)dynamic.*membership_rule.include_user_ids`)},
			{Config: config("missing"), ExpectError: regexp.MustCompile(`User group missing does not exist`)},
			{Config: config(static)},
			{
				// A group made dynamic outside Terraform keeps its last members in state with a
				// warning, instead of a diff that no apply can fix.
				PreConfig: func() {
					f.editGroup(static, func(g *fakeGroup) {
						g.MembershipMethod = "DYNAMIC_AUTOMATED"
						g.MemberQuery = json.RawMessage(`{"filters":[{"field":"user.email","operation":"equals","value":"nobody@example.com"}]}`)
					})
				},
				Config:   config(static),
				PlanOnly: true,
			},
			{
				// A group deleted outside Terraform is removed from state.
				PreConfig:          func() { f.deleteGroup(static) },
				Config:             config(static),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}
