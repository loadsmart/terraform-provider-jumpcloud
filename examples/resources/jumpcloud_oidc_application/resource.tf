resource "jumpcloud_oidc_application" "grafana" {
  display_label  = "grafana-production"
  show_in_portal = true
  redirect_uris  = ["https://grafana.example.com/login/generic_oauth"]
  login_url      = "https://grafana.example.com"

  # JumpCloud only puts mapped claims in the ID token and userinfo response.
  claims = {
    email  = "email"
    name   = "fullname"
    groups = "groups"
  }
}

resource "jumpcloud_application_association" "grafana_engineering" {
  application_id = jumpcloud_oidc_application.grafana.id
  type           = "user_group"
  target_id      = jumpcloud_user_group.engineering.id
}

output "grafana_client_id" {
  value = jumpcloud_oidc_application.grafana.client_id
}

output "grafana_client_secret" {
  value     = jumpcloud_oidc_application.grafana.client_secret
  sensitive = true
}
