data "jumpcloud_users" "ana" {
  filter = { email = "ana@example.com" }
}

resource "jumpcloud_user_group_memberships" "ana" {
  user_id = one(data.jumpcloud_users.ana.users).id
  group_ids = [
    jumpcloud_user_group.platform.id,
  ]
}
