# API HTTP

Todas las rutas se registran en `Server.Handler` (`internal/api/server.go`). La interfaz usa esta API y nada más, así que todo lo que hace la interfaz se puede automatizar con guiones.

- **Autenticación:** una galleta de sesión que pone el inicio de sesión con GitHub (`/api/auth/login`). Las rutas marcadas con 🔒 devuelven `401` sin ella, y `403` para los usuarios que no están en `ALLOWED_USERS`.
- **Formato:** JSON de entrada y de salida. Los errores son `{"error": "mensaje"}` con el código de estado correspondiente.
- **Actualizaciones en vivo:** las rutas `…/events` son flujos de **eventos enviados por el servidor** (`text/event-stream`). La entrada desactiva el búfer para ellas.
- **Idioma:** con `Accept-Language: es`, los mensajes de la plataforma llegan en español de México.

## Públicas {#public}

| Método | Ruta | Hace |
|---|---|---|
| GET | `/healthz` | `ok`; las sondas de disponibilidad y de vida |
| GET | `/api/public/stats` | números agregados para una página pública; solo con `PUBLIC_STATS=true`, y solo en el puerto interno `STATS_LISTEN` cuando está definido ([qué contiene](../guide/reliability.md#public-stats)) |
| GET | `/api/public/activity` | el *Ahora mismo* de la página de bienvenida: las ejecuciones en cola o en curso y las 8 versiones más recientes, solo con el nombre de la aplicación y la hora (sin repositorios, confirmaciones, ramas ni mensajes); solo con `PUBLIC_ACTIVITY=true` |
| POST | `/api/webhooks/github` | los avisos web de la aplicación de GitHub (envío, solicitud de incorporación, instalación), verificados con HMAC |
| GET | `/api/auth/login` | redirige al OAuth de GitHub |
| GET | `/api/auth/callback` | la respuesta de OAuth; crea la sesión |
| POST | `/api/auth/logout` | termina la sesión |
| GET | `/api/setup/status` | si la aplicación de GitHub está configurada |
| GET | `/api/setup/github` | inicia el flujo de manifiesto de la aplicación (necesita `?token=SETUP_TOKEN`) |
| POST | `/mcp` | la puerta MCP para agentes ([la Ruta](../guide/ruta.md)); necesita una llave de agente como token portador, no una sesión |
| GET | `/api/setup/github/callback` | GitHub devuelve las credenciales de la aplicación nueva; se guardan en un Secret |

## Cuenta y entorno 🔒 {#account-and-environment}

| Método | Ruta | Hace |
|---|---|---|
| GET | `/api/me` | la cuenta con la sesión iniciada |
| GET | `/api/installations` | las instalaciones de la aplicación de GitHub |
| GET | `/api/installations/{id}/repos` | los repositorios de una instalación |
| GET | `/api/zones` | las zonas DNS que el token puede editar |
| GET | `/api/environment` | las comprobaciones de la página Entorno |
| GET | `/api/platform/version` | qué versión de rendimiento se ejecuta y si hay una más reciente lista (`SELF_APP`) |
| POST | `/api/platform/update` | actualiza rendimiento a la imagen de una versión: `{release}`; se reinicia unos segundos después |
| GET | `/api/services` | el catálogo de Servicios (aplicaciones, complementos, servicios del clúster, datos de conexión) |
| GET | `/api/stats/visits` | el panel de visitantes del inicio: los últimos 7 días de cada aplicación que Umami cuenta, y los 7 anteriores ([Visitas](../guide/visits.md)) |
| GET | `/api/stats/delivery` | las estadísticas de entregas del panel: los últimos 30 días, los 30 anteriores y doce puntos semanales |
| GET | `/api/problems` | las advertencias y errores de la propia plataforma, agrupados ([Problemas](../guide/problems.md)) |
| GET | `/api/problems/count` | cuántos problemas están abiertos (la insignia de la barra superior) |
| POST | `/api/problems/{id}/dismiss` | descartar un problema |
| GET | `/api/ruta?kind=&q=` | las entradas de [la Ruta](../guide/ruta.md) |
| POST | `/api/ruta` | agrega una entrada (la escribe la persona con la sesión) |
| PUT | `/api/ruta/{id}` | cambia una entrada (se conserva la versión anterior) |
| POST | `/api/ruta/{id}/archive` | esconde una entrada de las listas |
| GET | `/api/ruta/activity` | los cargos de los agentes y sus últimas llamadas a herramientas |
| GET | `/api/agent-keys` | las llaves de agente (nunca los tokens) |
| POST | `/api/agent-keys` | crea una llave de agente; el token solo viene en esta respuesta |
| DELETE | `/api/agent-keys/{id}` | revoca una llave de agente |
| POST | `/api/notifications/test` | mandar ahora un correo de aviso de muestra (el botón de la página Entorno) |
| POST | `/api/dns/sync` | volver a crear ahora los registros DNS de cada aplicación |
| POST | `/api/propose` | detectar un repositorio y devolver un `rendimiento.yaml` + Dockerfile propuestos |
| GET | `/api/namespaces/{name}/migration` | lo que ya existe en un espacio de nombres (para adoptar cargas de trabajo existentes) |

## Aplicaciones 🔒 {#apps}

| Método | Ruta | Hace |
|---|---|---|
| GET | `/api/apps` | enumera las aplicaciones con su estado |
| POST | `/api/apps` | incorpora un repositorio (abre la solicitud de configuración, o adopta directamente) |
| GET | `/api/mercado` | el [Mercado](../guide/mercado.md): si está encendido, y cada página con el estado de su aplicación |
| POST | `/api/mercado` | abre una página: `{pagina, dominio}`; crea su repositorio y su aplicación (las fotos como URL `data:`, hasta 24 MB en total) |
| POST | `/api/mercado/preview` | el HTML de la página para la vista previa del formulario, como `{html}` |
| GET | `/api/mercado/{app}` | las respuestas del formulario de una página (`pagina.json`), su dirección y su repositorio |
| PUT | `/api/mercado/{app}` | guarda una página: una confirmación, que la publica |
| GET | `/api/apps/{app}` | una aplicación: especificación, estado, última ejecución y última versión |
| DELETE | `/api/apps/{app}` | borra la aplicación y todo lo que creó |
| GET | `/api/apps/{app}/impact` | lo que quitaría un borrado (se muestra antes de confirmar) |
| POST | `/api/apps/{app}/disconnect` | deja de administrar la aplicación pero deja corriendo sus cargas de trabajo |
| GET | `/api/apps/{app}/runs` | las ejecuciones de integración continua |
| POST | `/api/apps/{app}/runs` | inicia una ejecución de la rama principal (el botón **Ejecutar**) |
| GET | `/api/apps/{app}/releases` | el historial de versiones (huella por servicio) |
| GET | `/api/apps/{app}/releases/{number}/tasks/{task}/log` | el registro de una tarea posterior al despliegue de una versión |
| GET | `/api/apps/{app}/reliability?range=24h\|7d\|30d` | las comprobaciones de disponibilidad: por comprobación, la disponibilidad, p50/p95, los intervalos de la gráfica y la última comprobación; las caídas; las versiones del intervalo |
| GET | `/api/apps/{app}/visits?range=24h\|7d\|30d` | los visitantes según Umami: por cada sitio que coincide, los totales y los del periodo anterior, una serie para la gráfica, las páginas principales, los orígenes y los países |
| POST | `/api/apps/{app}/rollback` | revierte a una versión anterior (`{"release": N}`) |
| GET | `/api/apps/{app}/resources` | los objetos vivos de Kubernetes y su salud (el árbol de recursos) |
| PUT | `/api/apps/{app}/secrets/{secret}` | define los valores de un secreto (solo escritura; los valores nunca se devuelven) |
| GET | `/api/apps/{app}/events` | SSE: los cambios de estado de la aplicación |
| GET | `/api/apps/{app}/addons` | los complementos por aplicación (p. ej. Renovate) y su estado |
| PUT | `/api/apps/{app}/addons/{addon}` | enciende, apaga o configura un complemento por aplicación |

## Ejecuciones 🔒 {#runs}

| Método | Ruta | Hace |
|---|---|---|
| GET | `/api/runs/{id}` | una ejecución con sus pasos (el grafo) |
| POST | `/api/runs/{id}/cancel` | la cancela; se borran sus pods de construcción |
| GET | `/api/runs/{id}/steps/{step}/log` | el registro de un paso (guardado, o en vivo mientras corre) |
| GET | `/api/runs/{id}/events` | SSE: el estado de los pasos y las líneas del registro |

## Complementos por aplicación (Renovate) 🔒 {#per-app-add-ons-renovate}

| Método | Ruta | Hace |
|---|---|---|
| GET | `/api/addons` | los tipos de complementos por aplicación |
| GET | `/api/addons/{addon}` | uno, con sus ajustes |
| PUT | `/api/addons/{addon}` | cambia sus ajustes |
| GET | `/api/addons/{addon}/runs` | sus ejecuciones |
| POST | `/api/addons/{addon}/runs` | lo ejecuta ahora |
| GET | `/api/addons/{addon}/runs/{id}` | una ejecución con su registro |

## Complementos instalados (del clúster) 🔒 {#installed-add-ons-cluster-add-ons}

| Método | Ruta | Hace |
|---|---|---|
| GET | `/api/addon-catalog` | el catálogo de un clic |
| GET | `/api/installed` | los complementos instalados (objetos `Addon`) con su estado |
| GET | `/api/installed/{name}` | uno, con su vista previa, ganchos e inventario |
| PUT | `/api/installed/{name}` | instala o cambia uno (escribe `addons/<name>.yaml` en el repositorio de gitops) |
| PATCH | `/api/installed/{name}` | interruptores: suspender, sincronización manual, podar, adoptar |
| POST | `/api/installed/{name}/sync` | aprueba y aplica los cambios pendientes (sincronización manual) |
| DELETE | `/api/installed/{name}` | desinstala (primero los ganchos pre-delete; los tipos de `neverDelete` se quedan) |

## Ejemplo {#example}

```bash
# con una galleta de sesión copiada del navegador
curl -s -b "session=…" https://rendimiento.joserod.space/api/apps | jq '.[].name'
curl -s -b "session=…" -X POST https://rendimiento.joserod.space/api/apps/jobsentry/runs
```
