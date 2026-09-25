# <img src="https://brand.nixos.org/internals/nixos-logomark-default-gradient-none.svg" alt="NixOS" width="24"> flake release

[![check](https://trev.zip/llc/flake-release/actions/workflows/check.yaml/badge.svg?branch=main&logo=forgejo&logoColor=%23bac2de&label=check&labelColor=%23313244)](https://trev.zip/llc/flake-release/actions?workflow=check.yaml)
[![vulnerable](https://trev.zip/llc/flake-release/actions/workflows/vulnerable.yaml/badge.svg?branch=main&logo=forgejo&logoColor=%23bac2de&label=vulnerable&labelColor=%23313244)](https://trev.zip/llc/flake-release/actions?workflow=vulnerable.yaml)
[![nixpkgs](https://img.shields.io/endpoint?url=https%3A%2F%2Fnix-shield.trev.zip%2Fbadge%3Furl%3Dhttps%253A%252F%252Ftrev.zip%252Fllc%252Fflake-release%252Fraw%252Fbranch%252Fmain%252Fflake.lock%26input%3Dnixpkgs&logoColor=%23bac2de&labelColor=%23313244&color=%235277C3)](https://nixos.org/)

Generates release artifacts for packages in a nix flake:

- `dockerTools.buildLayeredImage` & `dockerTools.streamLayeredImage` can be uploaded to a container registry
- packages have every non-empty output bundled into a `.tar.xz`, or a `.zip` on Windows; `out` contents are placed at the archive root while other split outputs retain names such as `bin/`, `dev/`, and `doc/`; runs without any releasable outputs fail without creating a release
- dynamic ELF executables in the `out` and `bin` outputs are patched with their non-glibc dependencies
- Linux packages whose `meta.mainProgram` is a script rather than a native binary can be bundled into an AppImage when explicitly enabled
- Go, Cargo, npm, PyPI, Maven, and Gradle packages can be published from package source manifests

Works with GitHub, Gitea & Forgejo

## Usage

```sh
flake-release [packages...] [--dry-run] [--bundle-appimage]
```

### Environment

| Variable                     | Description                                                                          | Example                          |
| ---------------------------- | ------------------------------------------------------------------------------------ | -------------------------------- |
| GIT_TYPE                     | Host type for release                                                                | `github` / `gitea` / `forgejo`   |
| GITHUB_REPOSITORY            | Repository to push releases, inferred from `remote.origin.url` when unset            | `spotdemo4/flake-release`        |
| GITHUB_SERVER_URL            | Server to push releases, inferred from `remote.origin.url` when unset                | `https://github.com`             |
| GITHUB_ACTOR                 | User for Gitea & Forgejo                                                             | `github-actions[bot]`            |
| GITHUB_TOKEN                 | Token used to push releases                                                          |                                  |
| TAG                          | Exact short release tag; defaults to the CI tag event or local Git tag discovery     | `packages/api/v1.2.3`            |
| REGISTRY                     | Container registry                                                                   | `ghcr.io`                        |
| REGISTRY_USERNAME            | Username for container registry                                                      | `github-actions[bot]`            |
| REGISTRY_PASSWORD            | Password for container registry                                                      |                                  |
| PUBLISH_PACKAGES             | Package kinds to publish, separated by commas or whitespace                          | `go cargo gradle maven npm pypi` |
| PACKAGE_REGISTRY_OWNER       | Package owner or namespace, defaulting to the owner from `GITHUB_REPOSITORY`         | `spotdemo4`                      |
| PACKAGE_REGISTRY_URL         | Registry URL override                                                                | `https://npm.pkg.github.com`     |
| PACKAGE_REGISTRY_USERNAME    | Registry username, defaulting to `GITHUB_ACTOR`                                      | `github-actions[bot]`            |
| PACKAGE_REGISTRY_TOKEN       | Dedicated package registry write token; required outside dry-run                     |                                  |
| DRY_RUN                      | Validate and prepare releases without registry writes or cleanup                     | `true`                           |
| DELETE_OLD_RELEASE_ARTIFACTS | Cleanup release assets and image tags: `false`, `true`, or a release retention count | `2`                              |
| BUNDLE_APPIMAGE              | Bundle eligible Linux script packages as AppImages; disabled by default              | `true`                           |

By default, packages are released as normal output archives. Enable automatic AppImage conversion with `--bundle-appimage`, `BUNDLE_APPIMAGE=true`, or the Action input below. Explicitly selected package outputs that already contain an `.AppImage` are uploaded as AppImages regardless of this setting.

### Artifact retention

`DELETE_OLD_RELEASE_ARTIFACTS` (or the Action input `delete_old_release_artifacts`) accepts:

- `false`, `0`, or unset: disable cleanup (the default).
- `true` or `1`: keep the current release's artifacts and clean up previous artifacts.
- A positive integer such as `2`: keep the current release plus the newest `N-1` older releases in the same exact tag namespace, using version order rather than API listing order.

For example, `DELETE_OLD_RELEASE_ARTIFACTS=2` when publishing `v1.3.0` retains its artifacts and those of `v1.2.0`, while deleting artifacts from `v1.1.0` and older. The count is per hosted release, not per attached file; releases without attachments still count because they may have container images. Retained releases keep all attachments and their container version/platform tags. The `latest` image tag remains protected. Legacy tags with an empty version (`v`) do not consume retention slots.

Cleanup runs only after successful publication and creation of the current release, never during dry runs. It deletes release attachments and, when publishing images, old container tags; it does not delete release records or package registry versions. Hosted releases in other namespaces or with versions equal to or newer than the current release remain untouched.

Boolean aliases `yes`/`on` and `no`/`off` are also accepted, case-insensitively. Invalid values, negative counts, and fractional counts fail before publication instead of silently disabling cleanup.

### Scoped release tags

Tags may include a namespace before the version, such as `packages/api/v1.2.3`. The complete tag identifies the hosted release and limits changelog ancestry and old-asset cleanup to that exact namespace. The namespace is also interpreted as a literal, case-sensitive repository path when generating the changelog. A scoped changelog includes only commits that change that path or its descendants relative to their first parent; similarly prefixed sibling paths are excluded, while a commit that also changes unrelated paths is still included. Unscoped tags continue to include changes from the entire repository. Go submodules also verify that the namespace matches the module path beneath the configured repository.

A namespace does not infer Nix package attributes or filter requested source manifests. For a scoped release, pass only the package attributes that belong to that namespace.

Container images append the namespace to the repository path while retaining the terminal version as the image tag. For example, `packages/api/v1.2.3` publishes architecture images such as `registry/owner/repo/packages/api:1.2.3-amd64`, the combined manifest as `registry/owner/repo/packages/api:1.2.3`, and `registry/owner/repo/packages/api:latest`. Nested namespaces remain nested repository paths, while unscoped tags continue to publish directly to `registry/owner/repo`. Manifest discovery and old-image cleanup are confined to the derived repository path.

Container registry and repository paths are normalized to lowercase, including scoped namespace components. Consequently, Git tag namespaces that differ only by case share the same container repository path.

### Package publishing

Package publishing is disabled unless `PUBLISH_PACKAGES` explicitly lists one or more of `go`, `cargo`, `gradle`, `maven`, `npm`, or `pypi`. Values may be separated by commas, spaces, or newlines. Unknown values are rejected and repeated values are deduplicated.

| Registry host | Go  | Cargo | Gradle | Maven | npm | PyPI |
| ------------- | --- | ----- | ------ | ----- | --- | ---- |
| Forgejo       | yes | yes   | yes    | yes   | yes | yes  |
| Gitea         | yes | yes   | yes    | yes   | yes | yes  |
| GitHub        | no  | no    | yes    | yes   | yes | no   |

Forgejo and Gitea use `GITHUB_SERVER_URL` by default. GitHub npm uses `https://npm.pkg.github.com` and GitHub Maven/Gradle use `https://maven.pkg.github.com/{owner}/{repo}` by default. `PACKAGE_REGISTRY_URL` overrides these defaults. GitHub npm packages must have a lowercase scoped name in the form `@owner/name`, and the scope must match `PACKAGE_REGISTRY_OWNER`.

Use a dedicated `PACKAGE_REGISTRY_TOKEN` with package write access rather than reusing `GITHUB_TOKEN`. `PACKAGE_REGISTRY_USERNAME` defaults to `GITHUB_ACTOR`; Forgejo and Gitea require it for PyPI Basic authentication. Container registry credentials remain separate under `REGISTRY_USERNAME` and `REGISTRY_PASSWORD`.

#### Source discovery

For each requested Nix package, flake-release evaluates `.#<package>.src` and looks only at that source root for the requested manifests:

| Kind   | Root manifest                        |
| ------ | ------------------------------------ |
| Go     | `go.mod`                             |
| Cargo  | `Cargo.toml`                         |
| Gradle | `build.gradle` or `build.gradle.kts` |
| Maven  | `pom.xml`                            |
| npm    | `package.json`                       |
| PyPI   | `pyproject.toml`                     |

Manifest discovery is not recursive. Sources shared by multiple Nix package attributes are published only once per package kind. Evaluated sources are copied to writable temporary staging directories before package tools run. A requested kind that is not discovered in any selected package source is an error.

#### Tools and versions

Package ecosystem tools must already be available on `PATH`:

- Go requires `go`
- Cargo requires `cargo`
- Gradle requires `gradle` or `gradlew` wrapper
- Maven requires `mvn` or `mvnw` wrapper
- npm requires `npm`
- PyPI requires `python3` with the `build` and `twine` modules

The stock Docker action does not bundle or inherit these tools from the runner, so package publishing that depends on them is unavailable in that image. Run `flake-release` directly in an environment whose `PATH` contains the selected ecosystem tools, such as a Nix shell. Missing tools are fatal instead of silently skipping publication.

Package versions are strict: Go publishes the exact release tag, including a leading `v`; Cargo, Gradle, Maven, npm, and every built PyPI artifact must match the release tag after removing one leading `v`. Existing immutable or duplicate package versions are fatal conflicts, not idempotent success; this includes an HTTP 409 response from a Go registry. `DELETE_OLD_RELEASE_ARTIFACTS` does not delete package registry versions.

`--dry-run` performs source discovery, required-tool checks, package metadata and version validation, Go archive preparation, Cargo and npm dry-runs, and PyPI build/checks without requiring registry credentials. Maven and Gradle dry-runs validate manifests and versions and generate registry authentication without publishing. It does not write to a registry or clean up old release artifacts. `DRY_RUN=true` provides the same behavior.

## Install

### Action

```yaml
- name: Release
  uses: spotdemo4/flake-release@v0.27.1
  with:
    packages: # default: all
    github_repository: # default: ${{ github.repository }}
    github_server_url: # default: ${{ github.server_url }}
    github_actor: # default: ${{ github.actor }}
    github_token: # default: ${{ github.token }}
    registry: # default: ghcr.io
    registry_username: # default: ${{ github.actor }}
    registry_password: # default: ${{ github.token }}
    publish_packages: # go, cargo, gradle, maven, npm, and/or pypi
    package_registry_owner: # default: repository owner
    package_registry_url: # default: host-specific registry
    package_registry_username: # default: ${{ github.actor }}
    package_registry_token: # dedicated package write token
    delete_old_release_artifacts: # false (default), true, or a count including current (e.g. "2")
    bundle_appimage: # default: false
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
    };
};

outputs = { flake-release, ... }: {
    devShells.x86_64-linux.default = pkgs.mkShell {
        packages = [ flake-release.packages.x86_64-linux.default ];
    };
}
```

also available from the [nix user repository](https://nur.nix-community.org/repos/trev/) as `nur.repos.trev.flake-release`

### Docker

```sh
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
  -e BUNDLE_APPIMAGE=true \
  ghcr.io/spotdemo4/flake-release:0.27.1
```

### Downloads

https://trev.zip/llc/flake-release/releases
