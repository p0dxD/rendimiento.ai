# Developing it

Everything you need to change rendimiento with confidence:

1. **[Go for this codebase](go.md)**: the language, taught through this repository's own code: packages, types, interfaces, errors, contexts, concurrency, embedding, generics, testing.
2. **[Setting up and building](setup.md)**: tools, the repository layout, `make` targets, running it, working on the UI.
3. **[Recipes for common changes](recipes.md)**: step-by-step: a new `rendimiento.yaml` field, an API endpoint with a UI page, a catalog add-on, an environment check, a DNS provider, a database migration, a new step kind.
4. **[Testing](testing.md)**: the kinds of tests, where they run, how to write new ones.
5. **[Design patterns](patterns.md)**: the patterns in use, why each was chosen, and where to find it.
6. **[Refactoring and code health](refactoring.md)**: what to improve next in the code itself, and how.

The golden rules:

- **Run tests on a worker**, never on `main`: `make test-remote`.
- **Regenerate after changing types**: `make generate` (the test targets do it).
- **Keep the book in the same PR** as the change it describes.
