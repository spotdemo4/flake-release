{
  description = "nix flake releaser";

  nixConfig = {
    extra-substituters = [
      "https://nix.trev.zip"
    ];
    extra-trusted-public-keys = [
      "trev:I39N/EsnHkvfmsbx8RUW+ia5dOzojTQNCTzKYij1chU="
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
        fs = pkgs.lib.fileset;
        deps = with pkgs; [
          file
          findutils
          forgejo-cli
          gh
          gnused
          jq
          manifest-tool
          mktemp
          ncurses
          skopeo
          tea
          xz
          zip
        ];
      in
      {
        devShells = {
          default = pkgs.mkShell {
            shellHook = pkgs.shellhook.ref;
            packages =
              with pkgs;
              [
                # lint
                shellcheck

                # format
                nixfmt
                prettier

                # util
                bumper
                flake-release
                renovate
              ]
              ++ deps;
          };

          bump = pkgs.mkShell {
            packages = with pkgs; [
              bumper
            ];
          };

          release = pkgs.mkShell {
            packages = with pkgs; [
              flake-release
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

        packages = with pkgs.lib; rec {
          default = pkgs.stdenv.mkDerivation (finalAttrs: {
            pname = "flake-release";
            version = "0.11.2";

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
              sed -i '1c\#!${pkgs.runtimeShell}' src/flake-release.sh
              sed -i '2c\export PATH="${makeBinPath finalAttrs.runtimeInputs}:$PATH"' src/flake-release.sh
            '';

            doCheck = true;
            checkPhase = ''
              shellcheck **/*.sh
            '';

            installPhase = ''
              mkdir -p $out/lib/flake-release
              cp -R src/*.sh $out/lib/flake-release

              mkdir -p $out/bin
              makeWrapper "$out/lib/flake-release/flake-release.sh" "$out/bin/flake-release"
            '';

            dontFixup = true;

            meta = {
              description = "flake package releaser";
              mainProgram = "flake-release";
              homepage = "https://github.com/spotdemo4/flake-release";
              changelog = "https://github.com/spotdemo4/flake-release/releases/tag/v${finalAttrs.version}";
              license = licenses.mit;
              platforms = platforms.all;
            };
          });

          image = pkgs.dockerTools.buildLayeredImage {
            name = default.pname;
            tag = default.version;

            fromImage = pkgs.image.nix;
            contents = with pkgs; [
              dockerTools.caCertificates
            ];

            created = "now";
            meta = default.meta;

            config = {
              Entrypoint = [ "${meta.getExe default}" ];
              Env = [ "DOCKER=true" ];
              Labels = {
                "org.opencontainers.image.title" = default.pname;
                "org.opencontainers.image.description" = default.meta.description;
                "org.opencontainers.image.source" = default.meta.homepage;
                "org.opencontainers.image.version" = default.version;
                "org.opencontainers.image.licenses" = default.meta.license.spdxId;
              };
            };
          };
        };

        formatter = pkgs.nixfmt-tree;
      }
    );
}
