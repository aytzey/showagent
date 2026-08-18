# Contributing to showagent

Thanks for helping improve showagent. Keep changes focused and preserve the
core safety contract: existing sessions are never modified by branch or
convert operations, and destructive actions require explicit user intent.

## Development setup

Requirements: Go 1.25.13 or newer.

```sh
git clone https://github.com/aytzey/showagent.git
cd showagent
go test ./... -race
go vet ./...
go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...
```

Run `golangci-lint run` with the version configured in
`.github/workflows/ci.yml` before opening a pull request.

## Provider changes

A provider implements `session.ProviderImpl` and is registered in
`internal/session/provider.go`. Provider tests must use temporary homes through
the provider's environment override; never read, write, or delete the
developer's real agent store from the unit suite.

For format changes, include fixtures for discovery, transcript extraction,
branching, conversion, malformed input, and secret-safe previews. Describe the
upstream CLI version or source commit used to verify the format.

## Pull requests

- Add regression tests for behavior changes.
- Update `README.md` when CLI flags, keybindings, providers, or trust
  boundaries change.
- Keep generated binaries, real transcripts, credentials, and local paths out
  of commits.
- Explain manual verification for changes that touch a real provider CLI.
