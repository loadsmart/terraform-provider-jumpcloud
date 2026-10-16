# Let a test account sign in to the user portal without MFA, only from known addresses.
resource "jumpcloud_user_group" "synthetic_test" {
  name = "Synthetic Test"
}

resource "jumpcloud_ip_list" "synthetics" {
  name = "datadog-synthetics"
  ips  = ["203.0.113.7"]
}

resource "jumpcloud_authentication_policy" "synthetic_test" {
  name           = "Synthetic test - no MFA from Datadog"
  type           = "user_portal"
  action         = "allow"
  mfa_required   = false
  user_group_ids = [jumpcloud_user_group.synthetic_test.id]
  conditions     = jsonencode({ ipAddressIn = [jumpcloud_ip_list.synthetics.id] })
}

# Require MFA on every SSO application, except from the office.
resource "jumpcloud_ip_list" "office" {
  name = "office"
  ips  = ["198.51.100.0/24"]
}

resource "jumpcloud_authentication_policy" "apps_mfa" {
  name         = "Require MFA on applications"
  type         = "application"
  action       = "allow"
  mfa_required = true
  all_users    = true
  conditions   = jsonencode({ not = { ipAddressIn = [jumpcloud_ip_list.office.id] } })
}
