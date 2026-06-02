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
    let
      goTags = [ "containers_image_openpgp" ];
      goFlags = "-tags=${builtins.concatStringsSep "," goTags}";
    in
    trev.libs.mkFlake (
      system: pkgs: {
        devShells = {
          default = pkgs.mkShell {
            shellHook = pkgs.shellhook.ref;
            GOFLAGS = goFlags;
            nativeBuildInputs = with pkgs; [
              pkg-config
            ];
            buildInputs = with pkgs; [
              xz.dev
              xz.out
            ];
            packages = with pkgs; [
              # go
              go
              gopls
              gotools

              # lint
              go-tools
              nixd
              nil

              # format
              nixfmt
              oxfmt
              treefmt

              # util
              bumper
              fix-hash
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
              curl
              go
              jq
            ];
          };

          update = pkgs.mkShell {
            packages = with pkgs; [
              renovate
              fix-hash
              go
            ];
          };

          vulnerable = pkgs.mkShell {
            packages = with pkgs; [
              # go
              go
              govulncheck

              # nix
              flake-checker

              # actions
              octoscan
              zizmor
            ];
          };
        };

        apps = pkgs.mkApps {
          dev = "go run .";
        };

        checks = pkgs.mkChecks {
          go = self.packages.${system}.default.overrideAttrs {
            dontBuild = true;
            installPhase = ''
              touch $out
            '';
          };

          actions-gh = {
            root = ./.;
            files = [
              ./action.yaml
              ./.github/workflows
            ];
            packages = with pkgs; [
              action-validator
              octoscan
            ];
            forEach = ''
              action-validator "$file"
              octoscan scan "$file"
            '';
          };

          actions-fj = {
            root = ./.forgejo/workflows;
            filter = file: file.hasExt "yaml";
            packages = with pkgs; [
              zizmor
            ];
            forEach = ''
              zizmor --offline "$file"
            '';
          };

          renovate = {
            root = ./.forgejo;
            files = ./.forgejo/renovate.json;
            packages = with pkgs; [
              renovate
            ];
            script = ''
              renovate-config-validator renovate.json
            '';
          };

          nix = {
            root = ./.;
            filter = file: file.hasExt "nix";
            packages = with pkgs; [
              nixfmt
            ];
            forEach = ''
              nixfmt --check "$file"
            '';
          };

          config = {
            root = ./.;
            filter = file: file.hasExt "json" || file.hasExt "yaml" || file.hasExt "toml" || file.hasExt "md";
            ignore = ./.vscode;
            packages = with pkgs; [
              oxfmt
            ];
            forEach = ''
              oxfmt --check "$file"
            '';
          };
        };

        packages.default = pkgs.buildGoModule (
          final: with pkgs.lib; {
            pname = "flake-release";
            version = "0.18.0";

            src = fileset.toSource {
              root = ./.;
              fileset = fileset.unions [
                ./go.mod
                ./go.sum
                (fileset.fileFilter (file: file.hasExt "go") ./.)
              ];
            };
            goSum = ./go.sum;
            proxyVendor = true;
            vendorHash = "sha256-Nl4bh3qnS/H5fLs3S2G3qTtnjkrAPDWgiiJ1qGv3q5E=";
            tags = goTags;

            nativeBuildInputs = with pkgs; [
              makeWrapper
              pkg-config
            ];
            buildInputs = with pkgs; [
              xz.dev
              xz.out
            ];

            nativeCheckInputs = with pkgs; [
              go-tools
              gotools
            ];
            checkPhase = ''
              export HOME="$TMPDIR"
              export GOFLAGS="${goFlags}"
              go test ./...
              go vet ./...
              staticcheck ./...
              modernize -any=false ./...
            '';

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

        formatter = pkgs.treefmt.withConfig {
          configFile = ./treefmt.toml;
          runtimeInputs = with pkgs; [
            go
            nixfmt
            oxfmt
          ];
        };
      }
    );
}
