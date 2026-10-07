# terraform-provider-jumpcloud

Terraform provider for managing JumpCloud user groups, group memberships, and application associations.

This provider is in early development and has no resources yet. The v0.1 scope is:

- Resources: `jumpcloud_user_group`, `jumpcloud_user_group_memberships`, `jumpcloud_application_association`
- Data sources: `jumpcloud_user_group`, `jumpcloud_user_groups`, `jumpcloud_user`, `jumpcloud_users`, `jumpcloud_application`

## Provider configuration

```hcl
provider "jumpcloud" {
  # api_key = "..."                              # or JUMPCLOUD_API_KEY
  # org_id  = "..."                              # or JUMPCLOUD_ORG_ID, multi-tenant admins only
  # api_url = "https://console.eu.jumpcloud.com" # or JUMPCLOUD_API_URL, defaults to https://console.jumpcloud.com
}
```

Arguments take precedence over environment variables.

## Development

Requirements: Go (version in `go.mod`), [golangci-lint](https://golangci-lint.run/) v2, and Terraform 1.0 or later.

```shell
make build    # compile
make lint     # golangci-lint
make test     # unit tests
make testacc  # acceptance tests; creates real objects in the JumpCloud org behind JUMPCLOUD_API_KEY
```

To try a local build, install it with `make install` and point Terraform at it with a `dev_overrides` block in `~/.terraformrc`:

```hcl
provider_installation {
  dev_overrides {
    "loadsmart/jumpcloud" = "/path/to/your/GOPATH/bin"
  }
  direct {}
}
```

## License

[MIT](LICENSE)
