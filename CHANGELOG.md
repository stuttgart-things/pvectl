## [0.6.4](https://github.com/stuttgart-things/pvectl/compare/v0.6.3...v0.6.4) (2026-11-24)


### Bug Fixes

* **migrate:** wait until an HA-managed VM is on the target ([#23](https://github.com/stuttgart-things/pvectl/issues/23)) ([f81d9d9](https://github.com/stuttgart-things/pvectl/commit/f81d9d9fc3f0181c92f997f5ac4042bd51d32083))

## [0.6.3](https://github.com/stuttgart-things/pvectl/compare/v0.6.2...v0.6.3) (2026-11-22)


### Bug Fixes

* **deps:** bubbles v0.21.1 and x/term v0.2.2 ([#21](https://github.com/stuttgart-things/pvectl/issues/21)) ([4c97564](https://github.com/stuttgart-things/pvectl/commit/4c975647038ded705cb51188ef2b60ee9b3088bd))

## [0.6.2](https://github.com/stuttgart-things/pvectl/compare/v0.6.1...v0.6.2) (2026-11-22)


### Bug Fixes

* **deps:** update module github.com/lucasb-eyer/go-colorful to v1.4.1 ([#14](https://github.com/stuttgart-things/pvectl/issues/14)) ([b4a24ac](https://github.com/stuttgart-things/pvectl/commit/b4a24acd29310f5e0b31e28261acd833d2fe126e))
* **deps:** update module github.com/spf13/pflag to v1.1.0 ([#16](https://github.com/stuttgart-things/pvectl/issues/16)) ([f464c5f](https://github.com/stuttgart-things/pvectl/commit/f464c5fd4cc8eb06b9503a3f0f98a7b52390214b))

## [0.6.1](https://github.com/stuttgart-things/pvectl/compare/v0.6.0...v0.6.1) (2026-11-22)


### Bug Fixes

* **pve:** cpu check accepts x86-64-v3+, warns for non-Linux guests ([#20](https://github.com/stuttgart-things/pvectl/issues/20)) ([f79549c](https://github.com/stuttgart-things/pvectl/commit/f79549c0a310f50e1db336c75fda95e28c60c2c0))

# [0.6.0](https://github.com/stuttgart-things/pvectl/compare/v0.5.0...v0.6.0) (2026-11-22)


### Features

* **pve:** FindByName, Rename, Delete, Networks, ClusterStorages, PinnedKnownHosts ([#19](https://github.com/stuttgart-things/pvectl/issues/19)) ([1be691f](https://github.com/stuttgart-things/pvectl/commit/1be691f034fac00432a021e7cefaae5a725954e3))

# [0.5.0](https://github.com/stuttgart-things/pvectl/compare/v0.4.0...v0.5.0) (2026-11-21)


### Features

* **pve:** API token login (PVE_API_TOKEN_ID/PVE_API_TOKEN_SECRET) ([#18](https://github.com/stuttgart-things/pvectl/issues/18)) ([a0d5a78](https://github.com/stuttgart-things/pvectl/commit/a0d5a7824c7077851f0f10dcceca8638a5bd05f3))

# [0.4.0](https://github.com/stuttgart-things/pvectl/compare/v0.3.0...v0.4.0) (2026-11-19)


### Features

* **tls:** pin the API certificate instead of --insecure ([#15](https://github.com/stuttgart-things/pvectl/issues/15)) ([8e5b100](https://github.com/stuttgart-things/pvectl/commit/8e5b100d6c0ddc095c0eadc7fff02b5caf943b6d))

# [0.3.0](https://github.com/stuttgart-things/pvectl/compare/v0.2.0...v0.3.0) (2026-11-18)


### Features

* **ui:** charm look with logo, start menu, cards and tables ([#12](https://github.com/stuttgart-things/pvectl/issues/12)) ([6291fd8](https://github.com/stuttgart-things/pvectl/commit/6291fd897f7ef9454ffe4e3e0b91cc1c938db4e5))

# [0.2.0](https://github.com/stuttgart-things/pvectl/compare/v0.1.1...v0.2.0) (2026-11-18)


### Features

* **promote:** warn about older or pinned templates, require --allow-old ([#11](https://github.com/stuttgart-things/pvectl/issues/11)) ([be5701c](https://github.com/stuttgart-things/pvectl/commit/be5701cf25782b8e997788f0bed159616abe3566))

## [0.1.1](https://github.com/stuttgart-things/pvectl/compare/v0.1.0...v0.1.1) (2026-11-18)


### Bug Fixes

* help text shows the promote order (migrate, then FS9100 copy) ([#10](https://github.com/stuttgart-things/pvectl/issues/10)) ([1e57d65](https://github.com/stuttgart-things/pvectl/commit/1e57d651195c6d39151783039a7b7b4a78648fee))

# [0.1.0](https://github.com/stuttgart-things/pvectl/compare/v0.0.0...v0.1.0) (2026-11-18)


### Bug Fixes

* start versioning at v0.1.0 ([#5](https://github.com/stuttgart-things/pvectl/issues/5)) ([975f9dc](https://github.com/stuttgart-things/pvectl/commit/975f9dc674d766e48dd83c652ed17655ac5d3fea))


### Features

* pvectl CLI and library for LabUL Proxmox template work ([#1](https://github.com/stuttgart-things/pvectl/issues/1)) ([b7ba541](https://github.com/stuttgart-things/pvectl/commit/b7ba541c926c4a0afe8c806fb52b58c25bb47d10)), closes [stuttgart-things/stuttgart-things#4652](https://github.com/stuttgart-things/stuttgart-things/issues/4652)
