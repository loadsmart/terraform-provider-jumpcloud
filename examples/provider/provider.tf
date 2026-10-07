terraform {
  required_providers {
    jumpcloud = {
      source = "loadsmart/jumpcloud"
    }
  }
}

# Reads JUMPCLOUD_API_KEY (and JUMPCLOUD_ORG_ID, JUMPCLOUD_API_URL) from the environment.
provider "jumpcloud" {}
