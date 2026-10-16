# Find an application created in the admin console.
data "jumpcloud_application" "aws" {
  display_label = "AWS IAM Identity Center"
}

# Give its IdP details to the service provider.
output "aws_saml_metadata" {
  value = {
    sso_url     = data.jumpcloud_application.aws.sso_url
    entity_id   = data.jumpcloud_application.aws.idp_entity_id
    certificate = data.jumpcloud_application.aws.idp_certificate
  }
}
