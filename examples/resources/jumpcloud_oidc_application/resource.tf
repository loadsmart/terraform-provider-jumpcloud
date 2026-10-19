resource "jumpcloud_oidc_application" "grafana" {
  display_label  = "grafana-production"
  show_in_portal = true
  redirect_uris  = ["https://grafana.example.com/login/generic_oauth"]
  login_url      = "https://grafana.example.com"

  # Optional token lifetimes in seconds; omitted values keep JumpCloud's settings.
  access_token_lifetime_seconds  = 3600
  id_token_lifetime_seconds      = 3600
  refresh_token_lifetime_seconds = 2592000

  # JumpCloud only puts mapped claims in the ID token and userinfo response.
  # given_name and family_name are what the console's Profile scope adds.
  claims = {
    email       = "email"
    name        = "fullname"
    given_name  = "firstname"
    family_name = "lastname"
    groups      = "groups"
  }

  # Same value for every user; the console's Email scope adds email_verified.
  static_claims = {
    email_verified = "true"
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
