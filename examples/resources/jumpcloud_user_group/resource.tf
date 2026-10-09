# A static group: members are added with jumpcloud_user_group_members or
# jumpcloud_user_group_memberships.
resource "jumpcloud_user_group" "platform" {
  name        = "platform-squad"
  description = "Platform squad members"
}

# A dynamic group: JumpCloud keeps its members in line with the rule.
resource "jumpcloud_user_group" "engineering" {
  name  = "engineering"
  email = "engineering@example.com"

  membership_rule = {
    query = jsonencode({
      filters = [
        { field = "user.user_department", operation = "equals", value = "Engineering" },
        { field = "user.user_state", operation = "equals", value = "ACTIVATED" },
      ]
    })
    include_user_ids = [one(data.jumpcloud_users.cto.users).id]
  }

  sudo = {
    enabled          = true
    without_password = false
  }
}

data "jumpcloud_users" "cto" {
  filter = { email = "cto@example.com" }
}
