# terraform-provider-jumpcloud

Terraform provider for managing JumpCloud user groups, group memberships, OIDC applications, and application access.

This provider is in early development. It currently supports:

- Resources: `jumpcloud_user_group`, `jumpcloud_user_group_memberships`, `jumpcloud_oidc_application`, `jumpcloud_application_association`
- Data sources: `jumpcloud_user_group`, `jumpcloud_user_groups`, `jumpcloud_users`, `jumpcloud_application`

See [`docs/`](docs/) for arguments and examples.

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

Requirements: Go (version in `go.mod`), [golangci-lint](https://golangci-lint.run/) v2, and Terraform 1.0 or later on `PATH` (the unit tests run it against a fake JumpCloud API).

```shell
make build    # compile
make lint     # golangci-lint
make test     # unit tests
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

Releases are continuous. On every push to `main`, [release-please](https://github.com/googleapis/release-please) opens a release PR from the conventional commits since the last release, and the Rollbot app merges it right away:

| Commit type | Version bump |
| --- | --- |
| `fix:` | patch (`v0.1.0` → `v0.1.1`) |
| `feat:` | minor (`v0.1.0` → `v0.2.0`) |
| `feat!:` or `fix!:` | minor while on `v0`, major from `v1` on |
| `perf:`, `revert:` | patch |
| `ci:`, `docs:`, `chore:`, `refactor:`, `test:`, `build:`, `style:` | no release |

Squash merges use the PR title as the commit message and leave the body blank, so mark breaking changes with `!` in the PR title; a `BREAKING CHANGE:` footer is not seen.

The merged release PR updates `CHANGELOG.md` and creates a draft GitHub release with its `v*` tag. The tag runs `.github/workflows/release.yml`, which uploads the GoReleaser artifacts signed with the `GPG_PRIVATE_KEY` key and publishes the release. The HCP Terraform GitHub App then notifies the Terraform Registry ([`loadsmart/jumpcloud`](https://registry.terraform.io/providers/loadsmart/jumpcloud)).

Published versions are permanent: never move or delete a tag, release a fix instead. The signing key and the `loadsmart` namespace are managed in the HCP Terraform `Loadsmart` organization (Registry > Public namespaces).

## License

[MIT](LICENSE)
