# Go para este código

Este capítulo enseña el Go que necesita para leer y cambiar rendimiento, con su propio código como ejemplo. Si conoce otro lenguaje (Python, JavaScript, Java), reconocerá casi todas las ideas; las diferencias son de lo que trata este capítulo.

## Por qué Go aquí {#why-go-here}

- **Es el lenguaje de Kubernetes.** El cliente de Kubernetes (`client-go`), controller-runtime, Helm y kustomize son bibliotecas de Go. rendimiento las *importa*: genera los paquetes de Helm con el propio código de Helm y las kustomizations con el de kustomize, en lugar de llamar a herramientas externas.
- **Un solo programa estático.** `go build` produce un solo ejecutable sin un entorno de ejecución que instalar (ni JVM, ni Node, ni Python). La imagen del contenedor es ese archivo sobre una base casi vacía.
- **Concurrencia fácil.** Las gorrutinas y los canales hacen que "corre estos pasos en paralelo, máximo cuatro a la vez" sean unas cuantas líneas.
- **Sencillo, explícito y rápido de compilar.** Pocas funciones del lenguaje, un formato estricto (`gofmt`) y los errores como valores hacen que el código se lea fácil meses después, y que sea fácil contribuir para quien llega nuevo.

## Módulos y paquetes {#modules-and-packages}

Un **módulo** es un proyecto con versiones: `go.mod` en la raíz le da nombre (`github.com/p0dxD/rendimiento.ai`) y enumera sus dependencias con versiones exactas (`go.sum` guarda sus sumas de verificación). `go mod tidy` mantiene los dos en orden.

Un **paquete** es una carpeta de archivos `.go` que comparten un nombre de `package`. Cada archivo empieza con él y con sus importaciones:

```go
package controller

import (
	"context"                                   // biblioteca estándar
	appsv1 "k8s.io/api/apps/v1"                 // una dependencia, con otro nombre
	"github.com/p0dxD/rendimiento.ai/internal/render" // otro paquete nuestro
)
```

- **`internal/`** es especial: los paquetes que están debajo solo se pueden importar desde este módulo. Todo excepto `api/v1alpha1` vive ahí, así el código puede cambiar con libertad sin descomponer nada de afuera.
- **`cmd/rendimiento`** tiene `package main` y `func main()`: el punto de entrada del programa.
- **Las mayúsculas son la visibilidad.** `Render` (con mayúscula) se exporta y se puede usar desde otros paquetes; `render` (con minúscula) es privado de su paquete. No hay palabras clave `public` o `private`.

## Tipos: estructuras y métodos {#types-structs-and-methods}

Una **estructura** agrupa campos. Los nombres en JSON y YAML vienen de las **etiquetas de estructura**:

```go
type Health struct {
	Path    string `json:"path,omitempty"`    // comprobación HTTP
	TCP     bool   `json:"tcp,omitempty"`     // comprobación TCP
	Timeout int    `json:"timeout,omitempty"` // segundos
}
```

Los **métodos** son funciones con un *receptor*. Un receptor de apuntador (`*Spec`) puede modificar el valor; un receptor de valor trabaja sobre una copia:

```go
func (s *Spec) Default()        { /* llena los valores predeterminados en su lugar */ }
func (s Size) Resources() Resources { return sizes[s] }
```

No hay clases ni herencia. El comportamiento se une a los tipos con métodos, y se comparte con **interfaces** y **composición** (incrustar una estructura en otra, como `AddonReconciler` incrusta `client.Client` y así obtiene sus métodos `Get`, `List` y `Update`).

## Interfaces: la idea más importante {#interfaces-the-most-important-idea}

Una interfaz es un conjunto de firmas de métodos. **Cualquier** tipo que tenga esos métodos la cumple, sin tener que declararlo. Eso permite que el código dependa de *lo que algo hace* y no de *lo que es*:

```go
--8<-- "internal/dns/dns.go:provider"
```

El controlador guarda un `dns.Provider`. En producción es `*dns.Cloudflare`; sin token es `dns.Noop{}`; en las pruebas es una imitación que anota las llamadas en un mapa. El código del controlador es el mismo en los tres casos. Esta sola idea está detrás de casi toda la facilidad para probar rendimiento:

| Interfaz | Implementación real | Implementación de prueba |
|---|---|---|
| `dns.Provider` | `Cloudflare` | `fakeDNS` |
| `pipeline.Executor` | `KubeExecutor` (pods) | `fakeExec` (espera, anota el orden) |
| `platform.GitHub` | `github.Holder` (API de GitHub) | `fakeGitHub` (en memoria) |
| `addon.GitFetcher` | `GitHubFetcher` | `fakeGit` (`fstest.MapFS`) |
| `generate.Generator` | `Templates` | — (en el futuro: un generador con IA) |
| `io.Writer`, `fs.FS` | los registradores, un árbol de GitHub | búferes, árboles de archivos en memoria |

La costumbre en Go: **acepte interfaces, devuelva tipos concretos**, y mantenga las interfaces pequeñas y definidas donde se *usan* (la plataforma declara la interfaz `GitHub` con exactamente los métodos que llama).

## Los errores son valores {#errors-are-values}

Las funciones devuelven un `error` como último resultado; quien llama lo revisa de inmediato. No hay excepciones:

```go
raw, err := p.GitHub.FileAt(ctx, app.InstallationID, app.Repo, spec.FileName, ref)
if err != nil {
	return nil, fmt.Errorf("read %s: %w", spec.FileName, err)   // agrega contexto, conserva la causa
}
```

- **`%w`** envuelve la causa, así quien llama todavía puede preguntar por ella con **`errors.Is`** (un valor específico) o **`errors.As`** (un tipo específico).
- **Los errores centinela** nombran una condición: `store.ErrNotFound`, o el `errBlocked` del controlador ("necesita a una persona; reintenta despacio"):

```go
var errBlocked = errors.New("blocked")
...
if err != nil && !errors.Is(err, errBlocked) {
	return ctrl.Result{}, err // se reintenta con espera creciente
}
```

- **`errors.Join`** junta muchos errores en uno: `spec.Validate` informa todos los problemas de un archivo a la vez.

## `context.Context`: cancelación y plazos {#contextcontext-cancellation-and-deadlines}

Casi toda función que hace entrada o salida recibe primero un `ctx context.Context`. Lleva un **plazo** y una **señal de cancelación** por toda la cadena de llamadas: cuando se cancela una ejecución o se le acaba el tiempo a un paso, su contexto se cancela y cada llamada HTTP, petición a Kubernetes y ciclo de espera que cuelga de él se detiene.

```go
ctx, cancel := context.WithTimeout(ctx, k.Timeout)   // un paso puede correr como máximo STEP_TIMEOUT
defer cancel()                                        // siempre libere el temporizador
```

`defer` corre una llamada cuando la función que la rodea regresa, regrese como regrese: es la costumbre para limpiar (cerrar archivos, borrar un pod de construcción, soltar un candado).

## Concurrencia: gorrutinas, canales y candados {#concurrency-goroutines-channels-mutexes}

Una **gorrutina** es un hilo ligero: `go f()` corre `f` de forma concurrente. Miles de ellas son baratas. El corredor de integración continua arranca una por paso.

Un **canal** pasa valores entre gorrutinas, y se puede usar como señal o como semáforo:

```go
sem := make(chan struct{}, maxParallel) // un canal con búfer de maxParallel lugares
sem <- struct{}{}                        // toma un lugar (se bloquea si todos están ocupados)
defer func() { <-sem }()                 // lo devuelve
```

El corredor usa exactamente esto para limitar los pasos simultáneos, y un canal cerrado por paso (`close(done[s.ID])`) para avisar a los que dependen de él que un paso terminó. **`select`** espera en varios canales a la vez (un resultado, un temporizador o la cancelación).

Un **`sync.Mutex`** protege los datos compartidos (el mapa de suscriptores del concentrador de eventos, la bandera de "en marcha" del corredor de Renovate). **`sync.WaitGroup`** espera a que termine un grupo de gorrutinas. **`sync/atomic`** guarda la aplicación de GitHub en un `Holder` que se puede cambiar en marcha después de la configuración.

Reglas prácticas que sigue el código: nunca comparta un mapa entre gorrutinas sin un candado; nunca se bloquee mientras tiene un candado; siempre dele a las gorrutinas una forma de detenerse (un contexto).

## Incrustar archivos: `//go:embed` {#embedding-files-goembed}

Los archivos se pueden compilar *dentro* del programa:

```go
--8<-- "web/embed.go"
```

La interfaz (`web/dist`), las plantillas de Dockerfile (`templates/`) y las migraciones de SQL (`internal/store/migrations`) se incrustan así, por eso la plataforma es un solo archivo.

## Genéricos {#generics}

Desde Go 1.18, las funciones pueden recibir parámetros de tipo. rendimiento los usa con moderación, para ayudantes pequeñitos:

```go
func ptr[T any](v T) *T { return &v }   // ptr(false), ptr(60 * time.Second)
```

## Pruebas {#testing}

Las pruebas viven junto al código en archivos `*_test.go` y se corren con `go test ./...`. Una prueba es una función `func TestX(t *testing.T)`; `t.Errorf` anota un fallo y sigue, `t.Fatalf` detiene la prueba. La forma común es la **prueba con tablas**:

```go
for _, tc := range []struct{ yaml, want string }{
	{"services: [{name: a, needs: [mysql]}]", "not something rendimiento provides"},
	{"services: [{name: postgres}, {name: b, needs: [postgres]}]", "rename the service"},
} {
	if _, err := Parse([]byte(tc.yaml)); err == nil || !strings.Contains(err.Error(), tc.want) {
		t.Errorf("%s: got %v, want %q", tc.yaml, err, tc.want)
	}
}
```

Ayudantes estándar que se usan por todos lados: `httptest.NewServer` (un servidor HTTP de imitación, p. ej. un repositorio de Helm), `fstest.MapFS` (un árbol de archivos en memoria), `t.TempDir()`. Más en [Pruebas](testing.md).

## Herramientas {#tooling}

| Comando | Hace |
|---|---|
| `gofmt -w .` | Da formato al código de la única manera estándar (córralo antes de cada confirmación). |
| `go vet ./...` | Encuentra código sospechoso (verbos de formato equivocados, código inalcanzable…). |
| `go build ./...` | Compila todo. |
| `go test ./pkg/...` | Corre las pruebas. |
| `go mod tidy` | Pone `go.mod`/`go.sum` de acuerdo con las importaciones. |
| `go doc pkg.Name` | Muestra la documentación de los comentarios. |

**Los comentarios de documentación** empiezan con el nombre que describen (`// Render returns every object…`). Cada nombre exportado de este repositorio tiene uno; el [mapa del código](../reference/code-map.md) se genera a partir de ellos.

## Construir un programa estático {#building-a-static-binary}

```bash
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o rendimiento ./cmd/rendimiento
```

`CGO_ENABLED=0` evita enlazar código de C, así el programa no necesita bibliotecas del sistema. `GOOS`/`GOARCH` permiten compilar para otra plataforma: `GOOS=linux GOARCH=amd64 go build …` en una Pi arm64 produce un programa x86, sin emulación.

## Dónde aprender más {#where-to-learn-more}

- *[A Tour of Go](https://go.dev/tour/)* (una tarde) y *[Effective Go](https://go.dev/doc/effective_go)*.
- *[Go by Example](https://gobyexample.com/)* para recetas rápidas.
- El [libro de controller-runtime](https://book.kubebuilder.io/) (Kubebuilder) para controladores y CRD.
