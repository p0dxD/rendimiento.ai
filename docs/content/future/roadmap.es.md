# Hoja de ruta

Las tareas están ordenadas por valor según el esfuerzo, primero para este clúster y luego para otros usuarios. Cada una dice a dónde va el trabajo, así cualquiera se puede tomar como proyecto.

## A corto plazo {#near-term}

| # | Tarea | Por qué | Dónde |
|---|---|---|---|
| 1 | **Métricas de la plataforma en Grafana**: recolectar `METRICS_ADDR` (las métricas de disponibilidad ya se exportan ahí), agregar métricas de construcción y un panel | ver que las construcciones se vuelven lentas o que crecen las colas antes que los usuarios | un `VMServiceScrape`, un puerto de métricas en el Service, `pipeline`, `platform` |
| 2 | **Entornos de vista previa** por cada solicitud de incorporación (`pr-42.app.joserod.space`) | probar los cambios antes de integrarlos | `platform` (avisos web de solicitudes), `render` (sufijo en el nombre), limpieza al cerrarse |

## A mediano plazo {#medium-term}

| Tarea | Por qué |
|---|---|
| **Respaldos fuera de casa**: copiar la cubeta de respaldos de Garage a una cubeta en la nube (Cloudflare R2, Backblaze B2) | los respaldos sobreviven a perder todo el clúster |
| **Versiones canario y A/B** con los pesos canario de ingress-nginx | publicar primero al 10 %; reversión automática ante errores |
| **Contrato de extensiones**: un contenedor que recibe JSON y devuelve un veredicto en JSON, en los ganchos *antes del despliegue*, *después del despliegue* y *análisis* | compuertas de rendimiento con k6, experimentos de caos (Chaos Mesh), revisiones con IA, todo sin cambiar el núcleo |
| **Generador con IA** detrás de `generate.Generator` | mejores `rendimiento.yaml` y Dockerfiles para repositorios poco comunes |
| **Secretos desde un almacén externo** (SOPS en git, Vault, 1Password) | los secretos se vuelven reproducibles y auditables |
| **Roles y bitácora de auditoría** | más de una persona lo puede usar sin riesgo |
| **Plataforma de alta disponibilidad** (elección de líder, eventos con `LISTEN/NOTIFY`) | sin caídas al perder un nodo; vea [Escalar](scaling.md#live-updates-across-replicas) |
| **Tareas programadas** (`cron:` en la especificación, generadas como CronJobs) | reemplaza los últimos CronJobs escritos a mano |

## A largo plazo {#long-term}

| Tarea | Por qué |
|---|---|
| **Agentes para varios clústeres** y destinos en la nube (EKS, GKE, servidores físicos) | desplegar la misma aplicación en las Pi y en la nube; vea [Escalar](scaling.md#many-clusters) |
| **Entornos y promoción** (desarrollo → pruebas → producción, por huella) | la misma imagen, promovida cuando ya se probó |
| **Aprovisionamiento de clústeres** como extensión (Terraform o Crossplane) | un clúster nuevo con un clic |
| **Que cualquiera lo pueda instalar** (paquete de Helm, imágenes para varias arquitecturas, sitio de documentación) | vea [Código abierto](open-source.md) |

## Hecho {#done}

- Repositorio → URL HTTPS en vivo; aplicación de GitHub, solicitudes de incorporación, detección y plantillas
- Integración continua en un grupo de BuildKit repartido en los nodos trabajadores, con caché en el registro; construcciones sin Dockerfile con Railpack
- El controlador de aplicaciones: adopción, poda, autorreparación, reversión, DNS, cargas con GPU, IP de la red local
- `needs:` para Postgres, Redis y servicios existentes
- Un motor de complementos (Helm y kustomize desde git), con vistas previas, compuertas de seguridad y ganchos de Helm, que reemplazó a ArgoCD
- Renovate como complemento, que reemplazó al CronJob
- ArgoCD y Jenkins desinstalados (2026-09-28)
- Respaldos nocturnos de cada volumen que vale la pena conservar: rendimiento etiqueta los volúmenes que crea, y la página Entorno avisa de cualquier volumen en uso sin un respaldo reciente (2026-10-06)
- El libro en español, página por página, con el mismo estilo de Cotija (2026-10-06)
- Una cara nueva: Cotija, pueblo mágico. De día, paredes encaladas, tejas, papel picado y el guardapolvo color grana; de noche, un cielo añil, faroles y colores de alebrije. Cada aplicación es un cempasúchil que se marchita en una caída y revive cuando vuelve; caen pétalos cuando sale una versión. El movimiento se puede apagar y sigue el ajuste de reducir movimiento del dispositivo (2026-10-06)
- Español de México: la interfaz con un interruptor EN/ES, los mensajes de la plataforma traducidos al mostrarse (también los guardados) y los correos en el idioma de `NOTIFY_LANG` (2026-10-05)
- Los problemas se resuelven solos cuando rendimiento ve el arreglo: un envío posterior aceptado, un correo posterior enviado (2026-10-05)
- La página Problemas: las advertencias y errores de la propia plataforma, agrupados, con un conteo en la barra superior y un aviso en la aplicación afectada; un `rendimiento.yaml` inválido es una ejecución fallida, una comprobación roja en GitHub y un correo (2026-10-05)
- Las bases de datos de las aplicaciones pasaron a `needs: [postgres]`: podoi, wellness y stockpulse, cada una copiada y con el conteo de filas revisado, de noche y sin caídas (2026-10-05)
- Estadísticas de entregas en el panel: despliegues, tiempo de entrega, tasa de cambios fallidos y tiempo de recuperación de los últimos 30 días, con la tendencia contra los 30 anteriores y líneas de doce semanas (2026-10-04)
- El almacenamiento de objetos pasó de MinIO, cuyas imágenes ya no se pueden descargar libremente, a Garage: los respaldos de Longhorn y el archivo de registros (2026-10-04)
- Tareas antes del despliegue: migraciones antes de la actualización; un fallo detiene la versión (2026-10-02)
- Tareas después del despliegue: pruebas de humo y migraciones contra la versión en vivo, en su espacio de nombres; un fallo la revierte (2026-10-02)
- Registros de los pasos archivados en MinIO (gzip) y borrados a los 365 días por una regla de ciclo de vida (2026-10-01)
- Avisos por correo de construcciones fallidas, reversiones, caídas y recuperaciones (2026-10-01)
- Verificación de versiones con reversión automática: cada versión se vigila 5 minutos y se revierte a la última buena si descompone un servicio (2026-10-01)
- Comprobaciones de disponibilidad y la pestaña Confiabilidad: cada servicio se comprueba una vez por minuto, dentro del clúster y desde afuera; disponibilidad, tiempos de respuesta y caídas en gráficas con marcas de las versiones (2026-10-01)
- Tareas: comandos como pasos de integración continua con secretos, para construcciones de Expo/EAS y pruebas de humo (2026-09-29)
- El catálogo de Servicios, las comprobaciones del Entorno, el DNS dinámico
- Pruebas remotas (`make test-remote`), y este libro
