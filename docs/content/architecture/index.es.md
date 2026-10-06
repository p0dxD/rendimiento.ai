# Arquitectura

Cómo está armado rendimiento, del todo a cada subsistema. Cada capítulo liga al código; el [mapa del código](../reference/code-map.md) enumera cada archivo y cada función.

1. **[Panorama general](overview.md)**: un solo programa, sus partes, cómo se conectan y las capas de paquetes.
2. **[Flujos, paso a paso](flows.md)**: diagramas de secuencia de la incorporación, un envío, una versión, una reversión, una sincronización de complemento y una ejecución de Renovate.
3. **[El motor de integración continua](pipeline.md)**: la planeación, la cola de ejecuciones, los pods de construcción, la detección de cambios y los registros.
4. **[Construir imágenes con BuildKit](builds.md)**: BuildKit, los Dockerfiles en varias etapas, Railpack, la caché y el grupo de demonios.
5. **[El controlador de aplicaciones](controller.md)**: la conciliación, la generación de objetos, la aplicación del lado del servidor, la adopción, la salud, el DNS y los certificados.
6. **[El motor de complementos](addons.md)**: la generación con Helm y kustomize, las vistas previas, la adopción, la sincronización manual, los ganchos y git como fuente de verdad.
7. **[API, autenticación e interfaz](api-ui.md)**: la API HTTP, el inicio de sesión, los avisos web, las actualizaciones en vivo y la aplicación de React.
8. **[Datos y estado](data.md)**: qué vive en Postgres, qué en Kubernetes y qué en git, y por qué.
9. **[Modelo de seguridad](security.md)**: las identidades, el aislamiento, los secretos y los huecos.
