# <img src="https://brand.nixos.org/internals/nixos-logomark-default-gradient-none.svg" alt="NixOS" width="24"> nix flake release

[![check](https://github.com/spotdemo4/nix-flake-release/actions/workflows/check.yaml/badge.svg?branch=main)](https://github.com/spotdemo4/nix-flake-release/actions/workflows/check.yaml)
[![vulnerable](https://github.com/spotdemo4/nix-flake-release/actions/workflows/vulnerable.yaml/badge.svg?branch=main)](https://github.com/spotdemo4/nix-flake-release/actions/workflows/vulnerable.yaml)

Generates release artifacts for packages in a nix flake

## Usage

```elm
nix-flake-release [packages...]
```

### Environment

| Variable          | Description                     | Example                                                                                                                                                               |
| ----------------- | ------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| GITHUB_TYPE       | Host type for release           | `github` / `gitea` / `forgejo`                                                                                                                                        |
| GITHUB_REPOSITORY | Repository to push releases     | `spotdemo4/nix-flake-release`                                                                                                                                         |
| GITHUB_SERVER_URL | Server to push releases         | `https://github.com`                                                                                                                                                  |
| GITHUB_ACTOR      | User for Gitea & Forgejo        |                                                                                                                                                                       |
| GITHUB_TOKEN      | Token used to push releases     |                                                                                                                                                                       |
| REGISTRY          | Container registry              | `ghcr.io`                                                                                                                                                             |
| REGISTRY_USERNAME | Username for container registry |                                                                                                                                                                       |
| REGISTRY_PASSWORD | Password for container registry |                                                                                                                                                                       |
| BUNDLE            | Type of bundle to create        | [`appimage`](https://github.com/ralismark/nix-appimage) / [`arx`](https://github.com/nix-community/nix-bundle) / [`portable`](https://github.com/DavHau/nix-portable) |

## Install

### Action

```yaml
- name: Release
  uses: spotdemo4/nix-flake-release@v0.10.1
  with:
    packages: # default: all
    github_token: # default: ${{ github.token }}
    registry: # default: ghcr.io
    registry_username: # default: ${{ github.actor }}
    registry_password: # default: ${{ github.token }}
    bundle: # default: null
```

### Nix

```elm
nix run github:spotdemo4/nix-flake-release
```

#### Flake

```nix
inputs = {
    nix-flake-release = {
        url = "github:spotdemo4/nix-flake-release";
        inputs.nixpkgs.follows = "nixpkgs";
    };
};

outputs = { nix-flake-release, ... }: {
    devShells.x86_64-linux.default = pkgs.mkShell {
        packages = [
            nix-flake-release.packages.x86_64-linux.default
        ];
    };
}
```

also available from the [nix user repository](https://nur.nix-community.org/repos/trev/) as `nur.repos.trev.nix-flake-release`

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
  -e BUNDLE=... \
  ghcr.io/spotdemo4/nix-flake-release:0.10.1
```

### Downloads

#### [nix-release.sh](/src/nix-release.sh) - bash script

requires [jq](https://jqlang.org/), [skopeo](https://github.com/containers/skopeo/), [manifest-tool](https://github.com/estesp/manifest-tool), [gh](https://cli.github.com/) (github), [tea](https://gitea.com/gitea/tea) (gitea), [fj](https://codeberg.org/forgejo-contrib/forgejo-cli) (forgejo)

```elm
git clone https://github.com/spotdemo4/nix-flake-release &&
./nix-flake-release/src/nix-release.sh
```
