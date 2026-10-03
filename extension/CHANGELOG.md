# Changelog

This file is the Marketplace **Changelog** tab. It ships inside the `.vsix`; anything not written
here is invisible to someone deciding whether to install.

## [0.1.0]

The first release on the Marketplace: `code --install-extension talyvor.talyvor-code`.

### Added

- **`Talyvor: Run Claude Code (metered by Lens)`.** Opens a terminal running Claude Code under the
  Talyvor Code CLI (`talyvor-code exec -- claude`), so a Claude Code session is billed by your Lens
  and attributed to the active issue. If the CLI is not on your `PATH` the command says so and links
  to its install instructions.

### Fixed

- **`Talyvor: Test Lens Connection` said "✅ Connected" when your API key was wrong.** The command
  probed `GET /healthz`, which Lens serves *unauthenticated* — the key was never put on the wire, so
  a wrong, revoked or expired key produced a green tick, and the only failure message the command
  could emit told you to check "the URL and your network", which were the two things demonstrably
  working. Every AI call then failed with no diagnostic that could say why. It now also probes
  `GET /v1/auth/me` with the key and reports the rejection as a *key* problem, naming the setting.
  The same false tick shipped in the JetBrains plugin and in `talyvor-code check`; all three are
  fixed together, against one shared table of what each HTTP status is allowed to mean
  (`testdata/credential-verdict-cases.json`), so the three cannot drift apart.
  Only an explicit **401** or **403** is treated as a verdict against the key: a Lens without that
  route, a server error or a dead socket leaves the message exactly as it was, so no working install
  is turned red by the new probe.

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
