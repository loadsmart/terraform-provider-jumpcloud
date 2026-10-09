# A custom SAML application.
resource "jumpcloud_saml_application" "n8n" {
  display_label    = "n8n"
  show_in_portal   = true
  sp_entity_id     = "https://n8n.example.com/rest/sso/saml/metadata"
  acs_urls         = ["https://n8n.example.com/rest/sso/saml/acs"]
  name_id          = "email"
  groups_attribute = "groups"
  user_attributes = {
    email     = "email"
    firstName = "firstname"
    lastName  = "lastname"
  }
}

# AWS IAM Identity Center from JumpCloud's catalog. Enable user provisioning (SCIM)
# in the admin console; it has no API.
resource "jumpcloud_saml_application" "aws" {
  display_label  = "AWS IAM Identity Center"
  template       = "aws-sso"
  show_in_portal = true
  sp_entity_id   = "https://us-east-1.signin.aws.amazon.com/platform/saml/d-1234567890"
  acs_urls       = ["https://us-east-1.signin.aws.amazon.com/platform/saml/acs/00000000-0000-0000-0000-000000000000"]
  idp_init_url   = "https://d-1234567890.awsapps.com/start"
}

resource "jumpcloud_application_association" "aws_platform" {
  application_id = jumpcloud_saml_application.aws.id
  type           = "user_group"
  target_id      = jumpcloud_user_group.platform.id
}

output "aws_idp_certificate" {
  value = jumpcloud_saml_application.aws.idp_certificate
}
