# Construcciones y pruebas

## Qué inicia una construcción {#what-triggers-a-build}

| Evento | Construye | Despliega | Comprobación en GitHub |
|---|---|---|---|
| envío a la rama principal | los servicios que cambiaron (vea abajo) | sí, si todo sale bien | sí |
| envío a cualquier otra rama (incluidas las de solicitudes de incorporación y las de Renovate) | todos los servicios | no | sí, en la solicitud |
| botón **Construir ahora** (rama principal) | todos los servicios | sí | sí |
| solicitud de incorporación desde una bifurcación | nada | — | — |

## Dockerfile o Railpack {#dockerfile-or-railpack}

| La carpeta del servicio tiene… | rendimiento usa… |
|---|---|
| un `Dockerfile` (o `build.dockerfile` apunta a uno) | el Dockerfile, con `GIT_SHA` como argumento de construcción |
| ningún Dockerfile | **Railpack**, que detecta el lenguaje y construye desde el código fuente |
| cualquiera de los dos, con `build.builder: dockerfile` o `railpack` | lo que usted haya pedido |

Railpack es la forma más rápida de empezar; un Dockerfile da imágenes más pequeñas y reforzadas, y control total. [Detalles](../architecture/builds.md#railpack-no-dockerfile-needed). La primera línea del registro de una construcción dice qué constructor se usó; la siguiente, qué demonio de BuildKit.

## Pruebas {#tests}

```yaml
test:
  image: python:3.12-slim
  command: pip install -r requirements.txt && pytest -q
```

La prueba corre en su propio pod, en la carpeta del servicio de un clon recién hecho, al mismo tiempo que la construcción. Si falla, detiene la construcción (*detenido: falló …*), la ejecución falla, no se publica nada y GitHub muestra una comprobación en rojo. Los pods de pruebas tienen el mismo aislamiento de red que las construcciones (internet para las dependencias, nada dentro del clúster).

Un conjunto de pruebas más grande puede pedir más:

```yaml
test:
  image: golang:1.27
  command: go test ./...
  resources: { cpu: "1", memory: 2Gi, memoryLimit: 5Gi }   # o size: large
  timeout: 1800
  env: { CGO_ENABLED: "0" }
  postgres: true    # una base de datos desechable: DATABASE_URL
  cache: true       # /cache se conserva entre ejecuciones
```

- **Recursos.** Sin `size` ni `resources`, una prueba pide 250m de CPU y 256 MiB, y puede usar hasta 2 GiB; `size` y `resources` funcionan como en los servicios.
- **`postgres: true`** arranca un PostgreSQL vacío junto a las pruebas, en el mismo pod; las pruebas empiezan cuando acepta conexiones, y desaparece cuando terminan. Su dirección está en `DATABASE_URL` (`postgres://postgres:test@127.0.0.1:5432/test`).
- **`cache: true`** conserva un volumen en `/cache` de una ejecución a la siguiente, para que las dependencias no se descarguen y compilen cada vez. Go, npm y pip apuntan ahí (`GOCACHE`, `GOMODCACHE`, `npm_config_cache`, `PIP_CACHE_DIR`, `XDG_CACHE_HOME`); otras herramientas pueden usar `/cache` por su cuenta. Hay un volumen por aplicación y prueba, que se borra con la aplicación. Las ejecuciones de ramas y de solicitudes de incorporación lo comparten, así que una caché solo acelera las pruebas: ninguna prueba debe confiar en lo que encuentre ahí. Las imágenes que se despliegan las construye BuildKit y nunca la leen.

## Imágenes que no son servicios {#images-that-are-not-services}

`builds:` enumera las imágenes que produce el repositorio y que ningún servicio de la aplicación ejecuta: una imagen que usan otras aplicaciones o herramientas o, en el repositorio de rendimiento, rendimiento mismo. Cada una se prueba y se construye como un servicio (con los mismos campos `path`, `watch`, `build` y `test`), y su resumen (digest) se guarda con la versión, pero no se despliega nada a partir de ella.

```yaml
builds:
  - name: platform
    path: .
    build: { dockerfile: Dockerfile }
    test: { image: golang:1.27, command: go test ./..., postgres: true, cache: true }
```

La imagen se publica como `<registro>/<aplicación>-<nombre>`; la página de la ejecución muestra `platform:test` y `platform:build`. [Cómo se construye rendimiento a sí mismo](../environment/deploy.md#rendimiento-builds-itself).

## Solo se vuelve a construir lo que cambió {#only-what-changed-is-rebuilt}

En un envío a la rama principal, un servicio cuya carpeta no cambió desde la última versión conserva su imagen; sus pasos aparecen como *reutilizados*. Enumere con `watch:` otras carpetas de las que depende:

```yaml
services:
  - name: api
    path: api
    watch: [shared]        # los cambios en shared/ también reconstruyen api
```

Todo se vuelve a construir cuando cambia `rendimiento.yaml`, en las ejecuciones manuales, para los servicios en la raíz del repositorio, o cuando la comparación no es segura.

## Cuánto tardan las construcciones {#how-long-builds-take}

La primera construcción de un servicio es la más lenta: se descargan las imágenes base y las dependencias, y los paquetes nativos (las ruedas de Python, por ejemplo) pueden compilarse. Después, cada imagen se construye siempre en el mismo demonio de BuildKit y reutiliza su caché; un cambio solo de código suele tardar de unos segundos a un par de minutos. Varias aplicaciones construyendo a la vez usan nodos distintos. Un paso puede durar hasta 45 minutos (`STEP_TIMEOUT`), y como máximo corren 4 a la vez en todo el clúster (`MAX_PARALLEL_STEPS`).

## Leer una construcción fallida {#reading-a-failed-build}

La página de la ejecución muestra el registro de cada paso. En las construcciones, la salida de BuildKit muestra cada paso del Dockerfile (o de Railpack), su estado de caché (`CACHED` o el tiempo que tardó) y, si falla, la salida del comando. La comprobación de GitHub lleva a la ejecución.
