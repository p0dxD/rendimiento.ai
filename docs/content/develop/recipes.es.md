# Recetas para cambios comunes

Cada receta enumera los archivos que hay que tocar, en orden. Termine cada una con `make test-remote`, el libro y un despliegue.

## Agregar un campo a `rendimiento.yaml` {#add-a-field-to-rendimientoyaml}

Ejemplo: `terminationGracePeriod` (los segundos que tiene un pod para apagarse).

1. **El tipo** en `internal/spec/spec.go`, en `Service`, con un comentario de documentación:
    ```go
    // TerminationGracePeriod is how long a stopping pod may take, in seconds (default 30).
    TerminationGracePeriod int `json:"terminationGracePeriod,omitempty"`
    ```
2. **El valor predeterminado** en `Spec.Default` si lo necesita, y **la validación** en `Spec.Validate` (rango, formato), con un mensaje que nombre la ruta (`services[%d].terminationGracePeriod`).
3. **Las pruebas** en `internal/spec/spec_test.go`: un valor válido se lee bien; uno inválido da el mensaje.
4. **La generación** en `internal/render/render.go` (`deployment`): defina `pod.TerminationGracePeriodSeconds`.
5. **Los archivos de referencia**: si la especificación de referencia debe mostrarlo, agréguelo a la entrada de la prueba; vuelva a generar y **revise** las diferencias:
    ```bash
    go test ./internal/render -run Golden -update && git diff internal/render/testdata
    ```
6. **Vuelva a generar** el CRD, porque el recurso `App` incluye la especificación: `make generate`. Confirme `deploy/crds/`.
7. **La interfaz**, si la gente debe definirlo en el asistente: el tipo `Service` en `web/src/api.ts` y un campo en `ServiceForm` (`pages/NewApp.tsx`).
8. **El libro**: una fila en [la referencia](../guide/spec.md).
9. **El despliegue**: `kubectl apply -f deploy/crds/rendimiento.ai_apps.yaml` **antes** de la plataforma nueva (si no, el servidor de la API tira el campo nuevo), luego `make image` y reinicie.

## Agregar una ruta a la API y una página a la interfaz {#add-an-api-endpoint-and-a-ui-page}

Ejemplo: `GET /api/apps/{app}/pods`.

1. **El manejador**: un método de `*Server` en `internal/api` (un archivo nuevo por área, como `addons.go`):
    ```go
    func (s *Server) appPods(w http.ResponseWriter, r *http.Request, login string) {
        a := s.app(w, r)                 // 404 for unknown apps
        if a == nil { return }
        // … read, then:
        writeJSON(w, result)             // or httpError(w, status, message)
    }
    ```
    Deje la lógica fuera de los manejadores: póngala en el paquete de la plataforma, del almacén o del controlador, y llámela.
2. **La ruta** en `Server.Handler`: `auth("GET /api/apps/{app}/pods", s.appPods)`. `auth` significa que se requiere una sesión; `login` es el usuario.
3. **La prueba** en `internal/api/server_test.go` (un almacén real, una plataforma de imitación donde haga falta).
4. **El cliente**: el tipo de la respuesta y una función en `web/src/api.ts`.
5. **La página o el componente** en `web/src/pages/`; una ruta en `main.tsx` (y un `NavLink` para una página principal). Use `usePoll` para cargar y refrescar.
6. **El libro**: la [referencia de la API](../reference/api.md), y el capítulo de la guía al que pertenece la función.

## Agregar un complemento al catálogo {#add-an-add-on-to-the-catalog}

1. Revise el paquete: el `index.yaml` de su repositorio, la versión, y que tenga imágenes **arm64**.
2. Agregue una entrada a `Catalog` en `internal/addon/catalog.go`: `ID`, `Title`, `Category`, `Description`, `Helm` (repositorio, paquete, versión), `Namespace` y unos cuantos `Fields` (rutas de valores con puntos, con etiqueta, tipo y valor predeterminado). Ponga `ManualSync` en cualquier cosa riesgosa, y `Notes` para lo que los usuarios deben saber.
3. Pruébelo de verdad: instálelo desde la interfaz, revise la vista previa y luego desinstálelo.
4. El libro: [Complementos](../guide/addons.md) enumera el catálogo.

## Agregar una comprobación del entorno {#add-an-environment-check}

1. Escriba `func (c *Checker) checkX(ctx context.Context) Check` en `internal/environment/checks.go`: defina `ID`, `Name`, `Category`, `Required`, luego `Status` (`OK`, `Warning`, `Missing`, `Error`), un `Summary` de una línea, `Details` y un `Fix` que le diga al lector exactamente qué hacer.
2. Regístrela en `Checker.checks`.
3. Pruébela en `environment_test.go` con un cliente de imitación.

## Agregar un proveedor de DNS {#add-a-dns-provider}

1. Implemente `dns.Provider` (`Ensure`, `Remove`, `Zones`, `Describe`) en `internal/dns/<proveedor>.go`. `Ensure` debe negarse a sobrescribir registros que no creó (marque los suyos, como lo hace el comentario de Cloudflare).
2. Selecciónelo en `cmd/rendimiento/main.go` a partir de ajustes nuevos.
3. Agréguelo a las opciones de proveedor que muestra la página Entorno.
4. Pruebas con `httptest.NewServer` en lugar de la API del proveedor.

## Cambiar la base de datos {#change-the-database}

1. Agregue `internal/store/migrations/000N_que.sql`. Nunca edite una migración que ya corrió en algún lado.
2. Agregue métodos al almacén, cada uno con un comentario de documentación, SQL simple y `ErrNotFound` para las filas que no existen.
3. Pruebas en `internal/store/store_test.go` (corren contra el Postgres de prueba).
4. La migración corre sola en el siguiente arranque de la plataforma.

## Agregar un tipo nuevo de paso de integración continua {#add-a-new-kind-of-ci-step}

Los pasos de *tarea* (`tasks:`, comandos como `eas build` con secretos) se agregaron así, así que sígalos como ejemplo resuelto: `spec.Task`, `KindTask`, `internal/pipeline/task.go` y `skippedTasks` en la plataforma.

1. **La especificación**: una forma de declararlo (una lista de primer nivel como `tasks:`), con valores predeterminados y validación (nombres únicos entre servicios, tareas programadas y tareas; referencias que existan; sin ciclos).
2. **El plan** (`internal/pipeline/plan.go`): un `Kind` nuevo, pasos con sus dependencias.
3. **El ejecutor** (`KubeExecutor.pod`): el contenedor de ese tipo (imagen, comando, entorno desde secretos, recursos).
4. **La versión** (`platform.release`): decida si el resultado del paso afecta las versiones.
5. **La interfaz**: el grafo de la ejecución muestra cualquier tipo; agregue un ícono si quiere.
6. **Las pruebas**: pruebas del plan, una prueba de la forma del pod (como `TestBuildPodPicksBuilder`) y una prueba del corredor.

## Detectar un lenguaje o marco de trabajo nuevo {#detect-a-new-language-or-framework}

1. `internal/detect/detect.go`: reconozca el manifiesto, defina `Language`, `Framework`, `Port`, la imagen y el comando de pruebas, con una línea en `Reasons` que explique la suposición.
2. Una plantilla de Dockerfile en `templates/dockerfiles/<nombre>.tmpl`, elegida en `internal/generate/generate.go`.
3. Pruebas con un repositorio `fstest.MapFS` en `detect_test.go` y `generate_test.go`.

## Administrar un tipo nuevo de objeto de Kubernetes para las aplicaciones {#manage-a-new-kind-of-kubernetes-object-for-apps}

1. Genérelo (`internal/render`) y agréguelo a `Objects` y a `Objects.List`.
2. Deje que el controlador lo vigile: `Owns(&Kind{})` en `AppReconciler.SetupWithManager`.
3. Pódelo (`prune` enumera y borra por etiqueta los que ya sobran).
4. Dé los permisos de RBAC en `deploy/rbac.yaml`.
5. Decida si la adopción debe tomar su control (`takeover`).
