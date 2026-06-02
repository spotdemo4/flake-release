# <img src="https://brand.nixos.org/internals/nixos-logomark-default-gradient-none.svg" alt="NixOS" width="24"> flake release

[![check](https://trev.zip/llc/flake-release/actions/workflows/check.yaml/badge.svg?branch=main&logo=forgejo&logoColor=%23bac2de&label=check&labelColor=%23313244)](https://trev.zip/llc/flake-release/actions?workflow=check.yaml)
[![vulnerable](https://trev.zip/llc/flake-release/actions/workflows/vulnerable.yaml/badge.svg?branch=main&logo=forgejo&logoColor=%23bac2de&label=vulnerable&labelColor=%23313244)](https://trev.zip/llc/flake-release/actions?workflow=vulnerable.yaml)

Generates release artifacts for packages in a nix flake:

- `dockerTools.buildLayeredImage` & `dockerTools.streamLayeredImage` can be uploaded to a container registry
- packages that contain only static executable binaries will be compressed & uploaded to a release directly
- others will be bundled into an AppImage

Works with GitHub, Gitea & Forgejo

## Usage

```sh
flake-release [packages...]
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
| DELETE_OLD_RELEASE_ARTIFACTS | Delete release assets and image tags from previous releases after a new release exists | `true`                         |

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
  ghcr.io/spotdemo4/flake-release:0.17.0
```

### Downloads

Release binaries are available from GitHub releases.
