# Recipes for common changes

Each recipe lists the files to touch, in order. Finish every one with `make test-remote`, the book, and a deploy.

## Add a field to `rendimiento.yaml`

Example: `terminationGracePeriod` (seconds a pod gets to shut down).

1. **Type** in `internal/spec/spec.go`, on `Service`, with a doc comment:
    ```go
    // TerminationGracePeriod is how long a stopping pod may take, in seconds (default 30).
    TerminationGracePeriod int `json:"terminationGracePeriod,omitempty"`
    ```
2. **Default** in `Spec.Default` if it needs one, and **validation** in `Spec.Validate` (range, format), with a message naming the path (`services[%d].terminationGracePeriod`).
3. **Tests** in `internal/spec/spec_test.go`: a valid value parses; an invalid one gives the message.
4. **Rendering** in `internal/render/render.go` (`deployment`): set `pod.TerminationGracePeriodSeconds`.
5. **Golden files**: if the golden spec should show it, add it to the test's input; regenerate and **review** the diff:
    ```bash
    go test ./internal/render -run Golden -update && git diff internal/render/testdata
    ```
6. **Regenerate** the CRD, since the `App` resource embeds the spec: `make generate`. Commit `deploy/crds/`.
7. **UI**, if people should set it in the wizard: the `Service` type in `web/src/api.ts`, a field in `ServiceForm` (`pages/NewApp.tsx`).
8. **Book**: a row in [the reference](../guide/spec.md).
9. **Deploy**: `kubectl apply -f deploy/crds/rendimiento.ai_apps.yaml` **before** the new platform (otherwise the API server drops the new field), then `make image` and restart.

## Add an API endpoint and a UI page

Example: `GET /api/apps/{app}/pods`.

1. **Handler**: a method on `*Server` in `internal/api` (a new file per area, like `addons.go`):
    ```go
    func (s *Server) appPods(w http.ResponseWriter, r *http.Request, login string) {
        a := s.app(w, r)                 // 404 for unknown apps
        if a == nil { return }
        // … read, then:
        writeJSON(w, result)             // or httpError(w, status, message)
    }
    ```
    Keep logic out of handlers: put it in the platform, store or controller package, and call it.
2. **Route** in `Server.Handler`: `auth("GET /api/apps/{app}/pods", s.appPods)`. `auth` means a session is required; `login` is the user.
3. **Test** in `internal/api/server_test.go` (a real store, a fake platform where needed).
4. **Client**: the response type and a function in `web/src/api.ts`.
5. **Page or component** in `web/src/pages/`; a route in `main.tsx` (and a `NavLink` for a top-level page). Use `usePoll` to load and refresh.
6. **Book**: the [API reference](../reference/api.md), and the guide chapter the feature belongs to.

## Add an add-on to the catalog

1. Check the chart: its repository's `index.yaml`, the version, and **arm64** images.
2. Add an entry to `Catalog` in `internal/addon/catalog.go`: `ID`, `Title`, `Category`, `Description`, `Helm` (repo, chart, version), `Namespace`, and a few `Fields` (dotted value paths with a label, type and default). Set `ManualSync` for anything risky, and `Notes` for what users must know.
3. Try it for real: install it from the UI, check the preview, then uninstall.
4. Book: [Add-ons](../guide/addons.md) lists the catalog.

## Add an environment check

1. Write `func (c *Checker) checkX(ctx context.Context) Check` in `internal/environment/checks.go`: set `ID`, `Name`, `Category`, `Required`, then `Status` (`OK`, `Warning`, `Missing`, `Error`), a one-line `Summary`, `Details`, and a `Fix` that tells the reader exactly what to do.
2. Register it in `Checker.checks`.
3. Test it in `environment_test.go` with a fake client.

## Add a DNS provider

1. Implement `dns.Provider` (`Ensure`, `Remove`, `Zones`, `Describe`) in `internal/dns/<provider>.go`. `Ensure` must refuse to overwrite records it did not create (mark yours, as Cloudflare's comment does).
2. Select it in `cmd/rendimiento/main.go` from new settings.
3. Add it to the provider options shown on the Environment page.
4. Tests with `httptest.NewServer` standing in for the provider's API.

## Change the database

1. Add `internal/store/migrations/000N_what.sql`. Never edit a migration that has already run anywhere.
2. Add store methods, each with a doc comment, plain SQL, and `ErrNotFound` for missing rows.
3. Tests in `internal/store/store_test.go` (they run against the test Postgres).
4. The migration runs automatically on the platform's next start.

## Add a new kind of CI step

Example: a *command* step (run `eas build` for a mobile app, with a secret), the next roadmap item.

1. **Spec**: a way to declare it (for example `services[].steps` or a top-level `tasks:`), with validation.
2. **Plan** (`internal/pipeline/plan.go`): a new `Kind`, steps with their dependencies.
3. **Executor** (`KubeExecutor.pod`): the container for that kind (image, command, env from secrets, resources).
4. **Release** (`platform.release`): decide whether the step's outcome affects releases.
5. **UI**: the run graph shows any kind; add an icon if you like.
6. **Tests**: plan tests, a pod-shape test (like `TestBuildPodPicksBuilder`), and a runner test.

## Detect a new language or framework

1. `internal/detect/detect.go`: recognise the manifest, set `Language`, `Framework`, `Port`, test image and command, with a `Reasons` line explaining the guess.
2. A Dockerfile template in `templates/dockerfiles/<name>.tmpl`, chosen in `internal/generate/generate.go`.
3. Tests with an `fstest.MapFS` repository in `detect_test.go` and `generate_test.go`.

## Manage a new kind of Kubernetes object for apps

1. Render it (`internal/render`), add it to `Objects` and `Objects.List`.
2. Let the controller watch it: `Owns(&Kind{})` in `AppReconciler.SetupWithManager`.
3. Prune it (`prune` lists and deletes stale ones by label).
4. Grant the RBAC in `deploy/rbac.yaml`.
5. Decide whether adoption should take it over (`takeover`).
