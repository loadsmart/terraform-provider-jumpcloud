data "jumpcloud_user_group" "engineering" {
  name = "Engineering"
}

# Custom attributes are JSON, so decode a value before using it.
output "engineering_location" {
  value = jsondecode(data.jumpcloud_user_group.engineering.custom_attributes["otherInfo"]).location
}
