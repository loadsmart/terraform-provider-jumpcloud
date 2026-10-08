# Find an application created in the admin console.
data "jumpcloud_application" "aws" {
  display_label = "AWS IAM Identity Center"
}
