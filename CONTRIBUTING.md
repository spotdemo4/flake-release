# contributing

## requirements

- [nix](https://nixos.org/)

## getting started

```sh
nix develop
```

with [direnv](https://direnv.net/):

```sh
ln -s .envrc.project .envrc
direnv allow
```

### run

```sh
nix run . -- --dry-run
```

with [go](https://go.dev/):

```sh
go run . --dry-run
```

### format

```sh
nix fmt
```

with [golangci-lint](https://golangci-lint.run/):

```sh
golangci-lint fmt
```

### check

```sh
nix flake check
```

with [go](https://go.dev/) and [golangci-lint](https://golangci-lint.run/):

```sh
go test ./...
golangci-lint run ./...
```

### build

```sh
nix build
```

with [go](https://go.dev/):

```sh
go build ./...
```

### dependencies

after changing `go.mod`, update `gomod2nix.toml` with [gomod2nix](https://github.com/nix-community/gomod2nix):

```sh
gomod2nix generate --with-deps
```

### release

with [bumper](https://trev.zip/llc/bumper):

```sh
bumper
```

releases are automatically created for [significant](https://www.conventionalcommits.org/en/v1.0.0/#summary) changes
