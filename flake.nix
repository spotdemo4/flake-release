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
    gomod2nix = {
      url = "github:nix-community/gomod2nix";
      inputs.nixpkgs.follows = "nixpkgs";
      inputs.flake-utils.inputs.systems.follows = "systems";
    };
  };

  outputs =
    {
      self,
      trevpkgs,
      gomod2nix,
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
            shellHook = pkgs.trev.shellhook.ref;
            env = {
              GOTOOLCHAIN = "local";
              GOFLAGS = goFlags;
            };
            nativeBuildInputs = with pkgs; [
              pkg-config
            ];
            buildInputs = with pkgs; [
              xz
            ];
            packages = with pkgs; [
              # go
              go
              gopls
              gotools
              golangci-lint

              vscode-json-languageserver # json
              yaml-language-server # yaml
              tombi # toml
              oxfmt # format

              # nix
              nixd
              nixfmt

              # util
              treefmt
              trev.bumper
              gomod2nix.packages.${system}.default
            ];
          };

          bump = pkgs.mkShell {
            packages = with pkgs; [
              trev.bumper
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
              trev.renovate
              go # go mod tidy
            ];
          };

          vulnerable = pkgs.mkShell {
            env.GOFLAGS = goFlags;
            nativeBuildInputs = with pkgs; [
              pkg-config
            ];
            buildInputs = with pkgs; [
              xz
            ];
            packages = with pkgs; [
              go
              govulncheck # go
              flake-checker # nix
              zizmor # actions
            ];
          };
        };

        # nix run [#...]
        apps = pkgs.trev.libs.mkApps {
          dev = "go run .";
          update-deps = {
            packages = with pkgs; [
              go
              gomod2nix.packages.${system}.default
            ];
            script = ''
              export GOFLAGS="${goFlags}"
              go mod tidy
              gomod2nix generate --with-deps
            '';
          };
        };

        # nix build [#...]
        packages = {
          default = pkgs.trev.libs.mkGoModule (
            final: with pkgs.lib; {
              pname = "flake-release";
              version = "0.50.0";

              src = fileset.toSource {
                root = ./.;
                fileset = fileset.unions [
                  ./go.mod
                  ./go.sum
                  (fileset.fileFilter (file: file.hasExt "go") ./.)
                ];
              };
              goSum = ./go.sum;
              modules = ./gomod2nix.toml;
              tags = goTags;
              ldflags = [
                "-X=trev.zip/llc/flake-release/internal.version=${final.version}"
              ];

              nativeBuildInputs = with pkgs; [
                makeWrapper
                pkg-config
              ];
              buildInputs = with pkgs; [
                xz
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
                downloadPage = "https://trev.zip/llc/flake-release/releases";
                changelog = "https://trev.zip/llc/flake-release/releases/tag/v${final.version}";
                sourceProvenance = [ sourceTypes.fromSource ];
                maintainers = [ maintainers.spotdemo4 ];
              };
            }
          );
        };

        # nix build #images.[...]
        images = {
          default = pkgs.trev.libs.mkImage {
            fromImage = pkgs.trev.images.nix;
            src = self.packages.${system}.default;
            contents = with pkgs; [ dockerTools.caCertificates ];
            config.Env = [ "DOCKER=true" ];
          };
        };

        # nix build #appimages.[...]
        appimages = {
          default = pkgs.trev.libs.mkAppImage {
            src = self.packages.${system}.default;
          };
        };

        # nix fmt
        formatter = pkgs.treefmt.withConfig {
          configFile = ./treefmt.toml;
          runtimeInputs = with pkgs; [
            golangci-lint
            nixfmt
            oxfmt
          ];
        };

        # nix flake check
        checks = pkgs.trev.libs.mkChecks {
          go-lint = {
            src = self.packages.${system}.default;
            packages = with pkgs; [ golangci-lint ];
            script = ''
              export GOFLAGS="$GOFLAGS ${goFlags}"
              cp ${./.golangci.yaml} .golangci.yaml
              golangci-lint run ./...
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
            files = ./.forgejo/renovate.jsonc;
            packages = with pkgs; [
              trev.renovate
            ];
            script = ''
              renovate-config-validator renovate.jsonc
            '';
          };

          config = {
            root = ./.;
            filter =
              file:
              file.hasExt "json"
              || file.hasExt "jsonc"
              || file.hasExt "yaml"
              || file.hasExt "toml"
              || file.hasExt "md";
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
