package provider

import (
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"

	"github.com/loadsmart/terraform-provider-jumpcloud/internal/client"
)

var testProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"jumpcloud": providerserver.NewProtocol6WithError(New("test")()),
}

// testAccPreCheck runs before acceptance tests, which create real objects in the
// JumpCloud organization behind JUMPCLOUD_API_KEY. Their names start with "tfacc-".
func testAccPreCheck(t *testing.T) {
	if os.Getenv("JUMPCLOUD_API_KEY") == "" {
		t.Fatal("JUMPCLOUD_API_KEY must be set for acceptance tests")
	}
}

func testAccClient(t *testing.T) *client.Client {
	cfg, err := resolveConfig(providerModel{}, os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	return client.New(cfg.APIURL, cfg.APIKey, cfg.OrgID, "terraform-provider-jumpcloud/test")
}
