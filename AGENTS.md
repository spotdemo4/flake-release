# AGENTS.md

## Task Completion Requirements

- `nix fmt` must be used for formatting.
- `nix flake check` must pass before considering tasks completed. Intent-to-add (`git add -N`) new files so `nix flake check` can see them.

## Version Control Requirements

- Commit messages created by agents must follow [Conventional Commits 1.0.0](https://www.conventionalcommits.org/en/v1.0.0/).
- Commits created by agents must include an `Assisted-by: [tool name] ([primary model name and version])` Git commit trailer.
- Branch names created by agents must follow [Conventional Branch 1.1.0](https://conventionalbranch.org/).

## REST API Descriptions

- [GitHub API 2026-03-10](https://raw.githubusercontent.com/github/rest-api-description/refs/heads/main/descriptions/api.github.com/api.github.com.2026-03-10.json)
- [Gitea API](https://gitea.com/swagger.v1.json)
- [Forgejo API](https://trev.zip/swagger.v1.json)
