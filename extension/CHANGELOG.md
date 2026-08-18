# Changelog

This file is the Marketplace **Changelog** tab. It ships inside the `.vsix`; anything not written
here is invisible to someone deciding whether to install.

## [Unreleased]

### Added

- A Marketplace listing body (`extension/README.md`) and this changelog. Both are packaged into the
  `.vsix` and asserted by CI — see below for why that assertion exists.

### Fixed

- **The packaged extension had no listing body at all.** `vsce package` succeeds without a
  `README.md` and prints no warning, so the `.vsix` CI built on every pull request contained a
  licence, a manifest and compiled JavaScript — and nothing that would render on the Marketplace
  page. The existing packaging assertions checked the licence and the entrypoint, neither of which
  can see a missing README. Publishing that artefact would have produced a listing with a blank
  description page.

## [0.1.0]

Not yet published to the Marketplace. `0.1.0` is the version in `extension/package.json`; the
extension is installable today only from a locally built `.vsix` or the repository.
