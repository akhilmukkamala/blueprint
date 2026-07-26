# Installing Blueprint

Blueprint ships as a single static binary per OS/arch (cgo is required at build
time for tree-sitter, so releases are built on native runners — see
`dist/` for the release manifests). Every release publishes tarballs/zips, a
`checksums.txt`, an SBOM, and provenance on GitHub Releases.

> The GitHub repo slug (`akhilmukkamala/blueprint` below) is finalized at first
> publish; the install script and manifests read it from a variable so only one
> place changes.

## Online install

### One-line script (macOS, Linux)

```sh
curl -fsSL https://raw.githubusercontent.com/akhilmukkamala/blueprint/main/dist/install.sh | sh
```

Prefer to inspect first? Download `dist/install.sh`, read it, then `sh install.sh`.
The script detects OS/arch, downloads the latest release, **verifies sha256
against the release checksums file**, installs to `/usr/local/bin` (if writable)
or `~/.local/bin`, and prints PATH advice. It is idempotent. Overrides:
`BLUEPRINT_VERSION=1.2.3`, `BLUEPRINT_INSTALL_DIR=...`, `GITHUB_REPO=owner/repo`.

### Windows (PowerShell)

```powershell
powershell -ExecutionPolicy Bypass -File install.ps1
```

Installs to `%LOCALAPPDATA%\Programs\blueprint`, verifies sha256, and adds the
directory to your *user* PATH (no admin needed).

### Homebrew (macOS, Linux)

```sh
brew tap akhilmukkamala/blueprint
brew install blueprint
```

### Scoop (Windows)

```powershell
scoop bucket add blueprint https://github.com/akhilmukkamala/scoop-blueprint
scoop install blueprint
```

### winget (Windows)

```powershell
winget install AkhilMukkamala.Blueprint
```

(Homebrew formula, scoop manifest, and winget manifests are generated from
templates in `dist/` by `tools/relmanifests` at release time, so their sha256
pins always match `checksums.txt`.)

## Offline / air-gapped install

Blueprint is built to work with zero network access (see the network disclosure
below). To install on an air-gapped machine:

1. On a connected machine, download from the GitHub release:
   `blueprint-offline-<version>-<os>-<arch>.tar.gz` and `checksums.txt`.
2. Transfer both files (USB stick, internal artifact store, etc.).
3. On the target machine, verify the sha256:
   - macOS/Linux: `shasum -a 256 -c <(grep offline checksums.txt)` or compare
     `sha256sum <tarball>` against the `checksums.txt` line by eye.
   - Windows: `Get-FileHash -Algorithm SHA256 <file>` and compare.
4. Extract and put the binary on your PATH:
   `tar -xzf blueprint-offline-<version>-<os>-<arch>.tar.gz && mv blueprint ~/.local/bin/`
5. In each repo: `blueprint init --offline` (templates are embedded in the
   binary; nothing is fetched).

Upgrading air-gapped: transfer the newer offline tarball the same way, verify,
swap the binary, then run `blueprint upgrade` in each repo (the repo-file
migration is local; only the *release fetch* variant of upgrade needs network).

## Per-OS notes

- **Windows (first-class):** Blueprint creates **no symlinks anywhere** — safe
  on filesystems and policies where symlinks need elevation. Paths are
  MAX_PATH-aware. `install.ps1`, scoop, and winget all install per-user and
  manage the *user* PATH; no admin rights required. Open a new terminal after
  install so PATH changes take effect.
- **macOS:** binaries are unsigned in early releases; if Gatekeeper complains,
  `xattr -d com.apple.quarantine $(command -v blueprint)`. Apple Silicon and
  Intel builds are separate assets; the script picks the right one.
- **Linux:** glibc and musl users both use the static release binary. If
  `~/.local/bin` is not on PATH, the installer prints the export line to add.

## Versioning policy

Blueprint follows **semver** (`MAJOR.MINOR.PATCH`):

- **PATCH** — fixes only; repo files untouched.
- **MINOR** — new features; `blueprint upgrade` migrates `[tool]` files and
  managed regions, never `[user]` files.
- **MAJOR** — breaking changes to on-disk formats or CLI; upgrade prints a
  migration plan and requires explicit confirmation.

Pinned upgrades are first-class: install a specific version with
`BLUEPRINT_VERSION=x.y.z` (script), `scoop install blueprint@x.y.z`, or by
downloading the versioned asset directly. Teams should pin and roll forward
deliberately rather than tracking latest.

## Upgrade

Two layers, upgraded in this order:

1. **Binary:** re-run the install script (idempotent; replaces the binary), or
   `brew upgrade blueprint` / `scoop update blueprint` / `winget upgrade`.
   Prior binary versions are retained by the tool for rollback.
2. **Repo files:** `blueprint upgrade` — a three-way merge honoring ownership
   tiers (`[tool]` replaced, `[user]` untouched, `[mixed]` merged in managed
   regions). Use `--diff` / `--dry-run` first; it refuses on a dirty tree and
   makes exactly one commit.

## Rollback

- **Repo files:** `blueprint upgrade --rollback` (or `git revert` the single
  upgrade commit — upgrade always makes exactly one).
- **Binary:** prior binary versions are retained at install time; swap the
  previous binary back onto PATH (or `BLUEPRINT_VERSION=<old> sh install.sh`).

## Uninstall

1. In each repo: `blueprint uninstall` — removes `[tool]` files and managed
   regions; **`[user]` specs and knowledge stay** (the repo keeps its memory).
   `--purge` removes everything, with confirmation.
2. Remove the binary: `rm $(command -v blueprint)` (script installs), or
   `brew uninstall blueprint` / `scoop uninstall blueprint` /
   `winget uninstall AkhilMukkamala.Blueprint`.

## Network disclosure (AC-12)

After install, Blueprint makes **zero network calls** except these documented,
opt-in commands — enforced by the `internal/netaudit` network-audit test:

| Command | Network use |
|---|---|
| `blueprint verify` (full tier) | model-checker API call |
| `blueprint dream` | batch model API, only via the user-configured external `[dream]` command (unconfigured = fully offline, deterministic proposals) |
| `blueprint upgrade` | release fetch (an offline variant exists) |
| Tier-2/3 retrieval backends | only when the user configures them |

Everything else works fully air-gapped. Blueprint ships **no telemetry**.
