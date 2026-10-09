# Patrones de diseño

Los patrones de abajo son las ideas que sostienen el código. Cada uno tiene un *por qué* y un *dónde*, para que lo vea en el código. Cuando agregue algo, recurra a ellos primero: el código que sigue las formas que ya existen es más fácil de leer para la siguiente persona (incluido usted en el futuro).

## Patrones de arquitectura {#architecture-level-patterns}

### Monolito modular {#modular-monolith}

Un solo programa, muchos paquetes con fronteras claras (`spec`, `render`, `pipeline`, `controller`…). Los paquetes se hablan por interfaces pequeñas, nunca por variables globales compartidas.

- **Por qué:** un solo Deployment es fácil de correr en un clúster de Pi, hay una sola versión en qué pensar, no hay llamadas de red entre componentes ni transacciones distribuidas. Las fronteras de los paquetes lo mantienen *divisible*: el trabajador, los controladores y la API podrían volverse Deployments separados cambiando solo `cmd/rendimiento/main.go` (vea [Escalar](../future/scaling.md)).
- **Dónde:** `cmd/rendimiento/main.go` conecta todo.

### Raíz de composición: inyección de dependencias a mano {#composition-root-dependency-injection-by-hand}

`main.go` lee los ajustes, crea cada objeto concreto (almacén, contenedor de GitHub, proveedor de DNS, ejecutor, corredor, plataforma, controladores, servidor de la API) y le pasa a cada uno lo que necesita por campos de estructura.

```go
--8<-- "cmd/rendimiento/main.go:startup"
```

- **Por qué:** sin marco de inyección de dependencias, sin magia escondida. Leer `main.go` de arriba abajo dice exactamente qué corre y con qué. Las pruebas arman las mismas estructuras con imitaciones.

### Ciclo de conciliación: control por nivel {#reconciliation-loop-level-triggered-control}

Los controladores no reaccionan a *lo que pasó*; comparan *lo que debería ser* (la especificación del `App` o del `Addon`) con *lo que es* (el clúster) y actúan para cerrar la diferencia. Cada evento solo significa "vuelve a mirar".

```go
--8<-- "internal/controller/app_controller.go:reconcile"
```

- **Por qué:** se repara solo. Eventos perdidos, caídas, una persona que corre `kubectl delete`: la siguiente conciliación lo arregla. También es idempotente: correrlo dos veces no hace daño.
- **Dónde:** `AppReconciler.Reconcile`, `AddonReconciler.Reconcile` y, en espíritu, el sincronizador de complementos con git y el ciclo de DNS dinámico ("que así sea", cada cierto tiempo).

### Estado deseado declarativo + aplicación del lado del servidor {#declarative-desired-state-server-side-apply}

El controlador calcula los objetos deseados completos (`render.Render`) y se los entrega a Kubernetes con la **aplicación del lado del servidor** bajo el administrador de campos `rendimiento`. Kubernetes combina, lleva la cuenta de quién es dueño de cada campo e informa los conflictos.

- **Por qué:** sin carreras de leer, modificar y escribir, sin comparar a mano; los campos de otros controladores (las réplicas del escalador automático, las anotaciones de cert-manager) se dejan en paz.

### Núcleo puro, cáscara imperativa {#pure-core-imperative-shell}

`spec`, `detect`, `generate`, `render`, `pipeline.Plan` y `addon.Render` son **puros**: entra algo, sale algo, sin clúster y sin red. La *cáscara* (controladores, ejecutor, manejadores de la API) hace la entrada y salida y los llama.

- **Por qué:** las partes puras tienen la lógica complicada y se prueban con pruebas unitarias y de referencia rápidas. La cáscara es delgada y se prueba con envtest.

### Cola de trabajo en la base de datos (algo así como una bandeja de salida transaccional) {#work-queue-in-the-database-transactional-outbox-ish}

Las ejecuciones son filas. Los trabajadores apartan una con `SELECT … FOR UPDATE SKIP LOCKED`:

```go
--8<-- "internal/store/store.go:claim"
```

- **Por qué:** Postgres ya está ahí; no hay Redis ni RabbitMQ que mantener. `SKIP LOCKED` deja que muchos trabajadores aparten filas distintas sin estorbarse, y una caída deja la fila disponible otra vez (`RequeueOrphans` al arrancar).
- **Una ejecución por aplicación:** `PARALLEL_RUNS` trabajadores (3) apartan a la vez, y cada uno salta las aplicaciones que ya tienen una ejecución en marcha, así la construcción larga de una aplicación nunca detiene la página de otra y las versiones de cada aplicación conservan su orden.

### GitOps para la configuración, base de datos para las versiones {#gitops-for-configuration-database-for-releases}

La *configuración* de una aplicación vive en su repositorio (`rendimiento.yaml`), y la de los complementos en `p0dxD/gitops`. El *apuntador de la versión* (qué huella de imagen corre) vive en el objeto `App` y en la tabla `releases`.

- **Por qué:** la configuración recibe revisión de código e historial; las versiones no crean el ruido de confirmaciones `[skip ci]`, y la reversión es instantánea (apuntar a una huella anterior).

## Patrones de código {#code-level-patterns}

### Interfaces pequeñas, definidas por quien las usa {#small-interfaces-defined-by-the-consumer}

`platform.GitHub`, `pipeline.Executor`, `dns.Provider`, `addon.GitFetcher`, `generate.Generator`. Cada una declara solo los métodos que llama quien la usa. Las implementaciones reales y las imitaciones de las pruebas las cumplen por igual. Vea [Go para este código](go.md#interfaces-the-most-important-idea).

### Estrategia {#strategy}

Una familia de algoritmos intercambiables detrás de una interfaz, elegida al arrancar o en cada llamada:

| Decisión | Estrategias |
|---|---|
| Cómo construir una imagen | Dockerfile o **Railpack** (`build.builder`) |
| Qué proveedor de DNS | `Cloudflare` o `Noop` |
| Cómo se genera un complemento | paquete de Helm o kustomize/manifiestos simples |
| Qué demonio de BuildKit | el grupo (hash de encuentro) o una sola dirección |

### Adaptador {#adapter}

`github.Holder` adapta la API REST de GitHub a `platform.GitHub`; `GitHubFetcher` adapta el árbol de un repositorio a `fs.FS` para que Helm y kustomize lo lean como una carpeta local; el registrador adapta los flujos de registros de Kubernetes a `io.Writer` + el almacén + SSE.

### Contenedor con cambio atómico {#holder-atomic-swap}

Las credenciales de la aplicación de GitHub no existen hasta que termina la configuración. `github.Holder` envuelve un `atomic.Pointer` para que el servidor en marcha pueda poner una aplicación ya configurada sin reiniciar, y cada quien que lo llama ve o "sin configurar" o una aplicación completa, nunca la mitad de una.

### Observador: publicación y suscripción {#observer-publish-subscribe}

`events.Hub` reparte los avisos de cambio (se actualizó una ejecución, cambió el estado de una aplicación) a cada navegador conectado con **eventos enviados por el servidor**. Los productores llaman a `Publish` y no saben quién escucha.

### Opciones sin constructor, con valores predeterminados {#builder-free-options-with-defaults}

Las especificaciones y los ajustes son estructuras simples; un método `Default()` llena los huecos y `Validate()` revisa el resultado, informando **todos** los problemas a la vez (`errors.Join`). Sin cadenas de constructores ni opciones funcionales: el YAML *son* las opciones.

### Método plantilla: las canalizaciones {#template-method-pipelines}

`pipeline.Plan` convierte una especificación en un grafo dirigido de pasos (clonar → probar → construir, por servicio). El corredor recorre cualquier grafo de la misma manera; solo cambian los pasos. Los tipos nuevos de paso se conectan sin tocar el corredor (vea [Recetas](recipes.md#add-a-new-kind-of-ci-step)).

### Hash de encuentro {#rendezvous-hashing}

`buildkitFor` elige un demonio de BuildKit para una imagen sacando el hash de *(imagen, demonio)* y tomando la calificación más alta:

```go
--8<-- "internal/pipeline/kube.go:buildkitFor"
```

- **Por qué:** la misma imagen siempre cae en el mismo demonio (caché caliente), y agregar o quitar un demonio solo mueve las imágenes que le tocaban. No hace falta un coordinador.

### La propiedad y las etiquetas como índice {#ownership-and-labels-as-the-index}

Todo lo que crea rendimiento lleva las etiquetas `app.kubernetes.io/managed-by: rendimiento` y `rendimiento.ai/app` (o `rendimiento.ai/addon`), y referencias de dueño donde se puede. Podar es "enumerar por etiqueta y borrar lo que no se desea"; adoptar es "tomar el control de los objetos que coinciden pero no tienen nuestras etiquetas, solo después de que una persona esté de acuerdo".

### Compuertas de seguridad {#safety-gates}

Los cambios destructivos o sorprendentes se detienen y preguntan: la adopción de objetos existentes, los complementos con `manualSync`, los tipos de `neverDelete` (CRD, Namespaces, PVCs, PVs, StorageClasses), la anotación de pausa, los ganchos pre-delete. El controlador devuelve `errBlocked` y muestra el *porqué* en el estado, en lugar de adivinar.

### Errores centinela y errores envueltos {#sentinel-errors-and-wrapped-errors}

`store.ErrNotFound`, `errBlocked` y `fmt.Errorf("…: %w", err)` por todas partes. Quien llama decide según el *tipo* de fallo con `errors.Is`.

### Incrustar todo {#embed-everything}

La interfaz, las plantillas y las migraciones se incrustan con `//go:embed`. La imagen es un solo programa estático; un despliegue nunca puede mezclar un programa nuevo con archivos viejos.

## Patrones que *no* se usan, a propósito {#patterns-deliberately-not-used}

| No se usa | Por qué no (todavía) |
|---|---|
| Mapeador de objetos (ORM) | El SQL simple con `pgx` es más corto, más rápido, y cada consulta está a la vista. |
| Intermediario de mensajes | La cola en Postgres basta a esta escala; vea [Escalar](../future/scaling.md). |
| Microservicios | Una persona, un clúster: el costo de operarlos superaría por mucho el beneficio. Las fronteras existen para dividir después. |
| Carga de extensiones (paquete `plugin`, WASM) | Hoy los puntos de extensión son interfaces compiladas dentro; el contrato de extensiones (contenedores, JSON de entrada y salida) está en la [hoja de ruta](../future/roadmap.md). |
| Registradores o configuración globales | Todo se pasa explícitamente, así las pruebas pueden correr en paralelo. |
