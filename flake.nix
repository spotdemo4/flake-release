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
          ];
        };
      in
      rec {
        devShells = {
          default = pkgs.mkShell {
            packages = with pkgs; [
              # bash
              gh
              file
              zip
              docker
              shellcheck

              # util
              bumper

              # lint
              nixfmt
              prettier
            ];
            shellHook = pkgs.shellhook.ref;
          };

          bump = pkgs.mkShell {
            packages = with pkgs; [
              nix-update
            ];
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
              shellcheck -x release.sh
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

        packages = rec {
          default = pkgs.stdenv.mkDerivation (finalAttrs: {
            pname = "nix-flake-release";
            version = "0.0.1";

            src = builtins.path {
              name = "root";
              path = ./.;
            };

            nativeBuildInputs = with pkgs; [
              shellcheck
            ];

            runtimeInputs = with pkgs; [
              git
              gh
              nix
              file
              zip
              docker
            ];

            unpackPhase = ''
              cp -R "$src/src/." .
            '';

            dontBuild = true;

            configurePhase = ''
              sed -i '1c\#!${pkgs.runtimeShell}' release.sh
              sed -i '2c\export PATH="${pkgs.lib.makeBinPath finalAttrs.runtimeInputs}:$PATH"' release.sh
            '';

            checkPhase = ''
              shellcheck -x release.sh
            '';

            installPhase = ''
              mkdir -p $out/bin
              cp -R *.sh $out/bin
            '';

            meta = {
              description = "nix flake releaser";
              mainProgram = "release.sh";
              homepage = "https://github.com/spotdemo4/nix-flake-release";
              platforms = pkgs.lib.platforms.all;
            };
          });

          docker = pkgs.dockerTools.buildLayeredImage {
            name = default.pname;
            tag = default.version;
            created = "now";
            meta = default.meta;
            contents = with pkgs; [
              default
              dockerTools.caCertificates
            ];
            config.Cmd = [
              "${pkgs.lib.meta.getExe default}"
            ];
          };
        };

        formatter = pkgs.nixfmt-tree;
      }
    );
}
