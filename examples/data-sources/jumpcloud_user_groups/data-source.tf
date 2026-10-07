data "jumpcloud_user_groups" "all" {}

locals {
  squad_group_ids = [
    for group in data.jumpcloud_user_groups.all.groups : group.id
    if length(regexall("-squad$", group.name)) > 0
  ]
}
