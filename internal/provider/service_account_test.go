package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestServiceAccountResource(t *testing.T) {
	f := newFakeJumpCloud(t)
	f.addRole("Read Only", false)
	f.addRole("Group Reader", true)
	const addr = "jumpcloud_service_account.test"

	config := func(role, extra string) string {
		return f.providerConfig() + fmt.Sprintf(`
data "jumpcloud_role" "test" {
  name = %q
}

resource "jumpcloud_service_account" "test" {
  name    = "sa-example"
  role_id = data.jumpcloud_role.test.id
  %s
}`, role, extra)
	}
	// stateID copies the account ID out of the state, to compare it across steps.
	var firstID string
	stateID := func(dst *string) resource.TestCheckFunc {
		return func(s *terraform.State) error {
			*dst = s.RootModule().Resources[addr].Primary.ID
			return nil
		}
	}
	inJumpCloud := func(want bool) resource.TestCheckFunc {
		return func(s *terraform.State) error {
			id := s.RootModule().Resources[addr].Primary.ID
			if ok := f.hasServiceAccount(id); ok != want {
				return fmt.Errorf("service account %s exists = %t, want %t", id, ok, want)
			}
			return nil
		}
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			if len(f.accounts) != 0 {
				return fmt.Errorf("%d service accounts left after destroy", len(f.accounts))
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: config("Read Only", "secret_lifetime_days = 45"), ExpectError: regexp.MustCompile(`Unsupported secret lifetime`)},
			{
				Config: config("Read Only", ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair(addr, "role_id", "data.jumpcloud_role.test", "id"),
					resource.TestCheckResourceAttr(addr, "secret_lifetime_days", "365"),
					resource.TestMatchResourceAttr(addr, "client_id", regexp.MustCompile(`^client-`)),
					resource.TestMatchResourceAttr(addr, "client_secret", regexp.MustCompile(`^secret-`)),
					resource.TestCheckResourceAttr(addr, "expires_at", "2028-01-01T00:00:00Z"),
					resource.TestCheckResourceAttr(addr, "status", "ACTIVE"),
					inJumpCloud(true),
					stateID(&firstID),
				),
			},
			{
				// Reads do not return the secret, so the one from create must stay.
				Config: config("Read Only", ""),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				Check: resource.TestMatchResourceAttr(addr, "client_secret", regexp.MustCompile(`^secret-`)),
			},
			{
				// JumpCloud cannot change an account, so a new role replaces it.
				Config: config("Group Reader", `secret_lifetime_days = 90`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "secret_lifetime_days", "90"),
					resource.TestCheckResourceAttr(addr, "expires_at", "2027-04-01T00:00:00Z"),
					resource.TestMatchResourceAttr(addr, "client_secret", regexp.MustCompile(`^secret-`)),
					func(s *terraform.State) error {
						if id := s.RootModule().Resources[addr].Primary.ID; id == firstID {
							return fmt.Errorf("ID is still %s after a replacement", id)
						}
						if f.hasServiceAccount(firstID) {
							return fmt.Errorf("replaced service account %s still exists", firstID)
						}
						return nil
					},
				),
			},
			{
				// Deleted in the admin console: created again.
				PreConfig: func() {
					f.mu.Lock()
					clear(f.accounts)
					f.mu.Unlock()
				},
				Config: config("Group Reader", `secret_lifetime_days = 90`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionCreate)},
				},
				Check: inJumpCloud(true),
			},
			{
				ResourceName:            addr,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"client_secret"},
			},
		},
	})
}

func TestRoleDataSource(t *testing.T) {
	f := newFakeJumpCloud(t)
	f.addRole("Read Only", false)
	custom := f.addRole("Group Reader", true)
	f.addRole("Twin", true)
	f.addRole("Twin", true)
	const addr = "data.jumpcloud_role.test"

	config := func(name string) string {
		return f.providerConfig() + fmt.Sprintf(`
data "jumpcloud_role" "test" {
  name = %q
}`, name)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config("Group Reader"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "id", custom),
					resource.TestCheckResourceAttr(addr, "description", "Group Reader role"),
					resource.TestCheckResourceAttr(addr, "custom", "true"),
				),
			},
			{Config: config("group reader"), ExpectError: regexp.MustCompile(`Expected one role named "group reader", found 0`)},
			{Config: config("Twin"), ExpectError: regexp.MustCompile(`Expected one role named "Twin", found 2`)},
		},
	})
}
