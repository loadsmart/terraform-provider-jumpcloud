data "jumpcloud_users" "engineering" {
  filter = {
    department = "Engineering"
    state      = "ACTIVATED"
  }
}

locals {
  engineer_emails = [for user in data.jumpcloud_users.engineering.users : user.email]
}
