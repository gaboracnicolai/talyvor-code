#!/usr/bin/env bash
# Assert that a built .vsix carries the things a Marketplace LISTING is made of.
#
# ⚠ WHY THIS EXISTS. `vsce package` succeeds with no README.md and prints NO warning — measured,
# not assumed: packaging talyvor-code at 2124b47 produced a 60-file .vsix whose entries were a
# manifest, LICENSE.txt, package.json and out/src/**.js, with zero README entries and a silent
# exit 0. On the Marketplace the README *is* the extension's description page, so that artefact
# would have published a listing with a blank body. The two assertions that already existed here
# check the licence and the entrypoint; neither can see a missing README.
#
# ⚠ IT READS THE ARCHIVE, NEVER THE WORKING TREE — and the control that proves that had to be
# rebuilt, because the obvious one does not work. Adding `README.md` to .vscodeignore does NOT
# drop it from the .vsix: vsce collects the readme as a manifest asset and force-includes it
# (measured — ignored on disk, still `extension/readme.md` in the package). So .vscodeignore is
# not the way tree and archive diverge. `vsce package --readme-path <other>` IS: packaged with a
# decoy body while the on-disk README.md still named the id, a working-tree reader would have
# been green and this guard failed. That is why every assertion below extracts from the .vsix.
#
# ⚠ THE ENTRY NAMES ARE LOWERCASE, AND THAT WAS MEASURED RATHER THAN GUESSED. vsce writes
# `extension/readme.md` and `extension/changelog.md`, not the on-disk `README.md`/`CHANGELOG.md` —
# the same renaming that turns LICENSE into LICENSE.txt above. A case-sensitive grep for the
# on-disk name would be red forever with the file present.
set -euo pipefail

vsix="${1:?usage: check-vsix-listing.sh <path-to-.vsix> <path-to-package.json>}"
pkg="${2:?usage: check-vsix-listing.sh <path-to-.vsix> <path-to-package.json>}"

fail() { echo "::error::$*"; exit 1; }

[ -f "$vsix" ] || fail "no .vsix at $vsix"

entries="$(unzip -Z1 "$vsix")"

# 1. The description page. Without it the Marketplace listing body is empty.
grep -qx 'extension/readme.md' <<<"$entries" \
  || fail ".vsix has no extension/readme.md — the Marketplace listing would render an empty description page"

# 2. The changelog tab. A listing that cannot say what changed is a listing nobody can audit.
grep -qx 'extension/changelog.md' <<<"$entries" \
  || fail ".vsix has no extension/changelog.md — the Marketplace Changelog tab would be empty"

# 3. The install command must name the id this package actually publishes under. The README's
#    install command 404'd for everyone once already; an id that drifts from publisher+name is
#    exactly how that happens again. Derived from the manifest, never hardcoded here.
# `require(path)` resolves a bare relative name as a MODULE, not a file — `require('package.json')`
# died with MODULE_NOT_FOUND while `./package.json` worked. Resolve to absolute first so the guard
# behaves the same however CI spells the argument.
id="$(node -e "const p=require(require('path').resolve(process.argv[1])); if(!p.publisher||!p.name){console.error('::error::package.json is missing publisher or name'); process.exit(1)} process.stdout.write(p.publisher+'.'+p.name)" "$pkg")"
unzip -p "$vsix" 'extension/readme.md' | grep -qF "$id" \
  || fail "the packaged readme never names the extension id '$id' — its install command cannot be correct"

# 4. …and nothing that is only a build tool. .vscodeignore is an EXCLUDE list, so a new
#    top-level directory ships by default — measured: this very script was packaged into the
#    .vsix until `scripts/**` was added to it.
if grep -q '^extension/scripts/' <<<"$entries"; then
  fail ".vsix ships build scripts under extension/scripts/ — add the directory to .vscodeignore"
fi

# 5. The listing leads with the agent wallet (B28.13). The Marketplace shows the manifest's
#    description in search results and the readme's first paragraph at the top of the page, so both
#    are read from the archive: the packaged manifest, and the readme's text up to its first blank
#    line after the `# ` title (the logo sits above the title, B29.20).
manifest="$(unzip -p "$vsix" 'extension/package.json')"
node -e "process.exit(/wallet/i.test(JSON.parse(process.argv[1]).description||'')?0:1)" "$manifest" \
  || fail "the packaged manifest's description never mentions the agent wallet — the Marketplace search result would not say what the extension is"
first_para="$(unzip -p "$vsix" 'extension/readme.md' | awk '!t && /^# / {t=1; next} t && NF==0 && seen {done=1} t && !done && NF>0 {seen=1; print}')"
grep -qi 'agent wallet' <<<"$first_para" \
  || fail "the packaged readme's first paragraph never mentions the agent wallet"

# 6. The listing carries the brand (B29.20): the Talyvor app icon at 128×128, inside the archive the
#    manifest points at, on an Obsidian banner.
icon="$(node -e "process.stdout.write(JSON.parse(process.argv[1]).icon||'')" "$manifest")"
[ -n "$icon" ] || fail "the packaged manifest has no icon — the Marketplace and the Extensions view show a blank tile"
grep -qx "extension/$icon" <<<"$entries" || fail "the manifest's icon $icon is not in the .vsix"
dims="$(unzip -p "$vsix" "extension/$icon" | node -e "const b=require('fs').readFileSync(0); process.stdout.write(b.toString('latin1',1,4)==='PNG' ? b.readUInt32BE(16)+'x'+b.readUInt32BE(20) : 'not a PNG')")"
[ "$dims" = "128x128" ] || fail "the icon is $dims, not the 128x128 PNG the Marketplace asks for"
node -e "const b=JSON.parse(process.argv[1]).galleryBanner||{}; process.exit(b.color==='#060A12'&&b.theme==='dark'?0:1)" "$manifest" \
  || fail "the packaged manifest's galleryBanner is not Obsidian #060A12 on the dark theme"

echo "vsix listing ok: readme.md, changelog.md, no build scripts, the readme names '$id', the listing leads with the agent wallet, and it carries the brand icon and banner"
