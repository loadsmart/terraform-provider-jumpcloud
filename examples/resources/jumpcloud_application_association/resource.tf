data "jumpcloud_application" "aws" {
  display_label = "AWS IAM Identity Center"
}

resource "jumpcloud_application_association" "aws_platform" {
  application_id = data.jumpcloud_application.aws.id
  type           = "user_group"
  target_id      = jumpcloud_user_group.platform.id
}
