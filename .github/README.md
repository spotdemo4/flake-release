# <img src="https://brand.nixos.org/internals/nixos-logomark-default-gradient-none.svg" alt="NixOS" width="24"> nix flake release

[![check](https://github.com/spotdemo4/nix-flake-release/actions/workflows/check.yaml/badge.svg?branch=main)](https://github.com/spotdemo4/nix-flake-release/actions/workflows/check.yaml)
[![vulnerable](https://github.com/spotdemo4/nix-flake-release/actions/workflows/vulnerable.yaml/badge.svg?branch=main)](https://github.com/spotdemo4/nix-flake-release/actions/workflows/vulnerable.yaml)

Generates release artifacts for packages in a nix flake

## Usage

```elm
nix-flake-release [packages...]
```

## Install

### Action

```yaml
- name: Release
  uses: spotdemo4/nix-flake-release@v0.9.0
  with:
    packages: # default: all
    github_token: # default: ${{ github.token }}
    registry: # default: ghcr.io
    registry_username: # default: ${{ github.actor }}
    registry_password: # default: ${{ github.token }}
    bundle: # whether to bundle generic derivations, default: true
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
    devShells."${system}".default = pkgs.mkShell {
        packages = [
            nix-flake-release."${system}".default
        ];
    };
}
```

also available from the [nix user repository](https://nur.nix-community.org/repos/trev/) as `nur.repos.trev.nix-flake-release`

### Docker

```elm
docker run -it --rm \
  -v "$(pwd):/app" \
  -w /app \
  -v "$HOME/.ssh:/root/.ssh" \
  -e GITHUB_TOKEN=... \
  -e GITHUB_REPOSITORY=spotdemo4/nix-flake-release \
  -e REGISTRY=ghcr.io \
  -e REGISTRY_USERNAME=... \
  -e REGISTRY_PASSWORD=... \
  ghcr.io/spotdemo4/nix-flake-release:0.9.0
```

### Downloads

#### [nix-release.sh](/src/nix-release.sh) - bash script

requires [jq](https://jqlang.org/), [skopeo](https://github.com/containers/skopeo/), [gh](https://cli.github.com/) (github), [tea](https://gitea.com/gitea/tea) (gitea)

```elm
git clone https://github.com/spotdemo4/nix-flake-release &&
./nix-flake-release/src/nix-release.sh
```
