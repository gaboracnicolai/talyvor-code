# Changelog

This file is the Marketplace **Changelog** tab. It ships inside the `.vsix`; anything not written
here is invisible to someone deciding whether to install.

## [Unreleased]

### Added

- A Marketplace listing body (`extension/README.md`) and this changelog. Both are packaged into the
  `.vsix` and asserted by CI — see below for why that assertion exists.

### Fixed

- **The model dropdown offered a model that does not exist, and omitted one that does.** The list of
  values `talyvor.model` accepts in Settings was maintained by hand, separately from the list
  `Talyvor: Select AI Model` and the status bar work from. It offered `llama-3.1-70b`, which this
  extension has no profile for — no display name, no icon, no entry in the picker — and which
  Talyvor's price catalog does not price, so requests on it are billed against a fallback rate
  rather than a published one. It omitted `claude-opus-4-6`, which the picker *does* offer and
  write, so choosing Opus left a value the extension's own settings schema marked invalid. The two
  lists are now the same set, and CI fails if they diverge again.

- **The packaged extension had no listing body at all.** `vsce package` succeeds without a
  `README.md` and prints no warning, so the `.vsix` CI built on every pull request contained a
  licence, a manifest and compiled JavaScript — and nothing that would render on the Marketplace
  page. The existing packaging assertions checked the licence and the entrypoint, neither of which
  can see a missing README. Publishing that artefact would have produced a listing with a blank
  description page.

## [0.1.0]

Not yet published to the Marketplace. `0.1.0` is the version in `extension/package.json`; the
extension is installable today only from a locally built `.vsix` or the repository.
