package provider

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
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
					resource.TestCheckResourceAttr("data.jumpcloud_user_groups.all", "groups.#", "3"),
					resource.TestCheckResourceAttr("data.jumpcloud_user_groups.all", "groups.0.name", "devs"),
				),
			},
			{Config: lookup("dup"), ExpectError: regexp.MustCompile(`found 2`)},
			{Config: lookup("missing"), ExpectError: regexp.MustCompile(`found 0`)},
		},
	})
}

func TestAccUserGroup(t *testing.T) {
	name := "tfacc-" + acctest.RandString(8)
	const addr = "jumpcloud_user_group.test"
	config := func(description string) string {
		return fmt.Sprintf(`
resource "jumpcloud_user_group" "test" {
  name        = %q
  description = %q
}

data "jumpcloud_user_group" "test" {
  name = jumpcloud_user_group.test.name
}`, name, description)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testProviderFactories,
		CheckDestroy:             testAccCheckUserGroupsDestroyed(t),
		Steps: []resource.TestStep{
			{
				Config: config("Created by acceptance tests"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "description", "Created by acceptance tests"),
					resource.TestCheckResourceAttrPair("data.jumpcloud_user_group.test", "id", addr, "id"),
					resource.TestCheckResourceAttrPair("data.jumpcloud_user_group.test", "description", addr, "description"),
				),
			},
			{
				Config: config("Updated by acceptance tests"),
				Check:  resource.TestCheckResourceAttr(addr, "description", "Updated by acceptance tests"),
			},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true},
		},
	})
}

func testAccCheckUserGroupsDestroyed(t *testing.T) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		c := testAccClient(t)
		for _, rs := range s.RootModule().Resources {
			if rs.Type != "jumpcloud_user_group" {
				continue
			}
			if _, err := c.GetUserGroup(context.Background(), rs.Primary.ID); !errors.Is(err, client.ErrNotFound) {
				return fmt.Errorf("user group %s still exists (err: %v)", rs.Primary.ID, err)
			}
		}
		return nil
	}
}
