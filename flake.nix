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
    systems.url = "github:spotdemo4/systems";
    nixpkgs.url = "github:nixos/nixpkgs/nixpkgs-unstable";
    trev = {
      url = "github:spotdemo4/nur";
      inputs.systems.follows = "systems";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs =
    {
      self,
      trev,
      ...
    }:
    trev.libs.mkFlake (
      system: pkgs: {
        devShells = {
          default = pkgs.mkShell {
            shellHook = pkgs.shellhook.ref;
            packages = with pkgs; [
              # deps
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

              # lint
              shellcheck

              # format
              nixfmt
              prettier

              # util
              bumper
              flake-release
              renovate
            ];
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

        checks = pkgs.mkChecks {
          shellcheck = {
            root = ./.;
            fileset = pkgs.lib.fileset.unions [
              (pkgs.lib.fileset.fileFilter (file: file.hasExt "sh") ./.)
              ./.shellcheckrc
            ];
            deps = with pkgs; [
              shellcheck
            ];
            forEach = ''
              shellcheck "$file"
            '';
          };

          actions = {
            root = ./.;
            fileset = pkgs.lib.fileset.unions [
              ./action.yaml
              ./.github/workflows
            ];
            deps = with pkgs; [
              action-validator
              octoscan
            ];
            forEach = ''
              action-validator "$file"
              octoscan scan "$file"
            '';
          };

          renovate = {
            root = ./.github;
            fileset = ./.github/renovate.json;
            deps = with pkgs; [
              renovate
            ];
            script = ''
              renovate-config-validator renovate.json
            '';
          };

          nix = {
            root = ./.;
            filter = file: file.hasExt "nix";
            deps = with pkgs; [
              nixfmt
            ];
            forEach = ''
              nixfmt --check "$file"
            '';
          };

          prettier = {
            root = ./.;
            filter = file: file.hasExt "yaml" || file.hasExt "json" || file.hasExt "md";
            deps = with pkgs; [
              prettier
            ];
            forEach = ''
              prettier --check "$file"
            '';
          };
        };

        packages.default = pkgs.stdenv.mkDerivation (
          final: with pkgs.lib; {
            pname = "flake-release";
            version = "0.14.6";

            src = fileset.toSource {
              root = ./.;
              fileset = fileset.unions [
                ./.shellcheckrc
                (fileset.fileFilter (file: file.hasExt "sh") ./.)
              ];
            };

            nativeBuildInputs = with pkgs; [
              makeWrapper
              shellcheck
            ];

            runtimeInputs = with pkgs; [
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

            unpackPhase = ''
              cp -a "$src/." .
            '';

            dontBuild = true;

            configurePhase = ''
              chmod +w src
              sed -i '1c\#!${pkgs.runtimeShell}' src/flake-release.sh
              sed -i '2i\export PATH="${makeBinPath final.runtimeInputs}:$PATH"' src/flake-release.sh
            '';

            installPhase = ''
              mkdir -p $out/lib/flake-release
              cp -R src/*.sh $out/lib/flake-release

              mkdir -p $out/bin
              makeWrapper "$out/lib/flake-release/flake-release.sh" "$out/bin/flake-release"
            '';

            dontFixup = true;

            meta = {
              mainProgram = "flake-release";
              description = "Flake package releaser";
              license = licenses.mit;
              platforms = platforms.unix;
              homepage = "https://github.com/spotdemo4/flake-release";
              changelog = "https://github.com/spotdemo4/flake-release/releases/tag/v${final.version}";
            };
          }
        );

        images.default = pkgs.mkImage {
          fromImage = pkgs.image.nix;
          src = self.packages.${system}.default;
          contents = with pkgs; [ dockerTools.caCertificates ];
          config.Env = [ "DOCKER=true" ];
        };

        formatter = pkgs.nixfmt-tree;
        schemas = trev.schemas;
      }
    );
}
