# Contributing

Thanks for helping. The short version:

1. **Read** [Developing it](docs/content/develop/index.md): setup, recipes, testing and patterns.
2. **Keep changes focused.** One feature or fix per pull request. Put pure moves (renames, file splits) in their own commits.
3. **Test.** Run `make test-remote` (or `make test` on a machine that isn't a cluster control plane). New behaviour needs a test. Review any golden-file change before committing it.
4. **Document.** Every exported name gets a doc comment (`go run ./hack/undoc` must print nothing). Update the book in the same pull request, regenerate the code map with `make docs-codemap`, and run `make docs-check` if you changed a diagram.
5. **Format.** Run `gofmt -w .`, `go vet ./...`, and `npm run typecheck` in `web/`.
6. **Describe** what changed, why, and how you tested it.

Good first changes are listed in [Refactoring](docs/content/develop/refactoring.md) and [Roadmap](docs/content/future/roadmap.md).

Report security issues privately through GitHub's *Report a vulnerability*, not as public issues.
