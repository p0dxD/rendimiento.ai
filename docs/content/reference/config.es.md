# Ajustes

rendimiento se configura solo con **variables de entorno**, que se leen una vez al arrancar en `cmd/rendimiento/main.go`. En un clúster vienen del ConfigMap `rendimiento` y de unos cuantos Secrets (`deploy/rendimiento.yaml` trae valores de ejemplo; guarde los valores de un clúster real en una capa privada, vea [Cómo se despliega la plataforma](../environment/deploy.md#your-own-settings)). Para cambiar un ajuste, edite el ConfigMap y corra `kubectl -n rendimiento-system rollout restart deploy/rendimiento`.

## Básicos {#core}

| Variable | Predeterminado | Ejemplo | Significado |
|---|---|---|---|
| `DATABASE_URL` | *(obligatorio)* | del Secret `rendimiento-db` | cadena de conexión a Postgres |
| `BASE_URL` | `http://localhost:8080` | `https://rendimiento.example.com` | la URL pública; se usa para las respuestas de OAuth, los avisos web y las ligas en las comprobaciones de GitHub |
| `ALLOWED_USERS` | *(vacío: nadie)* | `su-cuenta-de-github` | las cuentas de GitHub que pueden iniciar sesión (separadas por comas o espacios) |
| `SETUP_TOKEN` | — | del Secret `rendimiento-setup` | protege `/api/setup/github` hasta que exista la aplicación de GitHub |
| `NAMESPACE` | `rendimiento-system` | | donde viven la plataforma y sus Secrets |
| `LISTEN` | `:8080` | | la dirección HTTP (API, interfaz, avisos web) |
| `METRICS_ADDR` | `:9090` | | métricas de Prometheus (controller-runtime) |
| `LEADER_ELECTION` | `true` | `false` | una sola réplica activa; apagado porque una réplica con Recreate nunca se encima |

## GitHub {#github}

| Variable | Predeterminado | Significado |
|---|---|---|
| `GITHUB_APP_NAME` | `rendimiento` | el nombre de la aplicación, que se usa al crearla con el flujo de manifiesto (`rendimiento` aquí) |
| `GITHUB_SECRET` | `rendimiento-github` | el Secret donde se guardan las credenciales de la aplicación después de la configuración |

## Construcciones {#builds}

| Variable | Predeterminado | Ejemplo | Significado |
|---|---|---|---|
| `REGISTRY` | `registry.example.lan:5000` | | a donde se envían las imágenes y las cachés de construcción |
| `REGISTRY_INSECURE` | `true` | | registro sobre HTTP simple |
| `BUILD_NAMESPACE` | `rendimiento-builds` | | donde corren los pods de construcción y de pruebas |
| `BUILDKIT_POOL` | *(vacío)* | `buildkitd-pool.devops-tools.svc.cluster.local` | el Service sin IP propia del grupo de demonios; las imágenes se reparten en él por hash de encuentro |
| `BUILDKIT_ADDR` | `tcp://buildkitd.devops-tools.svc.cluster.local:1234` | | el demonio único, que se usa cuando no hay grupo o no se puede resolver |
| `BUILDKIT_IMAGE` | `moby/buildkit:v0.18.2` | | la imagen del cliente `buildctl` en los pods de construcción |
| `MAX_PARALLEL_STEPS` | `2` | `4` | los pasos que corren a la vez, entre todas las ejecuciones |
| `STEP_TIMEOUT` | `45m` | | lo más que puede durar un paso (≥ 1m) |
| `BUILD_EXCLUDE_NODES` | *(vacío)* | `gpu-node` | los nodos que deben evitar los pods de construcción |
| `RAILPACK_IMAGE` | *(vacío: Railpack apagado)* | `registry.example.lan:5000/rendimiento-railpack:0.40.0` | la imagen con la herramienta de Railpack, para los servicios sin Dockerfile |
| `RAILPACK_FRONTEND` | *(vacío)* | `ghcr.io/railwayapp/railpack-frontend:v0.40.0` | la interfaz de entrada de BuildKit que corresponde a esa versión |

## Generar las aplicaciones {#rendering-apps}

| Variable | Predeterminado | Significado |
|---|---|---|
| `INGRESS_CLASS` | `nginx` | el `ingressClassName` de las entradas de las aplicaciones |
| `CLUSTER_ISSUER` | `letsencrypt-prod` | el emisor de cert-manager para los certificados de las aplicaciones |
| `STORAGE_CLASS` | `longhorn` | la clase de almacenamiento de los volúmenes de `volume:` y de las bases de datos de `needs:` |
| `GPU_RESOURCE` | *(vacío)* | el nombre del recurso que piden los servicios con `gpu:` (`nvidia.com/gpu`) |
| `GPU_RUNTIME_CLASS` | *(vacío)* | la clase de entorno de ejecución para los pods con GPU (`nvidia`) |
| `GPU_HOST_PATHS` | *(vacío)* | carpetas del nodo montadas en solo lectura en los pods con GPU (las bibliotecas del controlador) |
| `GPU_ENV` | *(vacío)* | `KEY=value;KEY=value` que se agregan a los contenedores con GPU |
| `GPU_SHARED_MEMORY` | *(vacío)* | el tamaño de `/dev/shm` en los pods con GPU |

## DNS {#dns}

Se toman del Secret opcional `rendimiento-dns`. Sin token, la automatización de DNS está apagada (`dns.Noop`).

| Variable | Predeterminado | Significado |
|---|---|---|
| `CLOUDFLARE_API_TOKEN` | — | un token con *Zone:DNS:Edit* |
| `DNS_TARGET` | — | a dónde apuntan los nombres de host de las aplicaciones; una IP enciende el DNS dinámico |
| `DNS_ZONE` | — | la zona predeterminada que ofrece el asistente (`example.com`) |
| `DNS_PROXIED` | `false` | crear los registros detrás del proxy de Cloudflare (`true` aquí) |
| `DDNS_INTERVAL` | `5m` | cada cuánto revisa la IP pública el DNS dinámico (≥ 1m) |

## Comprobaciones de disponibilidad {#uptime-checks}

| Variable | Predeterminado | Significado |
|---|---|---|
| `UPTIME_INTERVAL` | `1m` | Cada cuánto se comprueba cada servicio ([Confiabilidad](../guide/reliability.md)); `0` apaga las comprobaciones. Al menos `10s`. |
| `VERIFY_WINDOW` | `5m` | Cuánto tiempo se vigila cada versión nueva antes de contarla como verificada ([verificar las versiones](../guide/reliability.md#verifying-each-release)); `0` apaga la verificación y la reversión automática. Al menos `1m`. |

## Avisos {#notifications}

| Variable | Predeterminado | Ejemplo | Significado |
|---|---|---|---|
| `NOTIFY_EMAIL_TO` | *(vacío: apagado)* | `you@example.com` | quién recibe los [correos de aviso](../guide/notifications.md), separados por comas |
| `NOTIFY_LANG` | `en` | `es` | el idioma de los correos: `en`, o `es` para español de México |
| `NOTIFY_EMAIL_FROM` | `rendimiento <alerts@joserod.space>` | | el remitente; debe estar en un dominio verificado en Resend |
| `RESEND_API_KEY` | — | del Secret `rendimiento-notify` | la llave de la API de Resend |

## Estadísticas públicas {#public-stats}

| Variable | Predeterminado | Ejemplo | Significado |
|---|---|---|---|
| `PUBLIC_STATS` | `false` | `true` | servir [`GET /api/public/stats`](../guide/reliability.md#public-stats) sin iniciar sesión |
| `STATS_LISTEN` | *(vacío: en el puerto principal)* | `:8081` | servir las estadísticas públicas solo en este puerto interno, no en el público |
| `PUBLIC_STATS_ORIGINS` | *(ninguno)* | | los orígenes de navegador que pueden pedirlas (CORS); no hace falta si se piden desde un servidor |
| `PUBLIC_STATS_TZ` | `UTC` | `America/New_York` | la zona horaria en la que se cuentan sus días |

## Archivo de registros {#log-archive}

| Variable | Predeterminado | Ejemplo | Significado |
|---|---|---|---|
| `LOG_ARCHIVE_ENDPOINT` | *(vacío: apagado)* | `garage.garage.svc.cluster.local:3900` | el `host:puerto` compatible con S3 donde se [archivan los registros de los pasos](../architecture/data.md#the-log-archive) |
| `LOG_ARCHIVE_BUCKET` | `rendimiento-logs` | | la cubeta |
| `LOG_ARCHIVE_SECURE` | `false` | | usar HTTPS |
| `LOG_ARCHIVE_ACCESS_KEY`, `LOG_ARCHIVE_SECRET_KEY` | — | del Secret `rendimiento-logs` | las credenciales de un usuario que puede usar la cubeta |
| `LOG_RETENTION_DAYS` | `365` | | los registros archivados se borran después de estos días (una regla de ciclo de vida en la cubeta) |

## Complementos {#add-ons}

| Variable | Predeterminado | Significado |
|---|---|---|
| `ADDONS_REPO` | `p0dxD/gitops` | el repositorio de git donde se declaran los complementos (`addons/*.yaml`) |
| `ADDONS_IDENTITY` | `system:serviceaccount:<NAMESPACE>:rendimiento-addons` | la identidad con la que se aplican los objetos de los complementos (suplantación) |

## Los secretos que lee la plataforma {#secrets-the-platform-reads}

| Secret (en `rendimiento-system`) | Llaves | Lo escribe |
|---|---|---|
| `rendimiento-db` | `password` | usted, una vez |
| `rendimiento-setup` | `token` | usted, una vez |
| `rendimiento-github` | ID de la aplicación, llave privada, secreto de avisos web, cliente OAuth | el flujo de configuración |
| `rendimiento-dns` | `CLOUDFLARE_API_TOKEN`, `DNS_TARGET` | usted |
| `rendimiento-notify` | `RESEND_API_KEY` | usted |
| `rendimiento-logs` | `LOG_ARCHIVE_ACCESS_KEY`, `LOG_ARCHIVE_SECRET_KEY` | usted (una llave de almacenamiento limitada a la cubeta) |
