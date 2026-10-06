# Necesidades y catálogo de servicios

## Necesidades {#needs}

Una aplicación debe poder decir *de qué* depende y dejarle el *cómo* a la plataforma:

```yaml
services:
  - name: api
    needs:
      - postgres                              # una base de datos para esta aplicación
      - redis                                 # una caché para esta aplicación
      - service: jobsentry/ollama-internal    # el servicio de otra aplicación
        env: OLLAMA_HOST
```

### `postgres` {#postgres}

rendimiento ejecuta **un Postgres por aplicación** (compartido por todos sus servicios que lo necesitan):

- un Deployment y un Service llamados `postgres` en el espacio de nombres de la aplicación (`postgres:17-alpine`, configurable), sobre un volumen de Longhorn `postgres-data` (5 Gi, configurable), con la estrategia Recreate;
- un Secret `postgres-credentials` creado **una sola vez** con una contraseña aleatoria (nunca se vuelve a generar: la base de datos se inicializa con ella), que guarda `username` (`app`), `password`, `database` (el nombre de la aplicación con `_` en lugar de `-`), `host`, `port` y `uri`;
- en cada servicio que lo necesita: `DATABASE_URL` (la `uri`) y las variables estándar `PGHOST`, `PGPORT`, `PGUSER`, `PGPASSWORD` y `PGDATABASE`, todas leídas del Secret por referencia.

```yaml
postgres: { version: "16", size: 20Gi, resources: { memory: 512Mi, memoryLimit: 1Gi } }
```

### `redis` {#redis}

Un Redis por aplicación, como **caché**: sin persistencia, con las claves menos usadas recientemente desalojadas al pasar de `maxMemory` (64 MB de forma predeterminada), la contraseña en `redis-credentials` y `REDIS_URL` inyectada.

### Otro servicio {#another-service}

`{service: espacio/nombre}` resuelve el puerto de aplicación del Service e inyecta `http://nombre.espacio.svc.cluster.local:puerto` (o `http://nombre:puerto` para un servicio de la misma aplicación) en `<NOMBRE>_URL`, o en la variable `env` que usted indique. Si el servicio no existe, la aplicación muestra un error que lo nombra en lugar de arrancar descompuesta.

### Del lado de la aplicación {#from-the-apps-side}

No hay nada que configurar. Conéctese con las variables; `DATABASE_URL` funciona con casi cualquier controlador y ORM. La tarjeta **Provisto para esta aplicación** de la página de la aplicación muestra la base de datos o la caché, su salud, qué servicios la usan y cómo llegar a ella desde su computadora:

```bash
kubectl -n <aplicación> port-forward svc/postgres 5432
kubectl -n <aplicación> get secret postgres-credentials -o jsonpath='{.data.uri}' | base64 -d
```

!!! note "La detección marca las necesidades por usted"
    Cuando el código usa una biblioteca cliente de Postgres o de Redis (`psycopg`, `asyncpg`, `pg`, `ioredis`, `pgx`, el controlador JDBC de PostgreSQL, Jedis…), el asistente marca la necesidad por usted.

## El catálogo de servicios {#the-services-catalog}

La página **Servicios** enumera cada Service del clúster, para que una aplicación encuentre a quién llamar. De cada uno muestra:

- **de dónde viene**: una aplicación (con la carpeta del repositorio y la versión), un complemento, una aplicación de ArgoCD, una instalación de Helm o un despliegue hecho a mano; y sus imágenes, con ligas a sus páginas en el registro;
- **qué expone**: puertos, protocolo, URL públicas, una dirección en la red local (una liga en la propia tarjeta cuando es una interfaz web, como Grafana o la consola de MinIO), la comprobación de estado, las rutas documentadas y si alguna política de red restringe quién puede conectarse;
- **cómo conectarse**: la dirección en el clúster, la dirección corta desde la misma aplicación y un fragmento de `rendimiento.yaml` para pegar (en las bases de datos, en forma de `secretEnv`, porque las credenciales van en secretos);
- **quién lo usa**: cada carga de trabajo cuyas variables de entorno apuntan a él (por su nombre DNS en el clúster, por su nombre corto dentro del espacio de nombres o por su dirección en la red local). Los valores guardados en secretos no se pueden ver.

Los servicios se agrupan por **categoría**: Bases de datos, Cachés y colas, IA, Almacenamiento, Monitoreo y analítica, Web y API, Herramientas de desarrollo, Plataforma. Las categorías se deducen del protocolo, el nombre y la imagen (no del host del registro), o se fijan con `catalog.category`.

Describa sus propios servicios para los demás con un bloque `catalog:` ([referencia](spec.md#catalog)); los servicios de ollama y de linguistic en jobsentry son ejemplos.
