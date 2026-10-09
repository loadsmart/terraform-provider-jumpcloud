data "jumpcloud_users" "platform" {
  filter = { department = "Platform" }
}

resource "jumpcloud_user_group_members" "platform" {
  group_id = jumpcloud_user_group.platform.id
  user_ids = data.jumpcloud_users.platform.users[*].id
}
