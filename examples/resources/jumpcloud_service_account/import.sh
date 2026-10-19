# Import by service account ID. JumpCloud never returns the secret again, so
# client_secret is empty after an import; replace the account to get a new one.
terraform import jumpcloud_service_account.sa_example 6ac6b310f1aac9c461f1da73
