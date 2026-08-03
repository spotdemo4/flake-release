# <img src="https://brand.nixos.org/internals/nixos-logomark-default-gradient-none.svg" alt="NixOS" width="24"> flake release

[![check](https://trev.zip/llc/flake-release/actions/workflows/check.yaml/badge.svg?branch=main&logo=forgejo&logoColor=%23bac2de&label=check&labelColor=%23313244)](https://trev.zip/llc/flake-release/actions?workflow=check.yaml)
[![vulnerable](https://trev.zip/llc/flake-release/actions/workflows/vulnerable.yaml/badge.svg?branch=main&logo=forgejo&logoColor=%23bac2de&label=vulnerable&labelColor=%23313244)](https://trev.zip/llc/flake-release/actions?workflow=vulnerable.yaml)

Generates release artifacts for packages in a nix flake:

- `dockerTools.buildLayeredImage` & `dockerTools.streamLayeredImage` can be uploaded to a container registry
- packages have every non-empty output bundled into a `.tar.xz`, or a `.zip` on Windows; `out` contents are placed at the archive root while other split outputs retain names such as `bin/`, `dev/`, and `doc/`; runs without any releasable outputs fail without creating a release
- dynamic ELF executables in the `out` and `bin` outputs are patched with their non-glibc dependencies
- Linux packages whose `meta.mainProgram` is a script rather than a native binary are bundled into an AppImage
- Go, Cargo, npm, and PyPI packages can be published from package source manifests

Works with GitHub, Gitea & Forgejo

## Usage

```sh
flake-release [packages...] [--dry-run]
```

### Environment

| Variable                     | Description                                                                            | Example                        |
| ---------------------------- | -------------------------------------------------------------------------------------- | ------------------------------ |
| GIT_TYPE                     | Host type for release                                                                  | `github` / `gitea` / `forgejo` |
| GITHUB_REPOSITORY            | Repository to push releases, inferred from `remote.origin.url` when unset              | `spotdemo4/flake-release`      |
| GITHUB_SERVER_URL            | Server to push releases, inferred from `remote.origin.url` when unset                  | `https://github.com`           |
| GITHUB_ACTOR                 | User for Gitea & Forgejo                                                               | `github-actions[bot]`          |
| GITHUB_TOKEN                 | Token used to push releases                                                            |                                |
| REGISTRY                     | Container registry                                                                     | `ghcr.io`                      |
| REGISTRY_USERNAME            | Username for container registry                                                        | `github-actions[bot]`          |
| REGISTRY_PASSWORD            | Password for container registry                                                        |                                |
| PUBLISH_PACKAGES             | Package kinds to publish, separated by commas or whitespace                            | `go cargo npm pypi`            |
| PACKAGE_REGISTRY_OWNER       | Package owner or namespace, defaulting to the owner from `GITHUB_REPOSITORY`           | `spotdemo4`                    |
| PACKAGE_REGISTRY_URL         | Registry URL override                                                                  | `https://npm.pkg.github.com`   |
| PACKAGE_REGISTRY_USERNAME    | Registry username, defaulting to `GITHUB_ACTOR`                                        | `github-actions[bot]`          |
| PACKAGE_REGISTRY_TOKEN       | Dedicated package registry write token; required outside dry-run                       |                                |
| DRY_RUN                      | Validate and prepare releases without registry writes or cleanup                       | `true`                         |
| DELETE_OLD_RELEASE_ARTIFACTS | Delete release assets and image tags from previous releases after a new release exists | `true`                         |

### Package publishing

Package publishing is disabled unless `PUBLISH_PACKAGES` explicitly lists one or more of `go`, `cargo`, `npm`, or `pypi`. Values may be separated by commas, spaces, or newlines. Unknown values are rejected and repeated values are deduplicated.

| Registry host | Go  | Cargo | npm | PyPI |
| ------------- | --- | ----- | --- | ---- |
| Forgejo       | yes | yes   | yes | yes  |
| Gitea         | yes | yes   | yes | yes  |
| GitHub        | no  | no    | yes | no   |

Forgejo and Gitea use `GITHUB_SERVER_URL` by default. GitHub npm uses `https://npm.pkg.github.com` by default. `PACKAGE_REGISTRY_URL` overrides these defaults. GitHub npm packages must have a lowercase scoped name in the form `@owner/name`, and the scope must match `PACKAGE_REGISTRY_OWNER`.

Use a dedicated `PACKAGE_REGISTRY_TOKEN` with package write access rather than reusing `GITHUB_TOKEN`. `PACKAGE_REGISTRY_USERNAME` defaults to `GITHUB_ACTOR`; Forgejo and Gitea require it for PyPI Basic authentication. Container registry credentials remain separate under `REGISTRY_USERNAME` and `REGISTRY_PASSWORD`.

#### Source discovery

For each requested Nix package, flake-release evaluates `.#<package>.src` and looks only at that source root for the requested manifests:

| Kind  | Root manifest    |
| ----- | ---------------- |
| Go    | `go.mod`         |
| Cargo | `Cargo.toml`     |
| npm   | `package.json`   |
| PyPI  | `pyproject.toml` |

Manifest discovery is not recursive. Sources shared by multiple Nix package attributes are published only once per package kind. Evaluated sources are copied to writable temporary staging directories before package tools run. A requested kind that is not discovered in any selected package source is an error.

#### Tools and versions

Package ecosystem tools must already be available on `PATH`:

- Go requires `go`
- Cargo requires `cargo`
- npm requires `npm`
- PyPI requires `python3` with the `build` and `twine` modules

The stock Docker action does not bundle or inherit these tools from the runner, so package publishing that depends on them is unavailable in that image. Run `flake-release` directly in an environment whose `PATH` contains the selected ecosystem tools, such as a Nix shell. Missing tools are fatal instead of silently skipping publication.

Package versions are strict: Go publishes the exact release tag, including a leading `v`; Cargo, npm, and every built PyPI artifact must match the release tag after removing one leading `v`. Existing immutable or duplicate package versions are fatal conflicts, not idempotent success; this includes an HTTP 409 response from a Go registry. `DELETE_OLD_RELEASE_ARTIFACTS` does not delete package registry versions.

`--dry-run` performs source discovery, required-tool checks, package metadata and version validation, Go archive preparation, Cargo and npm dry-runs, and PyPI build/checks without requiring registry credentials. It does not write to a registry or clean up old release artifacts. `DRY_RUN=true` provides the same behavior.

## Install

### Action

```yaml
- name: Release
  uses: spotdemo4/flake-release@v0.17.0
  with:
    packages: # default: all
    github_repository: # default: ${{ github.repository }}
    github_server_url: # default: ${{ github.server_url }}
    github_actor: # default: ${{ github.actor }}
    github_token: # default: ${{ github.token }}
    registry: # default: ghcr.io
    registry_username: # default: ${{ github.actor }}
    registry_password: # default: ${{ github.token }}
    publish_packages: # go, cargo, npm, and/or pypi
    package_registry_owner: # default: repository owner
    package_registry_url: # default: host-specific registry
    package_registry_username: # default: ${{ github.actor }}
    package_registry_token: # dedicated package write token
    delete_old_release_artifacts: # default: false
```

### Nix

```sh
nix run github:spotdemo4/flake-release
```

#### Flake

```nix
inputs = {
    flake-release = {
        url = "github:spotdemo4/flake-release";
        inputs.nixpkgs.follows = "nixpkgs";
    };
};

outputs = { flake-release, ... }: {
    devShells.x86_64-linux.default = pkgs.mkShell {
        packages = [
            flake-release.packages.x86_64-linux.default
        ];
    };
}
```

also available from the [nix user repository](https://nur.nix-community.org/repos/trev/) as `nur.repos.trev.flake-release`

### Docker

```elm
docker run -it \
  -v "$(pwd):/app" \
  -w /app \
  -v "$HOME/.ssh:/root/.ssh" \
  -e GITHUB_TOKEN=... \
  -e GITHUB_REPOSITORY=... \
  -e REGISTRY=... \
  -e REGISTRY_USERNAME=... \
  -e REGISTRY_PASSWORD=... \
  -e PUBLISH_PACKAGES=... \
  -e PACKAGE_REGISTRY_TOKEN=... \
  ghcr.io/spotdemo4/flake-release:0.17.0
```

### Downloads

Release binaries are available from GitHub releases.
