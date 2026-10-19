data "jumpcloud_role" "read_only" {
  name = "Read Only"
}

resource "jumpcloud_service_account" "sa_example" {
  name                 = "sa-example"
  role_id              = data.jumpcloud_role.read_only.id
  secret_lifetime_days = 365
}

# Hand the credentials to the application without them leaving Terraform.
resource "aws_secretsmanager_secret_version" "jumpcloud" {
  secret_id = aws_secretsmanager_secret.jumpcloud.id
  secret_string = jsonencode({
    client_id     = jumpcloud_service_account.sa_example.client_id
    client_secret = jumpcloud_service_account.sa_example.client_secret
  })
}
