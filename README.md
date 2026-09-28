# <img src="https://brand.nixos.org/internals/nixos-logomark-default-gradient-none.svg" alt="NixOS" width="24"> flake release

[![check](https://trev.zip/llc/flake-release/actions/workflows/check.yaml/badge.svg?branch=main&logo=forgejo&logoColor=%23bac2de&label=check&labelColor=%23313244)](https://trev.zip/llc/flake-release/actions?workflow=check.yaml)
[![vulnerable](https://trev.zip/llc/flake-release/actions/workflows/vulnerable.yaml/badge.svg?branch=main&logo=forgejo&logoColor=%23bac2de&label=vulnerable&labelColor=%23313244)](https://trev.zip/llc/flake-release/actions?workflow=vulnerable.yaml)
[![nixpkgs](https://nix-shield.trev.zip/?url=https://trev.zip/llc/flake-release/raw/branch/main/flake.lock&input=nixpkgs&logoColor=%23bac2de&labelColor=%23313244&color=%235277C3)](https://nixos.org/)
[![go](<https://img.shields.io/badge/dynamic/regex?url=https://trev.zip/llc/flake-release/raw/branch/main/go.mod&search=toolchain%20go(.*)&replace=%241&logo=go&logoColor=%23bac2de&label=version&labelColor=%23313244&color=%2300ADD8>)](https://go.dev/doc/devel/release)

Generates release artifacts for packages in a nix flake:

- `dockerTools.buildLayeredImage` & `dockerTools.streamLayeredImage` can be uploaded to a container registry
- packages have every non-empty output bundled into a `.tar.xz`, or a `.zip` on Windows
- `out` contents are placed at the archive root, while other split outputs keep their names, such as `bin/`, `dev/`, and `doc/`
- dynamic ELF executables in the `out` and `bin` outputs are patched with their non-glibc dependencies
- shell scripts in the `out` and `bin` outputs are patched to find bundled files relative to themselves and other nix store programs through `PATH`
- Linux packages whose `meta.mainProgram` is a script rather than a native binary can be bundled into an AppImage when explicitly enabled
- Go, Cargo, npm, PyPI, Maven, and Gradle packages can be published from package source manifests

Runs that produce no releasable outputs fail without creating a release. Works with GitHub, Gitea & Forgejo

## Usage

```sh
flake-release [packages...] [--dry-run] [--bundle-appimage]
```

### Environment

| Variable                     | Description                                                                          | Default                                        | Example                        |
| ---------------------------- | ------------------------------------------------------------------------------------ | ---------------------------------------------- | ------------------------------ |
| PACKAGES                     | Packages to release, separated by spaces or newlines; added to any arguments         | all packages for the current system            | `default api`                  |
| GIT_TYPE                     | Host type for release                                                                | detected from the CI environment or origin URL | `github` / `gitea` / `forgejo` |
| GITHUB_REPOSITORY            | Repository to push releases                                                          | from `remote.origin.url`                       | `spotdemo4/flake-release`      |
| GITHUB_SERVER_URL            | Server to push releases                                                              | from `remote.origin.url`                       | `https://github.com`           |
| GITHUB_ACTOR                 | User for Gitea & Forgejo                                                             | Git user name                                  | `github-actions[bot]`          |
| GITHUB_TOKEN                 | Token used to push releases                                                          |                                                |                                |
| TAG                          | Exact short release tag                                                              | CI tag event, or latest Git tag                | `packages/api/v1.2.3`          |
| CONTAINER_REGISTRY           | Container registry                                                                   | `ghcr.io` on GitHub                            | `ghcr.io`                      |
| CONTAINER_REGISTRY_USERNAME  | Username for container registry                                                      | Git user name                                  | `github-actions[bot]`          |
| CONTAINER_REGISTRY_PASSWORD  | Password for container registry                                                      | `GITHUB_TOKEN`                                 |                                |
| PACKAGE_REGISTRY_OWNER       | Package owner or namespace                                                           | owner from `GITHUB_REPOSITORY`                 | `spotdemo4`                    |
| PACKAGE_REGISTRY_URL         | Registry URL override                                                                | host-specific                                  | `https://npm.pkg.github.com`   |
| PACKAGE_REGISTRY_USERNAME    | Registry username                                                                    | `GITHUB_ACTOR`                                 | `github-actions[bot]`          |
| PACKAGE_REGISTRY_TOKEN       | Dedicated package registry write token; enables package publishing                   |                                                |                                |
| DRY_RUN                      | Validate and prepare releases without publishing or cleanup                          | `false`                                        | `true`                         |
| DELETE_OLD_RELEASE_ARTIFACTS | Cleanup release assets and image tags: `false`, `true`, or a release retention count | `false`                                        | `2`                            |
| BUNDLE_APPIMAGE              | Bundle eligible Linux script packages as AppImages                                   | `false`                                        | `true`                         |

By default, packages are released as normal output archives. Enable automatic AppImage conversion with `--bundle-appimage`, `BUNDLE_APPIMAGE=true`, or the Action input below. Explicitly selected package outputs that already contain an `.AppImage` are uploaded as AppImages regardless of this setting.

### Artifact retention

`DELETE_OLD_RELEASE_ARTIFACTS` (or the Action input `delete_old_release_artifacts`) accepts:

- `false`, `0`, `no`, `off`, or unset: disable cleanup (the default).
- `true`, `1`, `yes`, or `on`: keep the current release's artifacts and clean up previous artifacts.
- A positive integer `N`: keep the current release plus the newest `N-1` older releases in the same tag namespace, ordered by version.

Values are case-insensitive. Invalid values, negative counts, and fractional counts fail before publication.

For example, `DELETE_OLD_RELEASE_ARTIFACTS=2` when publishing `v1.3.0` keeps the artifacts of `v1.3.0` and `v1.2.0` and deletes those of `v1.1.0` and older. The count is per release, not per file, and includes releases without attachments since they may still have container images. Retained releases keep all attachments and container tags, the `latest` image tag is never deleted, and legacy tags with an empty version (`v`) don't count toward retention.

Cleanup runs only after the current release is published successfully, and never during dry runs. It deletes release attachments and old container tags, but not release records or package registry versions. Releases in other namespaces, or with versions equal to or newer than the current release, are left untouched.

### Scoped release tags

Tags may include a namespace before the version, such as `packages/api/v1.2.3`. The full tag identifies the release, and changelog history and old-artifact cleanup are limited to that namespace.

The namespace is treated as a case-sensitive repository path: a scoped changelog only includes commits that touch that path (compared to their first parent). Unscoped tags include changes from the entire repository. For Go modules, the namespace must match the module path within the repository.

A namespace does not select Nix packages or filter source manifests, so for a scoped release pass only the packages that belong to that namespace.

Container images append the namespace to the repository path and use the version as the image tag. For example, `packages/api/v1.2.3` publishes `registry/owner/repo/packages/api:1.2.3-amd64` (per architecture), `registry/owner/repo/packages/api:1.2.3` (combined manifest), and `registry/owner/repo/packages/api:latest`. Unscoped tags publish directly to `registry/owner/repo`. Container paths are lowercased, so namespaces that differ only by case share a container repository.

### Package publishing

Package publishing is enabled by setting `PACKAGE_REGISTRY_TOKEN`. Every package manifest discovered in the selected sources is then published, limited to the kinds the registry host supports:

| Registry host | Go  | Cargo | Gradle | Maven | npm | PyPI |
| ------------- | --- | ----- | ------ | ----- | --- | ---- |
| Forgejo       | yes | yes   | yes    | yes   | yes | yes  |
| Gitea         | yes | yes   | yes    | yes   | yes | yes  |
| GitHub        | no  | no    | yes    | yes   | yes | no   |

Forgejo and Gitea use `GITHUB_SERVER_URL` by default. GitHub npm uses `https://npm.pkg.github.com` and GitHub Maven/Gradle use `https://maven.pkg.github.com/{owner}/{repo}` by default. `PACKAGE_REGISTRY_URL` overrides these defaults. GitHub npm packages must have a lowercase scoped name in the form `@owner/name`, and the scope must match `PACKAGE_REGISTRY_OWNER`.

Use a dedicated `PACKAGE_REGISTRY_TOKEN` with package write access rather than reusing `GITHUB_TOKEN`. `PACKAGE_REGISTRY_USERNAME` defaults to `GITHUB_ACTOR`; Forgejo and Gitea require it for PyPI Basic authentication. Container registry credentials remain separate under `CONTAINER_REGISTRY_USERNAME` and `CONTAINER_REGISTRY_PASSWORD`.

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

Manifest discovery is not recursive. Sources shared by multiple Nix package attributes are published only once per package kind. Evaluated sources are copied to writable temporary staging directories before package tools run.

Manifests that opt out of publishing are skipped: an npm `package.json` with `"private": true`, and a `Cargo.toml` that sets `publish = false` or only defines a workspace. Any other discovered manifest must be publishable and match the release version, so opt out of publishing packages that are not meant for a registry. A Gradle build and a Maven `pom.xml` with the same coordinates in one source are rejected as duplicates.

#### Tools and versions

Package ecosystem tools must already be available on `PATH`:

- Go requires `go`
- Cargo requires `cargo`
- Gradle requires `gradle` or `gradlew` wrapper
- Maven requires `mvn` or `mvnw` wrapper
- npm requires `npm`; when `package-lock.json` exists, `npm ci` installs dependencies before publishing
- PyPI requires `python3` with the `build` and `twine` modules

The stock Docker action does not bundle or inherit these tools from the runner, so package publishing that depends on them is unavailable in that image. Run `flake-release` directly in an environment whose `PATH` contains the selected ecosystem tools, such as a Nix shell. Missing tools are fatal instead of silently skipping publication.

Package versions are strict: Go publishes the exact release tag, including a leading `v`; Cargo, Gradle, Maven, npm, and every built PyPI artifact must match the release tag after removing one leading `v`. Existing immutable or duplicate package versions are fatal conflicts, not idempotent success; this includes an HTTP 409 response from a Go registry. `DELETE_OLD_RELEASE_ARTIFACTS` does not delete package registry versions.

With `--dry-run` or `DRY_RUN=true`, nothing is written to a registry and old artifacts aren't cleaned up. When `PACKAGE_REGISTRY_TOKEN` is set, a dry run still does the following, without using the token:

- discovers sources and checks for required tools
- validates package metadata and versions
- prepares Go archives
- runs `cargo publish --dry-run` and `npm publish --dry-run`
- builds and checks PyPI distributions
- generates Maven and Gradle registry authentication

## Install

### Action

```yaml
- name: Release
  uses: spotdemo4/flake-release@v0.29.0
  with:
    packages: # default: all
    git_type: # default: detected
    github_repository: # default: ${{ github.repository }}
    github_server_url: # default: ${{ github.server_url }}
    github_actor: # default: ${{ github.actor }}
    github_token: # default: ${{ github.token }}
    container_registry: # default: ghcr.io
    container_registry_username: # default: ${{ github.actor }}
    container_registry_password: # default: ${{ github.token }}
    package_registry_owner: # default: repository owner
    package_registry_url: # default: host-specific registry
    package_registry_username: # default: ${{ github.actor }}
    package_registry_token: # dedicated package write token; enables package publishing
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

outputs = { nixpkgs, flake-release, ... }: {
    devShells.x86_64-linux.default = nixpkgs.legacyPackages.x86_64-linux.mkShell {
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
  -e CONTAINER_REGISTRY=... \
  -e CONTAINER_REGISTRY_USERNAME=... \
  -e CONTAINER_REGISTRY_PASSWORD=... \
  -e PACKAGE_REGISTRY_TOKEN=... \
  -e BUNDLE_APPIMAGE=true \
  ghcr.io/spotdemo4/flake-release:0.29.0
```

### Downloads

https://trev.zip/llc/flake-release/releases
