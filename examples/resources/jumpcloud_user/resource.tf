# A test account for synthetic sign-in checks. People come from the HR integration.
resource "jumpcloud_user" "synthetic_test" {
  username   = "synthetic.test"
  email      = "synthetic.test@example.com"
  first_name = "Synthetic"
  last_name  = "Test"
  department = "Engineering"
}
