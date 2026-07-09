## Summary

<!-- What user-visible problem does this solve? -->

## Verification

- [ ] `go test ./... -race`
- [ ] `go vet ./...`
- [ ] `golangci-lint run`
- [ ] Documentation updated when behavior or trust boundaries changed

## Provider safety

- [ ] Existing sessions are not modified unexpectedly
- [ ] Tests use temporary provider homes, not real local session stores
- [ ] Format claims identify the upstream CLI version or source reference
