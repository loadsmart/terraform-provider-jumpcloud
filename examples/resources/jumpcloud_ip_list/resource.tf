resource "jumpcloud_ip_list" "office" {
  name        = "office"
  description = "Office egress addresses"
  ips         = ["203.0.113.7", "198.51.100.0/24"]
}
