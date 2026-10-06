# Reestructuración y salud del código

rendimiento creció rápido, una función a la vez, con pruebas en cada paso. El código está sano. No tiene estado global, es pequeño (unas 21,000 líneas de Go y TypeScript) y cada nombre exportado está documentado. Algunas partes ahora hacen más de un trabajo. Este capítulo enumera qué mejorar y cómo hacerlo sin riesgo.

## Cómo reestructurar sin riesgo aquí {#how-to-refactor-safely-here}

1. **Empiece en verde.** Corra `make test-remote` antes de tocar nada.
2. **Primero mueva, luego cambie.** Primero mueva el código sin cambiar su comportamiento (dividir un archivo, renombrar) y confírmelo aparte. Cambie el comportamiento en otra confirmación. Así quien revise puede comprobar el movimiento de forma mecánica.
3. **Apóyese en los archivos de referencia.** Una reestructuración de `render` debe dejar intactos todos los `testdata/*.golden.yaml`. Si cambia un archivo de referencia, la reestructuración cambió el comportamiento.
4. **Mantenga puros los paquetes.** No deje que `spec`, `render`, `detect`, `generate` o `pipeline.Plan` empiecen a hacer entrada y salida. Son rápidos de probar porque no la hacen.
5. **Deje documentados los nombres exportados.** Corra `go run ./hack/undoc` para ver los que no tienen comentario de documentación, y luego `make docs-codemap`.

## Los archivos más grandes {#the-largest-files}

| Archivo | Líneas | Hace |
|---|---|---|
| `internal/api/server.go` | ~1,070 | rutas, autenticación, sesiones, aplicaciones, ejecuciones, versiones, secretos, eventos, configuración |
| `internal/spec/spec.go` | ~970 | cada tipo de `rendimiento.yaml`, valores predeterminados, validación |
| `internal/platform/platform.go` | ~850 | avisos web, ejecuciones, versiones, solicitudes de incorporación, objetos App |
| `internal/renovate/renovate.go` | ~700 | programador, generación del trabajo, configuración, estado |
| `internal/catalog/catalog.go` | ~680 | la página Servicios: descubrimiento, datos de conexión, red local |
| `internal/controller/app_controller.go` | ~620 | conciliación, aplicar, podar, toma de control, necesidades, DNS, estado |

Los archivos grandes no son un problema en sí. En Go es común escribir archivos largos y planos. Vale la pena dividir estos porque cada uno mezcla varios asuntos.

## Reestructuraciones sugeridas, en orden de valor {#suggested-refactors-in-order-of-value}

### Dividir `api/server.go` por área {#split-apiservergo-by-area}

`addons.go`, `installed.go` y `credentials.go` ya muestran el patrón. Mueva los manejadores a `apps.go`, `runs.go`, `auth.go`, `setup.go` y `events.go`. Deje en `server.go` la estructura `Server`, `Handler()` (tener todas las rutas en un lugar es útil) y los ayudantes compartidos (`writeJSON`, `httpError`, `auth`). Es un movimiento puro sin cambio de comportamiento, y es una buena primera contribución.

### Dividir `spec.go` en tipos, valores predeterminados y validación {#split-specgo-into-types-defaults-and-validation}

Divídalo en `types.go` (las estructuras y sus marcas `+kubebuilder`), `defaults.go` y `validate.go`. Tanto controller-gen como el CRD leen los tipos, así que tenerlos juntos ayuda al revisar un cambio de esquema.

### Ponerles nombre a los pasos del controlador {#give-the-controllers-steps-names}

`AppReconciler.Reconcile` se lee como una lista de etapas: generar, necesidades, aplicar, podar, DNS y estado. Convierta cada etapa en un método que reciba la misma estructura `appState` y devuelva un error. Así la función de conciliación cabe en una pantalla, y cada etapa puede tener sus propios casos de envtest.

### SQL con tipos usando sqlc {#typed-sql-with-sqlc}

Las consultas son cadenas escritas a mano y leídas a mano. [sqlc](https://sqlc.dev) genera Go con tipos seguros a partir de archivos `.sql`. Eso atrapa los errores en nombres de columnas al construir y quita el código repetitivo de lectura. Adóptelo un archivo del almacén a la vez, y conserve las mismas firmas de los métodos para que quien los llama no cambie.

### Compartir tipos entre Go y TypeScript {#share-types-between-go-and-typescript}

`web/src/api.ts` repite a mano las formas de JSON que devuelven los manejadores de Go. Cuando cambia un campo en Go, nada falla hasta que se descompone una página. Opciones, de la más sencilla a la más completa:

- **[tygo](https://github.com/gzuidhof/tygo)**: genera interfaces de TypeScript a partir de estructuras de Go. Agréguelo a `make generate`, y deje que la revisión de tipos de la interfaz atrape las diferencias.
- **OpenAPI**: describa la API (a mano o generada) y luego genere un cliente con tipos con `openapi-typescript`. Esto además le da documentación de la API a los usuarios de fuera. Vale la pena hacerlo antes de [abrir el código](../future/open-source.md).

### Registros estructurados y con niveles en todas partes {#structured-levelled-logging-everywhere}

`log/slog` ya se usa en casi todos los paquetes, y controller-runtime registra con `logr` (conectado en `cmd/rendimiento/logr.go`). Dele a cada línea de registro un atributo `app`, `run` o `addon` para poder filtrar los registros, y agregue un ajuste `LOG_LEVEL`.

### Métricas propias {#metrics-of-our-own}

`METRICS_ADDR` (:9090) expone las métricas de controller-runtime: conteos de conciliación, errores y profundidad de la cola. Agregue también métricas de la plataforma: ejecuciones por resultado, duración de la construcción por servicio, tiempo de espera en la cola, el demonio de BuildKit elegido y las versiones. Estas métricas permiten contestar preguntas como "¿las construcciones se están volviendo más lentas?". Vea [Escalar](../future/scaling.md).

### La estructura de la interfaz {#ui-structure}

Las páginas tienen su propia lógica de carga de datos y de formularios. Conforme crezcan, mueva las piezas compartidas (una insignia de estado, el visor de registros o la vista de diferencias) a `components/`. Considere una biblioteca de datos (TanStack Query) en lugar de `usePoll`, y una preparación para pruebas de componentes (Vitest y Testing Library) para que la interfaz tenga pruebas.

### La configuración como estructura {#configuration-as-a-struct}

`main.go` lee unas 40 variables de entorno con `env("NAME", default)`. Júntelas en una estructura `Config` con una función `Load()` que valide todo a la vez. Así el código puede mostrar la configuración efectiva al arrancar (con los secretos tapados) y generar [la referencia de ajustes](../reference/config.md).

## Asperezas conocidas {#known-rough-edges}

- **Una sola réplica de la plataforma.** La elección de líder existe, pero el despliegue tiene `LEADER_ELECTION=false`, y las actualizaciones en vivo están en memoria (vea [Escalar](../future/scaling.md#live-updates-across-replicas)).
- **Registros en Postgres mientras corre una ejecución.** Los registros de construcción se guardan como filas y se mudan al almacenamiento de objetos cuando la ejecución termina ([el archivo de registros](../architecture/data.md#the-log-archive)). Los registros en vivo todavía pasan por la base de datos.
- **Registro sobre HTTP simple.** `registry.example.lan:5000` no es seguro. Está bien en una red doméstica, pero necesita TLS para cualquier otra cosa.
- **Solo arm64.** Las imágenes se construyen para la arquitectura del clúster. Las imágenes para varias arquitecturas se tratan en [Código abierto](../future/open-source.md#releases-and-multi-arch-images).
- **Reintentos.** Las fallas pasajeras de construcción se reintentan una vez (`TestTransientRetry`). Los demás pasos no se reintentan.
