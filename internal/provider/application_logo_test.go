package provider

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestApplicationLogoResource(t *testing.T) {
	f := newFakeJumpCloud(t)
	app := f.addApp("Grafana", "oidc")
	const addr = "jumpcloud_application_logo.test"

	dir := t.TempDir()
	write := func(name string, content []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, content, 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	png := func(s string) []byte { return append([]byte("\x89PNG\r\n\x1a\n"), s...) }
	logo := write("logo.png", png("first"))
	copied := write("copy.png", png("first"))
	text := write("logo.txt", []byte("not an image"))

	config := func(file string) string {
		return f.providerConfig() + fmt.Sprintf(`
resource "jumpcloud_application_logo" "test" {
  application_id = %q
  file           = %q
}`, app, file)
	}
	stored := func(want []byte, uploads int) resource.TestCheckFunc {
		return func(*terraform.State) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			if !bytes.Equal(f.logos[app], want) || f.uploads != uploads {
				return fmt.Errorf("JumpCloud has %q after %d uploads, want %q after %d", f.logos[app], f.uploads, want, uploads)
			}
			return nil
		}
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		CheckDestroy: func(*terraform.State) error {
			f.mu.Lock()
			defer f.mu.Unlock()
			if _, ok := f.logos[app]; ok {
				return fmt.Errorf("logo still set after destroy")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{Config: config(text), ExpectError: regexp.MustCompile(`JumpCloud accepts PNG or JPEG`)},
			{
				Config: config(logo),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(addr, "id", app),
					resource.TestCheckResourceAttrPair(addr, "id", addr, "application_id"),
					resource.TestMatchResourceAttr(addr, "url", regexp.MustCompile(`/images/applications/`)),
					resource.TestMatchResourceAttr(addr, "sha256", regexp.MustCompile(`^[0-9a-f]{64}$`)),
					stored(png("first"), 1),
				),
			},
			{
				// Same image at another path: no upload.
				Config: config(copied),
				Check:  stored(png("first"), 1),
			},
			{
				// New image at the same path: the hash change triggers an upload.
				PreConfig: func() { write("copy.png", png("second")) },
				Config:    config(copied),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate)},
				},
				Check: stored(png("second"), 2),
			},
			{
				// Removed in the admin console: uploaded again.
				PreConfig: func() {
					f.mu.Lock()
					delete(f.logos, app)
					f.mu.Unlock()
				},
				Config: config(copied),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionCreate)},
				},
				Check: stored(png("second"), 3),
			},
			{
				ResourceName:            addr,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"file", "sha256"},
			},
		},
	})
}

func TestApplicationLogoMissingApplication(t *testing.T) {
	f := newFakeJumpCloud(t)
	file := filepath.Join(t.TempDir(), "logo.png")
	if err := os.WriteFile(file, []byte("\x89PNG\r\n\x1a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testProviderFactories,
		Steps: []resource.TestStep{{
			Config: f.providerConfig() + fmt.Sprintf(`
resource "jumpcloud_application_logo" "test" {
  application_id = "missing"
  file           = %q
}`, file),
			ExpectError: regexp.MustCompile(`Application missing does not exist`),
		}},
	})
}
