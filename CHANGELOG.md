# Changelog

## [0.6.0](https://github.com/robgee86/arduino-test-buddy/compare/v0.5.0...v0.6.0) (2026-11-18)


### Features

* **plugin:** fetch the program from its release, installing the plugin is all it takes ([#15](https://github.com/robgee86/arduino-test-buddy/issues/15)) ([5dcf193](https://github.com/robgee86/arduino-test-buddy/commit/5dcf19398e3d6f3582e958213cd4060740ec5b17))


### Bug Fixes

* **mcp:** end the server on a stop signal while a request is open ([#16](https://github.com/robgee86/arduino-test-buddy/issues/16)) ([52fed41](https://github.com/robgee86/arduino-test-buddy/commit/52fed4117eee9588dbd72aca41721aa8e58daf90))

## [0.5.0](https://github.com/robgee86/arduino-test-buddy/compare/v0.4.0...v0.5.0) (2026-11-18)


### Features

* build and test for native Windows ([#11](https://github.com/robgee86/arduino-test-buddy/issues/11)) ([f388530](https://github.com/robgee86/arduino-test-buddy/commit/f388530aecce5de2d3e4b65c22701d8e354ef258))
* **plugin:** ship the board-test skill and the MCP server as a Claude Code plugin ([#10](https://github.com/robgee86/arduino-test-buddy/issues/10)) ([5e7fd1e](https://github.com/robgee86/arduino-test-buddy/commit/5e7fd1e21b67e82b95eed79d7b46553df544c07a))
* **push:** build the checkout set in the config file when none is given and the current folder is not one ([#13](https://github.com/robgee86/arduino-test-buddy/issues/13)) ([aa71e7d](https://github.com/robgee86/arduino-test-buddy/commit/aa71e7dad7ac52b184ef4b70de89a4a550b16345))

## [0.4.0](https://github.com/robgee86/arduino-test-buddy/compare/v0.3.0...v0.4.0) (2026-11-18)


### Features

* drop registry tags after 3 days and remove the images apps pulled on cleanup ([#8](https://github.com/robgee86/arduino-test-buddy/issues/8)) ([b468e84](https://github.com/robgee86/arduino-test-buddy/commit/b468e8431257f579e3da074a50f27ffa9f7288bf))

## [0.3.0](https://github.com/robgee86/arduino-test-buddy/compare/v0.2.0...v0.3.0) (2026-10-22)


### Features

* **preflight:** report kernel and security modules, keep the release's assets on cleanup ([#4](https://github.com/robgee86/arduino-test-buddy/issues/4)) ([fac4e7b](https://github.com/robgee86/arduino-test-buddy/commit/fac4e7b11f3e596b706d89bf5e22a8d8a39d321b))


### Bug Fixes

* **preflight:** stop collecting the security modules, keep only the kernel version ([#6](https://github.com/robgee86/arduino-test-buddy/issues/6)) ([49f3acf](https://github.com/robgee86/arduino-test-buddy/commit/49f3acf14723edda8811e14a0e1a5210121ee903))
* **run:** read-only tunnel to the host registry, with the images a run used verified ([#7](https://github.com/robgee86/arduino-test-buddy/issues/7)) ([66893c8](https://github.com/robgee86/arduino-test-buddy/commit/66893c869b78cc5cff25562abc5120d05e8dc62b))

## [0.2.0](https://github.com/robgee86/arduino-test-buddy/compare/v0.1.0...v0.2.0) (2026-10-22)


### ⚠ BREAKING CHANGES

* **run:** name the app with examples:, user: or host: prefixes and drop --dir/local_dir ([#2](https://github.com/robgee86/arduino-test-buddy/issues/2))

### Features

* **run:** name the app with examples:, user: or host: prefixes and drop --dir/local_dir ([#2](https://github.com/robgee86/arduino-test-buddy/issues/2)) ([b33aeee](https://github.com/robgee86/arduino-test-buddy/commit/b33aeeed989161b789bd6d55ac04db5daef99e89))

## 0.1.0 (2026-10-09)


### Features

* board test CLI and MCP server with registry push, run, logs, exec and scoped cleanup ([2c00458](https://github.com/robgee86/arduino-test-buddy/commit/2c00458a403bc6d16b866763a884f88c19704a35))
* explicit up, down and status, nothing runs or restarts in the background ([6734b39](https://github.com/robgee86/arduino-test-buddy/commit/6734b39ba2d89012ac87dd7dc8f124c098490c3e))
* push every container by default and push whatever bake produced under the tag ([cea0c6e](https://github.com/robgee86/arduino-test-buddy/commit/cea0c6e7310d42788e25399f40dc4d7456b6a6b3))
* registry and builder on the developer machine, boards pull through a per-run tunnel and take turns ([72f8a61](https://github.com/robgee86/arduino-test-buddy/commit/72f8a619b03d567cb67605c43a834efb526a1487))
* select examples by the brick folder name as well as its id ([beab85a](https://github.com/robgee86/arduino-test-buddy/commit/beab85ad5d2f0f2add0d89078ff2e80b01c25043))
* trim stale build cache on up and down, list tags with their last push in status ([4fed304](https://github.com/robgee86/arduino-test-buddy/commit/4fed3044c461dc1f311b5e8ffe60f9921fde74a8))


### Bug Fixes

* end the board ssh session with its turn, restart a session's own app, find apps by folder ([6b757fa](https://github.com/robgee86/arduino-test-buddy/commit/6b757fa68c96377cf6972ae7442033dcda08a851))
* keep build cache used within 3 days so parallel branches don't evict each other ([e4178c5](https://github.com/robgee86/arduino-test-buddy/commit/e4178c57c99da84c55202cdca390bdfd927ca621))
* list the built images without Docker's reference filter, which cannot span the repository slash ([c6e6edc](https://github.com/robgee86/arduino-test-buddy/commit/c6e6edcee05e05c11280107a8736a981745aacdd))
