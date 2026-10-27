# Changelog

## [0.14.1](https://github.com/loadsmart/terraform-provider-jumpcloud/compare/v0.14.0...v0.14.1) (2026-10-27)


### Bug Fixes

* use single-unit oidc token lifespans ([#37](https://github.com/loadsmart/terraform-provider-jumpcloud/issues/37)) ([e8c1471](https://github.com/loadsmart/terraform-provider-jumpcloud/commit/e8c1471264a00e6614c1063c5747685b9147740e))

## [0.14.0](https://github.com/loadsmart/terraform-provider-jumpcloud/compare/v0.13.0...v0.14.0) (2026-10-22)


### Features

* manage service accounts (`jumpcloud_service_account` resource) and read roles (`jumpcloud_role` data source) ([#33](https://github.com/loadsmart/terraform-provider-jumpcloud/issues/33)) ([a747c5e](https://github.com/loadsmart/terraform-provider-jumpcloud/commit/a747c5e82b61c35bbb2de5d1ce674377396f0379))

## [0.13.0](https://github.com/loadsmart/terraform-provider-jumpcloud/compare/v0.12.0...v0.13.0) (2026-10-20)


### Features

* add declare redirect endpoint to saml applications ([#34](https://github.com/loadsmart/terraform-provider-jumpcloud/issues/34)) ([cfb1e58](https://github.com/loadsmart/terraform-provider-jumpcloud/commit/cfb1e58e59c2ff76a2b27dc5a33bc78c3b65716d))

## [0.12.0](https://github.com/loadsmart/terraform-provider-jumpcloud/compare/v0.11.0...v0.12.0) (2026-10-19)


### Features

* add multi-value saml attributes ([#31](https://github.com/loadsmart/terraform-provider-jumpcloud/issues/31)) ([69088bb](https://github.com/loadsmart/terraform-provider-jumpcloud/commit/69088bbf5cb8f7eff45ea3ecea79e630a01e93e8))

## [0.11.0](https://github.com/loadsmart/terraform-provider-jumpcloud/compare/v0.10.0...v0.11.0) (2026-10-19)


### Features

* add oidc application token lifetimes ([#29](https://github.com/loadsmart/terraform-provider-jumpcloud/issues/29)) ([8acbe7d](https://github.com/loadsmart/terraform-provider-jumpcloud/commit/8acbe7ddc19d0d4102fd97a971a60e8453133f88))

## [0.10.0](https://github.com/loadsmart/terraform-provider-jumpcloud/compare/v0.9.0...v0.10.0) (2026-10-19)


### Features

* add application logos ([#27](https://github.com/loadsmart/terraform-provider-jumpcloud/issues/27)) ([1e90f1f](https://github.com/loadsmart/terraform-provider-jumpcloud/commit/1e90f1fbfb9576f013a6b6cef66e2c63b70f158c))

## [0.9.0](https://github.com/loadsmart/terraform-provider-jumpcloud/compare/v0.8.0...v0.9.0) (2026-10-16)


### Features

* manage custom attributes on user groups ([#25](https://github.com/loadsmart/terraform-provider-jumpcloud/issues/25)) ([12533a7](https://github.com/loadsmart/terraform-provider-jumpcloud/commit/12533a7caea9247d19806b4a28290cab1544c2d7))

## [0.8.0](https://github.com/loadsmart/terraform-provider-jumpcloud/compare/v0.7.0...v0.8.0) (2026-10-16)


### Features

* add subject field to oidc applications ([#21](https://github.com/loadsmart/terraform-provider-jumpcloud/issues/21)) ([531eeec](https://github.com/loadsmart/terraform-provider-jumpcloud/commit/531eeeca5d0c980693d0c5a7bf869e6dbf6f941b))

## [0.7.0](https://github.com/loadsmart/terraform-provider-jumpcloud/compare/v0.6.0...v0.7.0) (2026-10-16)


### Features

* add access token format and audiences to oidc applications ([#20](https://github.com/loadsmart/terraform-provider-jumpcloud/issues/20)) ([608fc2b](https://github.com/loadsmart/terraform-provider-jumpcloud/commit/608fc2b027b8a513d550c9cd7b305fd847356afa))

## [0.6.0](https://github.com/loadsmart/terraform-provider-jumpcloud/compare/v0.5.0...v0.6.0) (2026-10-16)


### Features

* add static claims to oidc applications ([#19](https://github.com/loadsmart/terraform-provider-jumpcloud/issues/19)) ([4e42d8c](https://github.com/loadsmart/terraform-provider-jumpcloud/commit/4e42d8c9248e36895f464515609fa81ec5037183))

## [0.5.0](https://github.com/loadsmart/terraform-provider-jumpcloud/compare/v0.4.0...v0.5.0) (2026-10-16)


### Features

* add saml applications ([#15](https://github.com/loadsmart/terraform-provider-jumpcloud/issues/15)) ([0b877ff](https://github.com/loadsmart/terraform-provider-jumpcloud/commit/0b877ff8bf0220e0d210f85dd761a2a6206aa1a7))

## [0.4.0](https://github.com/loadsmart/terraform-provider-jumpcloud/compare/v0.3.1...v0.4.0) (2026-10-09)


### Features

* manage dynamic user groups and group members ([#13](https://github.com/loadsmart/terraform-provider-jumpcloud/issues/13)) ([e4c3d4c](https://github.com/loadsmart/terraform-provider-jumpcloud/commit/e4c3d4c47bca03e42d55f282956cac12f7624a79))

## [0.3.1](https://github.com/loadsmart/terraform-provider-jumpcloud/compare/v0.3.0...v0.3.1) (2026-10-08)


### Bug Fixes

* correct OIDC application import and update ([#11](https://github.com/loadsmart/terraform-provider-jumpcloud/issues/11)) ([d652b24](https://github.com/loadsmart/terraform-provider-jumpcloud/commit/d652b248d2974db6583190f76330d47390b26e26))

## [0.3.0](https://github.com/loadsmart/terraform-provider-jumpcloud/compare/v0.2.0...v0.3.0) (2026-10-08)


### Features

* add oidc applications and application associations ([#6](https://github.com/loadsmart/terraform-provider-jumpcloud/issues/6)) ([d109cf9](https://github.com/loadsmart/terraform-provider-jumpcloud/commit/d109cf9158dec9faf50d29a6d6f3076fd9b640c9))

## [0.2.0](https://github.com/loadsmart/terraform-provider-jumpcloud/compare/v0.1.0...v0.2.0) (2026-10-08)


### Features

* add user groups, memberships, and user lookups ([#4](https://github.com/loadsmart/terraform-provider-jumpcloud/issues/4)) ([27e0da9](https://github.com/loadsmart/terraform-provider-jumpcloud/commit/27e0da937b49e8594754332bf7420c08e537be75))

## 0.1.0 (2026-10-08)


### Features

* add jumpcloud api client ([#2](https://github.com/loadsmart/terraform-provider-jumpcloud/issues/2)) ([36bf145](https://github.com/loadsmart/terraform-provider-jumpcloud/commit/36bf1458a1ed60218553b337723d9956b7103225))
* scaffold jumpcloud terraform provider ([b199218](https://github.com/loadsmart/terraform-provider-jumpcloud/commit/b1992180db8e4a194e6b19ef96f68e88a193fc40))
