# Flatpak packaging

This directory holds the Flathub manifest for ED Colonization Reporter and the
generated Go module sources it needs to build offline.

| File | Purpose |
|------|---------|
| `ca.thegalloways.edcolreport.yaml` | The Flatpak manifest (build from source). |
| `go.mod.yml` | Generated. Lists every Go module as an archive source so the build is offline. Reconstructs `vendor/`. |
| `modules.txt` | Generated. The `vendor/modules.txt` that `go.mod.yml` copies in. |
| `generate-sources.sh` | Regenerates the two files above from `go.mod`/`go.sum`. |

The desktop entry, icon, and AppStream metainfo live one level up in
`packaging/` (shared with the AUR package) and are installed by the manifest.

## Prerequisites

```sh
flatpak install flathub org.freedesktop.Platform//25.08 org.freedesktop.Sdk//25.08 \
    org.freedesktop.Sdk.Extension.golang//25.08
```

(`flatpak-builder --install-deps-from=flathub` will also pull these in.)

## Regenerating module sources

Re-run whenever `go.mod` / `go.sum` change, then commit the result:

```sh
./packaging/flatpak/generate-sources.sh
```

## Build & test locally

The committed manifest builds from a pinned git **tag** (the Flathub form), so
the tag must exist to build it as-is. To test the current working tree instead,
temporarily swap the app's `git` source for a local `dir` source:

```yaml
    sources:
      - type: dir
        path: ../..
        skip: [.git, .flatpak-builder, dist, edcolreport, vendor]
      - go.mod.yml
```

Then build and install into the user installation:

```sh
cd packaging/flatpak
flatpak-builder --user --install --force-clean build-dir ca.thegalloways.edcolreport.yaml
flatpak run ca.thegalloways.edcolreport
```

## Journal access

Fyne uses its own file dialog (not the XDG portal), so the journal directory
must be visible inside the sandbox. The manifest grants read-only access to the
common Steam locations. For a custom Wine/Lutris prefix, grant it per-user:

```sh
flatpak override --user --filesystem="$HOME/Games/elite-dangerous:ro" ca.thegalloways.edcolreport
```

## Submitting to Flathub

Flathub builds happen in a separate `flathub/ca.thegalloways.edcolreport` repo,
not this one. Before opening the submission PR:

1. Cut a release tag (e.g. `v0.2.0`) and set both `tag:` and `commit:` on the
   git source in the manifest (Flathub requires the commit pin).
2. Bump the `-X main.version=` ldflag and the `<release>` entry in the metainfo
   to match the tag.
3. Commit a real screenshot under `docs/screenshots/` so the metainfo's
   tag-pinned image URL resolves.
4. Confirm the runtime is still the latest on Flathub (Flathub requires the
   newest stable runtime at submission time).

## Caveats verified / to verify

- **Offline build:** modules come from `go.mod.yml`; no `--share=network` in
  build-args (Flathub forbids build-time network).
- **Go version:** the build uses the Go shipped by the `golang` SDK extension
  (`GOTOOLCHAIN=local`). It must be >= the `go` directive in `go.mod`. If a
  future runtime ships an older Go, bump the runtime or lower the directive.
- **System tray:** the StatusNotifier talk-names cover the opt-in tray; some
  desktops may also need an `--own-name` rule depending on the tray host.
