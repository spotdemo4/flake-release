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
    trevpkgs = {
      url = "github:spotdemo4/trevpkgs";
      inputs.systems.follows = "systems";
      inputs.nixpkgs.follows = "nixpkgs";
    };
  };

  outputs =
    {
      self,
      trevpkgs,
      ...
    }:
    let
      goTags = [ "containers_image_openpgp" ];
      goFlags = "-tags=${builtins.concatStringsSep "," goTags}";
    in
    trevpkgs.libs.mkFlake (
      system: pkgs: {

        # nix develop [#...]
        devShells = {
          default = pkgs.mkShell {
            shellHook = pkgs.shellhook.ref;
            env = {
              GOTOOLCHAIN = "local";
              GOFLAGS = goFlags;
            };
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
              go-tools

              vscode-json-languageserver # json
              yaml-language-server # yaml
              tombi # toml
              oxfmt # format

              # nix
              nixd
              nixfmt

              # util
              treefmt
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
              self.packages.${system}.default
              go # go packages
            ];
          };

          update = pkgs.mkShell {
            packages = with pkgs; [
              renovate
              go # go mod tidy
              fix-hash # vendorHash
            ];
          };

          vulnerable = pkgs.mkShell {
            env.GOFLAGS = goFlags;
            nativeBuildInputs = with pkgs; [
              pkg-config
            ];
            buildInputs = with pkgs; [
              xz.dev
              xz.out
            ];
            packages = with pkgs; [
              go
              govulncheck # go
              flake-checker # nix
              zizmor # actions
            ];
          };
        };

        # nix run #dev
        apps = pkgs.mkApps {
          dev = "go run .";
        };

        # nix build [#...]
        packages = {
          default = pkgs.mkGoModule (
            final: with pkgs.lib; {
              pname = "flake-release";
              version = "0.41.0";

              src = fileset.toSource {
                root = ./.;
                fileset = fileset.unions [
                  ./go.mod
                  ./go.sum
                  (fileset.fileFilter (file: file.hasExt "go") ./.)
                ];
              };
              goSum = ./go.sum;
              vendorHash = "sha256-rqqUlzlO2dI2s4WORudYhyxg0DR6CM6OsL99E3LHLO0=";
              tags = goTags;

              nativeBuildInputs = with pkgs; [
                makeWrapper
                pkg-config
              ];
              buildInputs = with pkgs; [
                xz.dev
                xz.out
              ];

              doCheck = true;
              checkPhase = ''
                runHook preCheck
                export HOME="$TMPDIR"
                export GOFLAGS="$GOFLAGS ${goFlags}"
                go test ./...
                runHook postCheck
              '';

              postInstall = ''
                wrapProgram $out/bin/flake-release --prefix PATH : ${makeBinPath [ pkgs.patchelf ]}
              '';

              meta = {
                mainProgram = "flake-release";
                description = "Flake package releaser";
                license = licenses.mit;
                platforms = platforms.unix;
                badPlatforms = [ systems.inspect.platformPatterns.isStatic ];
                homepage = "https://trev.zip/llc/flake-release";
                changelog = "https://trev.zip/llc/flake-release/releases/tag/v${final.version}";
                downloadPage = "https://trev.zip/llc/flake-release/releases/tag/v${final.version}";
              };
            }
          );
        };

        # nix build #images.[...]
        images = {
          default = pkgs.mkImage {
            fromImage = pkgs.image.nix;
            src = self.packages.${system}.default;
            contents = with pkgs; [ dockerTools.caCertificates ];
            config.Env = [ "DOCKER=true" ];
          };
        };

        # nix build #appimages.[...]
        appimages = {
          default = pkgs.mkAppImage {
            src = self.packages.${system}.default;
          };
        };

        # nix fmt
        formatter = pkgs.treefmt.withConfig {
          configFile = ./treefmt.toml;
          runtimeInputs = with pkgs; [
            go
            nixfmt
            oxfmt
          ];
        };

        # nix flake check
        checks = pkgs.mkChecks {
          inherit (self.packages.${system}) default;

          go = {
            src = self.packages.${system}.default;
            packages = with pkgs; [ go-tools ];
            script = ''
              export GOFLAGS="$GOFLAGS ${goFlags}"
              test -z "$(go fmt ./... | tee /dev/stderr)"
              go vet ./...
              staticcheck ./...
              go fix -diff ./...
            '';
          };

          nix = {
            root = ./.;
            filter = file: file.hasExt "nix";
            packages = with pkgs; [
              nixfmt
            ];
            script = ''
              nixfmt --check "$file"
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
              zizmor
            ];
            script = ''
              action-validator "$file"
              zizmor --offline "$file"
            '';
          };

          actions-fj = {
            root = ./.forgejo/workflows;
            filter = file: file.hasExt "yaml";
            packages = with pkgs; [
              forgejo-runner
              zizmor
            ];
            script = ''
              forgejo-runner validate --workflow --path "$file"
              zizmor --offline "$file"
            '';
          };

          renovate-fj = {
            root = ./.forgejo;
            files = ./.forgejo/renovate.json;
            packages = with pkgs; [
              renovate
            ];
            script = ''
              renovate-config-validator renovate.json
            '';
          };

          config = {
            root = ./.;
            filter = file: file.hasExt "json" || file.hasExt "yaml" || file.hasExt "toml" || file.hasExt "md";
            packages = with pkgs; [
              oxfmt
            ];
            script = ''
              oxfmt --check
            '';
          };
        };
      }
    );
}
