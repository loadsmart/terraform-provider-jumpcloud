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
make docs     # regenerate docs/ from the schema and examples/
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

## Releasing

Releases are published to the Terraform Registry as [`loadsmart/jumpcloud`](https://registry.terraform.io/providers/loadsmart/jumpcloud). Pushing a `v*` tag runs `.github/workflows/release.yml`, which builds the binaries with GoReleaser and signs the checksums with the GPG key in the `GPG_PRIVATE_KEY` and `GPG_PASSPHRASE` secrets. The HCP Terraform GitHub App notifies the Registry about the new release.

```shell
git tag v0.1.0
git push origin v0.1.0
```

Never move or delete a published tag; release a new version instead. The signing key and the `loadsmart` namespace are managed in the HCP Terraform `Loadsmart` organization (Registry > Public namespaces).

## License

[MIT](LICENSE)
