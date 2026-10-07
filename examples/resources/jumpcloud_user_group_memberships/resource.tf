data "jumpcloud_user" "ana" {
  email = "ana@example.com"
}

resource "jumpcloud_user_group_memberships" "ana" {
  user_id = data.jumpcloud_user.ana.id
  group_ids = [
    jumpcloud_user_group.platform.id,
  ]
}
