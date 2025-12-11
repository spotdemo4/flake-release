# nix flake release

![check](https://github.com/spotdemo4/nix-flake-release/actions/workflows/check.yaml/badge.svg?branch=main)
![vulnerable](https://github.com/spotdemo4/nix-flake-release/actions/workflows/vulnerable.yaml/badge.svg?branch=main)

Generates release artifacts for a nix flake

## Usage

```elm
release [packages...]
```

## Install

### Action

```yaml
- name: Release
  uses: spotdemo4/nix-flake-release@v0.4.5
  with:
    github_token: # default: ${{ github.token }}
    registry: # default: ghcr.io
    registry_username: # default: ${{ github.actor }}
    registry_password: # default: ${{ github.token }}
```

### Nix

```elm
nix run github:spotdemo4/nix-flake-release
```

#### Flake

```nix
inputs = {
    bumper = {
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
  -w /app \
  -v "$(pwd):/app" \
  -v "$HOME/.ssh:/root/.ssh" \
  ghcr.io/spotdemo4/nix-flake-release:0.4.5
```

### Downloads

#### [release.sh](/src/release.sh) - bash script

requires [jq](https://jqlang.org/), [skopeo](https://github.com/containers/skopeo), [gh](https://cli.github.com/) (github)

#### [bumper-0.7.1.tar.xz](https://github.com/spotdemo4/nix-flake-release/releases/download/v0.4.5/nix-flake-release-0.4.5.tar.xz) - bundle

contains all dependencies, only use if necessary
