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

La prueba corre en su propio pod, en la carpeta del servicio de un clon recién hecho, antes de la construcción. Si falla, la construcción queda *omitida*, la ejecución falla y GitHub muestra una comprobación en rojo. Los pods de pruebas tienen 2 GiB de memoria y el mismo aislamiento de red que las construcciones (internet para las dependencias, nada dentro del clúster).

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
