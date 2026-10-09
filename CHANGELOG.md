# Changelog

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
