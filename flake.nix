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
        deps = with pkgs; [
          buildah
          file
          findutils
          gh
          gnused
          jq
          mktemp
          ncurses
          skopeo
          tea
          xz
          zip
        ];
        fs = pkgs.lib.fileset;
      in
      rec {
        devShells = {
          default = pkgs.mkShell {
            packages =
              with pkgs;
              [
                # util
                bumper

                # lint
                shellcheck
                nixfmt
                prettier
              ]
              ++ deps;
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

              # actions
              octoscan
            ];
          };
        };

        checks = pkgs.lib.mkChecks {
          shellcheck = {
            src = fs.toSource {
              root = ./.;
              fileset = fs.unions [
                (fs.fileFilter (file: file.hasExt "sh") ./.)
                ./.shellcheckrc
              ];
            };
            deps = with pkgs; [
              shellcheck
            ];
            script = ''
              shellcheck **/*.sh
            '';
          };

          actions = {
            src = fs.toSource {
              root = ./.;
              fileset = fs.unions [
                ./action.yaml
                ./.github/workflows
              ];
            };
            deps = with pkgs; [
              action-validator
              octoscan
            ];
            script = ''
              action-validator **/*.yaml
              octoscan scan .
            '';
          };

          renovate = {
            src = fs.toSource {
              root = ./.github;
              fileset = ./.github/renovate.json;
            };
            deps = with pkgs; [
              renovate
            ];
            script = ''
              renovate-config-validator renovate.json
            '';
          };

          nix = {
            src = fs.toSource {
              root = ./.;
              fileset = fs.fileFilter (file: file.hasExt "nix") ./.;
            };
            deps = with pkgs; [
              nixfmt-tree
            ];
            script = ''
              treefmt --ci
            '';
          };

          prettier = {
            src = fs.toSource {
              root = ./.;
              fileset = fs.fileFilter (file: file.hasExt "yaml" || file.hasExt "json" || file.hasExt "md") ./.;
            };
            deps = with pkgs; [
              prettier
            ];
            script = ''
              prettier --check .
            '';
          };
        };

        packages = {
          default = pkgs.stdenv.mkDerivation (finalAttrs: {
            pname = "nix-flake-release";
            version = "0.8.5";

            src = fs.toSource {
              root = ./.;
              fileset = fs.unions [
                (fs.fileFilter (file: file.hasExt "sh") ./.)
                ./.shellcheckrc
              ];
            };

            nativeBuildInputs = with pkgs; [
              makeWrapper
              shellcheck
            ];

            runtimeInputs = deps;

            unpackPhase = ''
              cp -a "$src/." .
            '';

            dontBuild = true;

            configurePhase = ''
              chmod +w src
              sed -i '1c\#!${pkgs.runtimeShell}' src/nix-release.sh
              sed -i '2c\export PATH="${pkgs.lib.makeBinPath finalAttrs.runtimeInputs}:$PATH"' src/nix-release.sh
            '';

            doCheck = true;
            checkPhase = ''
              shellcheck **/*.sh
            '';

            installPhase = ''
              mkdir -p $out/lib/nix-flake-release
              cp -R src/*.sh $out/lib/nix-flake-release

              mkdir -p $out/bin
              makeWrapper "$out/lib/nix-flake-release/nix-release.sh" "$out/bin/nix-flake-release"
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
