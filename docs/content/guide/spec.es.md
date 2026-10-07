# Referencia de `rendimiento.yaml`

`rendimiento.yaml`, en la raíz del repositorio de una aplicación, es el único archivo que rendimiento necesita. El asistente escribe una primera versión; después es código como cualquier otro: edítelo en una solicitud de incorporación, y el cambio se despliega al integrarlo. Se analiza de forma **estricta** (un campo desconocido o mal escrito es un error), se completan los valores predeterminados y cada problema se reporta de una vez con su ruta (`services[1].port …`). El tipo en Go es `spec.Spec`, en [`internal/spec/spec.go`](https://github.com/p0dxD/rendimiento.ai/blob/main/internal/spec/spec.go).

## Un ejemplo completo {#a-complete-example}

```yaml
# Todo es opcional excepto services[].name.
postgres: { version: "17", size: 10Gi }       # ajustes para needs: [postgres]
redis: { maxMemory: 128mb }                   # ajustes para needs: [redis]

services:
  - name: web                                 # una etiqueta DNS: minúsculas, dígitos, guiones
    path: web                                 # carpeta que se construye (predeterminado ".")
    port: 3000                                # el puerto en el que escucha la aplicación (predeterminado 8080)
    domain: shop.joserod.space                # dirección HTTPS pública (DNS + certificado)
    aliases: [www.shop.joserod.space]         # más hosts, el mismo certificado
    size: medium                              # small | medium | large
    replicas: 2                               # más de 1 = despliegues sin interrupciones
    health: { path: /api/health }             # disponibilidad + vida
    env: { NODE_ENV: production }
    secrets: [shop-web]                       # secretos completos como variables de entorno
    needs: [redis, { service: api }]          # se inyectan REDIS_URL y API_URL
    test: { image: "node:20", command: "npm ci && npm test" }

  - name: api
    path: api
    port: 8000
    routes: [shop.joserod.space/api]          # una ruta en el host de web
    needs: [postgres]                         # se inyectan DATABASE_URL + PG*
    secretEnv: { STRIPE_KEY: shop-api/stripe } # una variable tomada de la clave de un secreto
    resources: { memory: 512Mi, memoryLimit: 1Gi }
    build: { builder: railpack, start: "uvicorn main:app --host 0.0.0.0 --port 8000" }

  - name: models
    image: dustynv/ollama:r36.4.0             # una imagen lista para usar: no se construye nada
    port: 11434
    gpu: 1
    volume: { size: 20Gi, mount: /root/.ollama }
    lan: { ip: 192.168.1.50 }                 # también se alcanza en la red doméstica
    catalog:
      title: Ollama
      env: OLLAMA_HOST

jobs:
  - name: nightly-report
    schedule: "0 6 * * *"
    timeZone: America/New_York
    service: api                              # se ejecuta con la imagen de api
    command: [python, report.py]

tasks:
  - name: mobile                              # un comando que se ejecuta como paso de integración continua
    image: node:20-bookworm
    path: mobile                              # solo se ejecuta cuando cambia mobile/
    command: npx --yes eas-cli build --platform all --non-interactive --no-wait
    secretEnv: { EXPO_TOKEN: expo/token }
```

## Nivel superior {#top-level}

| Campo | Tipo | Predeterminado | Qué significa |
|---|---|---|---|
| `services` | lista | **obligatorio** | Los servicios de la aplicación (al menos uno). |
| `jobs` | lista | ninguno | Tareas programadas (CronJobs). |
| `builds` | lista | ninguna | Imágenes que se construyen y prueban pero no se despliegan ([Imágenes](#builds)). |
| `tasks` | lista | ninguno | Comandos que se ejecutan como pasos de integración continua ([Tareas](tasks.md)). |
| `verify` | objeto | activa, 5 min, con reversión | Cómo se verifica cada versión después de publicarse: `window` (segundos, 60–3600), `rollback` (`false` solo reporta), `disabled` ([Confiabilidad](reliability.md#verifying-each-release)). |
| `sharedNamespace` | booleano | `false` | El espacio de nombres de la aplicación pertenece a otra cosa (por ejemplo, ArgoCD): debe existir, y rendimiento nunca lo crea, lo etiqueta, se adueña de él ni lo elimina. |
| `postgres` | objeto | vea [necesidades](#postgres-and-redis) | Ajustes para `needs: [postgres]`. |
| `redis` | objeto | vea [necesidades](#postgres-and-redis) | Ajustes para `needs: [redis]`. |

## Servicios {#services}

### Identidad y origen {#identity-and-source}

| Campo | Tipo | Predeterminado | Qué significa |
|---|---|---|---|
| `name` | texto | **obligatorio** | Etiqueta DNS; única en la aplicación. También es el nombre del Deployment y del Service. |
| `path` | texto | `.` | Carpeta del repositorio que se construye, relativa a la raíz. |
| `image` | texto | ninguna | Ejecutar esta imagen lista para usar en lugar de construir (por ejemplo `postgres:15-alpine`). Sin pasos de construcción ni de pruebas. |
| `watch` | lista de rutas | ninguna | Otras carpetas cuyos cambios también deben reconstruir este servicio (código compartido). |
| `language` | texto | detectado | Solo informativo (se muestra en la interfaz). |

### Construcción y pruebas {#build-and-test}

| Campo | Tipo | Predeterminado | Qué significa |
|---|---|---|---|
| `build.builder` | `dockerfile` \| `railpack` | automático | Automático: el Dockerfile si la carpeta tiene uno, si no, Railpack. |
| `build.dockerfile` | texto | `Dockerfile` | Ruta del Dockerfile, relativa a `path`. |
| `build.args` | mapa | ninguno | Argumentos de construcción (los `ARG` del Dockerfile), o entorno de construcción para Railpack. `GIT_SHA` siempre se pasa a los Dockerfiles. |
| `build.start` | texto | detectado | Solo Railpack: el comando de inicio. |
| `test.image` | texto | — | Imagen en la que corren las pruebas. |
| `test.command` | texto | — | Comando de la terminal, ejecutado en la carpeta del servicio; un código de salida distinto de cero hace fallar la ejecución y omite la construcción. |
| `test.size`, `test.resources` | como en el servicio | 250m / 256Mi, límite 2Gi | Las solicitudes y los límites del pod de pruebas. |
| `test.timeout` | segundos | `STEP_TIMEOUT` | Solo más corto que el límite de la plataforma. |
| `test.env` | mapa | ninguno | Variables de entorno para las pruebas. |
| `test.postgres` | booleano | `false` | Un PostgreSQL desechable junto a las pruebas, en `DATABASE_URL` ([Pruebas](builds.md#tests)). |
| `test.cache` | booleano | `false` | Conservar `/cache` entre ejecuciones; Go, npm y pip lo usan ([Pruebas](builds.md#tests)). |

### Ejecución {#running}

| Campo | Tipo | Predeterminado | Qué significa |
|---|---|---|---|
| `port` | entero | `8080` | El puerto en el que escucha la aplicación. `PORT` se define con él. |
| `replicas` | entero 0–10 | `1` | Pods. Más de uno da despliegues sin interrupciones. |
| `size` | `small` \| `medium` \| `large` | `small` | Tamaño predefinido de recursos (abajo). |
| `resources` | objeto | ninguno | Sobrescribir cualquiera de `cpu`, `memory` (solicitudes), `cpuLimit`, `memoryLimit`; un límite puede ser `none`. |
| `health.path` | texto | ninguna | Comprobación HTTP GET de disponibilidad y de vida. |
| `health.tcp` | booleano | `false` | Comprobar que el puerto acepta conexiones (bases de datos, servicios que no son HTTP). |
| `health.timeout` | entero 1–60 | 1 | Segundos por comprobación. |
| `command`, `args` | listas | los de la imagen | Sobrescribir el punto de entrada y los argumentos. |
| `gpu` | entero 0–8 | `0` | GPU que se piden (una sola réplica). El perfil de GPU del clúster agrega la clase de ejecución, los controladores y la memoria compartida. |

Sin `health`, se usa una comprobación TCP de disponibilidad en el puerto, así el tráfico solo llega a un pod cuando ya escucha.

| Tamaño | CPU solicitada | Memoria solicitada | Límite de CPU | Límite de memoria |
|---|---|---|---|---|
| `small` | 50m | 64Mi | 500m | 256Mi |
| `medium` | 100m | 256Mi | 1 | 512Mi |
| `large` | 250m | 512Mi | 2 | 1Gi |

### Configuración y secretos {#configuration-and-secrets}

| Campo | Tipo | Qué significa |
|---|---|---|
| `env` | mapa | Variables de entorno simples. |
| `secrets` | lista de nombres | Cargar Secrets completos como variables de entorno (`envFrom`). Los secretos que falten no bloquean al pod. Los valores se definen en la página **Configuración** de la aplicación o vienen de un secreto sellado. |
| `secretEnv` | mapa `VAR: secreto/clave` | Una variable tomada de una clave de un Secret. |
| `secretFiles` | lista de `{secret, mount}` | Montar un Secret como archivos de solo lectura. |
| `configFiles` | lista de `{configMap, mount}` | Montar un ConfigMap existente como archivos de solo lectura. |

### Almacenamiento {#storage}

| Campo | Tipo | Predeterminado | Qué significa |
|---|---|---|---|
| `volume.size` | cantidad | — | Un volumen nuevo de Longhorn (`<servicio>-data`) de este tamaño. |
| `volume.mount` | ruta | **obligatorio** | Dónde montarlo. |
| `volume.existingClaim` | nombre | ninguno | Montar en su lugar un PersistentVolumeClaim existente (conserva sus datos; se usa al migrar). |
| `volume.fsGroup` | entero | 1001 en imágenes construidas, ninguno en las listas para usar | Grupo que puede escribir en el volumen; `-1` lo desactiva. |

Un servicio con volumen usa la estrategia **Recreate** (un volumen se conecta a un solo pod a la vez), así que sus actualizaciones tienen un hueco breve. Los volúmenes **nunca se eliminan** al quitarlos del archivo.

### Exposición {#exposure}

| Campo | Tipo | Qué significa |
|---|---|---|
| `domain` | nombre de host | Dirección HTTPS pública: una entrada, un certificado y un registro DNS. |
| `aliases` | nombres de host | Más hosts con el mismo certificado (requiere `domain`). |
| `routes` | lista de `host/ruta` | Mandar a este servicio una ruta en un host que pertenece a otro servicio de esta aplicación, por ejemplo `shop.joserod.space/api`. |
| `tlsSecret` | nombre | Nombre del secreto del certificado (predeterminado `<nombre>-tls`); conserve uno existente al migrar. |
| `ingress.name` | nombre | Conservar el nombre de un Ingress existente al migrar. |
| `ingress.annotations` | mapa | Ajustes adicionales `nginx.ingress.kubernetes.io/*` (límites de peticiones, tamaño del cuerpo, CORS…). Se rechazan los fragmentos de código. |
| `ingress.tlsSecrets` | mapa `host: secreto` | Poner algunos hosts en su propio certificado. |
| `streaming` | booleano | Respuestas sin búfer y de larga duración (eventos enviados por el servidor, respuestas de inteligencia artificial en tiempo real, websockets). |
| <a id="lan"></a>`lan.ip` | IPv4 privada | Exponer también en la red doméstica a través de MetalLB, en esta dirección (de su grupo; vacía deja que él la elija). |
| `lan.port` | entero | Puerto en esa dirección (predeterminado 80). La página de la aplicación y el panel ligan a la dirección en cuanto MetalLB la asigna. |

### Necesidades {#needs}

`needs` enumera aquello de lo que depende el servicio. Vea [Necesidades y catálogo de servicios](needs.md).

| Forma | Provee | Se inyecta |
|---|---|---|
| `postgres` | el Postgres de la aplicación | `DATABASE_URL`, `PGHOST`, `PGPORT`, `PGUSER`, `PGPASSWORD`, `PGDATABASE` |
| `redis` | la caché Redis de la aplicación | `REDIS_URL` |
| `{service: espacio/nombre}` o `{service: nombre}` | la dirección de otro servicio | `<NOMBRE>_URL` |
| cualquiera de las anteriores con `env: VAR` (`{postgres: {env: DB_URL}}`) | lo mismo | en `VAR` |

Las variables que usted define en `env` o en `secretEnv` siempre tienen prioridad.

### Catálogo {#catalog}

Cómo se describe el servicio en la página Servicios, para las demás aplicaciones:

| Campo | Qué significa |
|---|---|
| `catalog.title`, `catalog.description` | Se muestran en lugar del nombre. |
| `catalog.category` | `database`, `messaging`, `ai`, `storage`, `monitoring`, `web`, `devtools` o `platform` (si no, se deduce). |
| `catalog.env` | La variable en la que quienes lo llaman suelen poner la dirección (por ejemplo `OLLAMA_HOST`). |
| `catalog.path` | Se agrega a la dirección en el valor sugerido (por ejemplo `/analyze`). |
| `catalog.docs` | Liga a la documentación de la API. |
| `catalog.endpoints` | Líneas cortas como `POST /analyze: classify a message`. |

## Postgres y Redis {#postgres-and-redis}

| Campo | Predeterminado | Qué significa |
|---|---|---|
| `postgres.version` | `17` | Versión mayor de `postgres:<v>-alpine`. |
| `postgres.size` | `5Gi` | Tamaño del volumen. |
| `postgres.resources` | 100m / 256Mi, límite 512Mi | Sobrescrituras. |
| `redis.version` | `7` | Versión de `redis:<v>-alpine`. |
| `redis.maxMemory` | `64mb` | Tamaño de la caché; se desalojan las claves menos usadas recientemente. |
| `redis.resources` | 50m / 64Mi, límite 256Mi | Sobrescrituras. |

## Tareas programadas {#jobs}

| Campo | Tipo | Predeterminado | Qué significa |
|---|---|---|---|
| `name` | texto ≤ 52 | **obligatorio** | Único entre tareas programadas y servicios. |
| `schedule` | cron | **obligatorio** | Cron estándar de 5 campos o del estilo `@daily`. |
| `timeZone` | nombre IANA | hora del clúster (UTC) | Por ejemplo `America/New_York`. |
| `service` / `path` / `image` | exactamente uno | — | Usar la imagen publicada de un servicio, construir esta carpeta o ejecutar una imagen lista para usar. |
| `watch`, `build` | como en los servicios | | Para las tareas programadas con `path`. |
| `command`, `args` | listas | | Qué ejecutar. |
| `size`, `resources` | como en los servicios | `small` | |
| `timeout` | segundos | ninguno | Detener una ejecución pasado este tiempo. |
| `env`, `secretEnv`, `secrets` | como en los servicios | | |

## Imágenes (builds) {#builds}

Imágenes que se construyen en cada ejecución como las de un servicio, pero no se despliegan; sus resúmenes (digests) se guardan con cada versión ([Imágenes que no son servicios](builds.md#images-that-are-not-services)).

| Campo | Tipo | Predeterminado | Qué significa |
|---|---|---|---|
| `name` | texto ≤ 40 | **obligatorio** | Único entre servicios, tareas programadas, imágenes y tareas. Los pasos son `<nombre>:test` y `<nombre>:build`; la imagen es `<registro>/<aplicación>-<nombre>`. |
| `path`, `watch` | como en los servicios | `.` | Qué se construye y qué cuenta como cambio. |
| `build` | como en los servicios | `Dockerfile` | |
| `test` | como en los servicios | ninguna | Corre antes de la construcción. |

## Tareas {#tasks}

Comandos que se ejecutan como pasos de integración continua; vea [Tareas](tasks.md) para saber cuándo corren y cómo funcionan los secretos.

| Campo | Tipo | Predeterminado | Qué significa |
|---|---|---|---|
| `name` | texto ≤ 40 | **obligatorio** | Único entre servicios, tareas programadas, imágenes y tareas. El paso es `<nombre>:task`. |
| `stage` | `build` \| `pre-deploy` \| `post-deploy` | `build` | `build`: un paso de integración continua. `pre-deploy`: después de la construcción, antes del despliegue; una falla detiene la versión ([previas al despliegue](tasks.md#pre-deploy-tasks)). `post-deploy`: contra la versión en vivo, como parte de la verificación; una falla la revierte ([posteriores al despliegue](tasks.md#post-deploy-tasks)). |
| `service` | nombre de servicio | — | Previas y posteriores al despliegue: ejecutar con la imagen nueva de este servicio y su entorno (en lugar de `image`). |
| `image` | imagen | **obligatorio** (build) | En qué corre el comando. Las tareas posteriores al despliegue indican `image` o `service`. |
| `command` | texto | **obligatorio** | Se ejecuta con `sh -c`. |
| `path` | texto | `.` | Carpeta de trabajo, relativa a la raíz del repositorio; también lo que cuenta como cambio. |
| `watch` | lista de rutas | ninguna | Más rutas cuyos cambios ejecutan la tarea. |
| `after` | lista | ninguna | Servicios e imágenes (su construcción) y tareas a los que hay que esperar. |
| `when` | `deploy` \| `always` | `deploy` | `deploy`: solo los envíos a la rama principal. `always`: cada ejecución, incluidas las ramas y las solicitudes de incorporación. (No `on:`, que YAML lee como `true`.) |
| `optional` | booleano | `false` | Una falla no hace fallar la ejecución. |
| `size`, `resources` | como en los servicios | `medium` | |
| `timeout` | segundos | `STEP_TIMEOUT` | Solo puede ser menor que el límite de la plataforma. |
| `env`, `secrets`, `secretEnv` | como en los servicios | | Los secretos se leen del espacio de nombres de la aplicación cuando empieza el paso. |
