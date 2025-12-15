{
  description = "nix flake releaser";

  nixConfig = {
    extra-substituters = [
      "https://cache.trev.zip/nur"
    ];
    extra-trusted-public-keys = [
      "nur:70xGHUW1+1b8FqBchldaunN//pZNVo6FKuPL4U/n844="
    ];
  };

  inputs = {
    systems.url = "github:nix-systems/default";
    nixpkgs.url = "github:nixos/nixpkgs/nixpkgs-unstable";
    trev = {
      url = "github:spotdemo4/nur";
      inputs.systems.follows = "systems";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs =
    {
      nixpkgs,
      trev,
      ...
    }:
    trev.libs.mkFlake (
      system:
      let
        pkgs = import nixpkgs {
          inherit system;
          overlays = [
            trev.overlays.packages
            trev.overlays.libs
            trev.overlays.images
          ];
        };
      in
      rec {
        devShells = {
          default = pkgs.mkShell {
            packages = with pkgs; [
              # bash
              file
              findutils
              gh
              gnused
              jq
              mktemp
              ncurses
              nix
              skopeo
              xz
              zip

              # util
              bumper

              # lint
              shellcheck
              nixfmt
              prettier
            ];
            shellHook = pkgs.shellhook.ref;
          };

          update = pkgs.mkShell {
            packages = with pkgs; [
              renovate
            ];
          };

          vulnerable = pkgs.mkShell {
            packages = with pkgs; [
              # nix
              flake-checker
              nix-scan

              # actions
              octoscan
            ];
          };
        };

        checks = pkgs.lib.mkChecks {
          bash = {
            src = packages.default;
            deps = with pkgs; [
              shellcheck
            ];
            script = ''
              shellcheck src/*.sh
            '';
          };

          action = {
            src = ./.;
            deps = with pkgs; [
              action-validator
            ];
            script = ''
              action-validator action.yaml
            '';
          };

          nix = {
            src = ./.;
            deps = with pkgs; [
              nixfmt-tree
            ];
            script = ''
              treefmt --ci
            '';
          };

          actions = {
            src = ./.;
            deps = with pkgs; [
              prettier
              action-validator
              octoscan
              renovate
            ];
            script = ''
              prettier --check "**/*.json" "**/*.yaml"
              action-validator .github/**/*.yaml
              octoscan scan .github
              renovate-config-validator .github/renovate.json
            '';
          };
        };

        apps = pkgs.lib.mkApps {
          dev.script = "./src/release.sh";
        };

        packages = {
          default = pkgs.stdenv.mkDerivation (finalAttrs: {
            pname = "nix-flake-release";
            version = "0.7.0";

            src = builtins.path {
              name = "root";
              path = ./.;
            };

            nativeBuildInputs = with pkgs; [
              makeWrapper
              shellcheck
            ];

            runtimeInputs = with pkgs; [
              file
              findutils
              gh
              gnused
              jq
              mktemp
              ncurses
              nix
              skopeo
              xz
              zip
            ];

            unpackPhase = ''
              cp -a "$src/." .
            '';

            dontBuild = true;

            configurePhase = ''
              chmod +w src
              sed -i '1c\#!${pkgs.runtimeShell}' src/release.sh
              sed -i '2c\export PATH="${pkgs.lib.makeBinPath finalAttrs.runtimeInputs}:$PATH"' src/release.sh
            '';

            doCheck = true;
            checkPhase = ''
              shellcheck src/*.sh
            '';

            installPhase = ''
              mkdir -p $out/lib/nix-flake-release
              cp -R src/*.sh $out/lib/nix-flake-release

              mkdir -p $out/bin
              makeWrapper "$out/lib/nix-flake-release/release.sh" "$out/bin/nix-flake-release"
            '';

            dontFixup = true;

            meta = {
              description = "nix flake releaser";
              mainProgram = "nix-flake-release";
              homepage = "https://github.com/spotdemo4/nix-flake-release";
              changelog = "https://github.com/spotdemo4/nix-flake-release/releases/tag/v${finalAttrs.version}";
              platforms = pkgs.lib.platforms.all;
            };
          });

          image = pkgs.dockerTools.buildLayeredImage {
            name = packages.default.pname;
            tag = packages.default.version;

            fromImage = pkgs.image.nix;
            contents = with pkgs; [
              dockerTools.caCertificates
              packages.default
            ];

            created = "now";
            meta = packages.default.meta;

            config = {
              Cmd = [ "${pkgs.lib.meta.getExe packages.default}" ];
              Env = [ "DOCKER=true" ];
            };
          };
        };

        formatter = pkgs.nixfmt-tree;
      }
    );
}
