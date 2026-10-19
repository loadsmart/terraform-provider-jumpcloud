resource "jumpcloud_application_logo" "grafana" {
  application_id = jumpcloud_oidc_application.grafana.id
  file           = "${path.module}/grafana.png"
}
