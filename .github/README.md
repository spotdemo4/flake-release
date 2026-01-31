# <img src="https://brand.nixos.org/internals/nixos-logomark-default-gradient-none.svg" alt="NixOS" width="24"> flake release

[![check](https://img.shields.io/github/actions/workflow/status/spotdemo4/flake-release/check.yaml?branch=main&logo=github&logoColor=%23bac2de&label=check&labelColor=%23313244)](https://github.com/spotdemo4/flake-release/actions/workflows/check.yaml)
[![vulnerable](https://img.shields.io/github/actions/workflow/status/spotdemo4/flake-release/vulnerable.yaml?branch=main&logo=github&logoColor=%23bac2de&label=vulnerable&labelColor=%23313244)](https://github.com/spotdemo4/flake-release/actions/workflows/vulnerable.yaml)

Generates release artifacts for packages in a nix flake:

- `dockerTools.buildLayeredImage` & `dockerTools.streamLayeredImage` will be uploaded to a container registry
- packages that contain only executable binaries will be compressed & uploaded to a release directly
- packages that depend on nix store paths can be bundled into an AppImage (`appimage`), Arx tarball (`arx`), or bundled with a portable nix binary (`portable`)

Works with GitHub, Gitea & Forgejo

## Usage

```elm
flake-release [packages...]
```

### Environment

| Variable          | Description                     | Example                                                                                                                                                               |
| ----------------- | ------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| GITHUB_TYPE       | Host type for release           | `github` / `gitea` / `forgejo`                                                                                                                                        |
| GITHUB_REPOSITORY | Repository to push releases     | `spotdemo4/flake-release`                                                                                                                                             |
| GITHUB_SERVER_URL | Server to push releases         | `https://github.com`                                                                                                                                                  |
| GITHUB_ACTOR      | User for Gitea & Forgejo        | `github-actions[bot]`                                                                                                                                                 |
| GITHUB_TOKEN      | Token used to push releases     |                                                                                                                                                                       |
| REGISTRY          | Container registry              | `ghcr.io`                                                                                                                                                             |
| REGISTRY_USERNAME | Username for container registry | `github-actions[bot]`                                                                                                                                                 |
| REGISTRY_PASSWORD | Password for container registry |                                                                                                                                                                       |
| BUNDLE            | Type of bundle to create        | [`appimage`](https://github.com/ralismark/nix-appimage) / [`arx`](https://github.com/nix-community/nix-bundle) / [`portable`](https://github.com/DavHau/nix-portable) |

## Install

### Action

```yaml
- name: Release
  uses: spotdemo4/flake-release@v0.11.1
  with:
    packages: # default: all
    github_repository: # default: ${{ github.repository }}
    github_server_url: # default: ${{ github.server_url }}
    github_actor: # default: ${{ github.actor }}
    github_token: # default: ${{ github.token }}
    registry: # default: ghcr.io
    registry_username: # default: ${{ github.actor }}
    registry_password: # default: ${{ github.token }}
    bundle: # default: null
```

### Nix

```elm
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
  -e BUNDLE=... \
  ghcr.io/spotdemo4/flake-release:0.11.1
```

### Downloads

#### [flake-release.sh](/src) - bash script

requires [jq](https://jqlang.org/), [skopeo](https://github.com/containers/skopeo/), [manifest-tool](https://github.com/estesp/manifest-tool), [gh](https://cli.github.com/) (github), [tea](https://gitea.com/gitea/tea) (gitea), [fj](https://codeberg.org/forgejo-contrib/forgejo-cli) (forgejo)

```elm
git clone https://github.com/spotdemo4/flake-release &&
./flake-release/src/flake-release.sh
```
