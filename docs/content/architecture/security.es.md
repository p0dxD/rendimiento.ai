# Modelo de seguridad

Una plataforma de despliegue puede ejecutar el código de cualquiera y cambiar cualquier cosa en el clúster, así que su modelo de seguridad merece un capítulo. Este dice qué está protegido, cómo, y qué todavía no.

## Identidades {#identities}

| Identidad | Puede | La usa |
|---|---|---|
| **Usted** (una cuenta de GitHub en `ALLOWED_USERS`) | todo en la interfaz y en la API | el panel |
| **La aplicación de GitHub** (tokens de instalación, 1 hora) | leer código, enviar ramas, abrir solicitudes de incorporación, escribir comprobaciones y administrar incidencias (Renovate), solo en los repositorios donde está instalada | la plataforma, los pods de construcción (para clonar), Renovate |
| Cuenta de servicio **`rendimiento`** | administrar objetos App y Addon; espacios de nombres, Deployments, Services, Ingresses, CronJobs, PVCs y Secrets en todo el clúster; pods de construcción en `rendimiento-builds`; leer nodos, métricas y CRD; **suplantar solo a `rendimiento-addons`** | el proceso de la plataforma |
| Cuenta de servicio **`rendimiento-addons`** | `cluster-admin`; ningún pod se ejecuta con ella | el controlador de complementos, por suplantación |
| **Pods de construcción** | nada en Kubernetes (sin token montado) | los pasos de integración continua, Renovate |

## Aislamiento del código no confiable {#isolation-of-untrusted-code}

El código de los repositorios se ejecuta en la integración continua: las pruebas y las líneas `RUN` de los Dockerfiles. Se contiene en capas:

1. **Sin credenciales**: los pods de construcción no reciben un token de cuenta de servicio de Kubernetes; el único secreto es un token para clonar en esa ejecución.
2. **Red**: la política de red `isolate-builds` permite solo DNS, los demonios de BuildKit e internet. Las demás aplicaciones, las bases de datos, la API de Kubernetes, la red de los nodos y la red doméstica (`192.168.0.0/16`) son inalcanzables. Se comprobó con pods de prueba en cada nodo.
3. **Nodos**: las construcciones nunca corren en el plano de control ni en los nodos que aparecen en `BUILD_EXCLUDE_NODES` (por ejemplo, los nodos que no pueden aplicar políticas de red).
4. **Límites**: un plazo por paso y límites de memoria por pod de pruebas.
5. **Bifurcaciones**: las solicitudes de incorporación desde bifurcaciones nunca se construyen.

Los demonios de BuildKit corren en modo **privilegiado** (crean contenedores); los pods de construcción hablan con ellos por TCP. Un `RUN` malicioso se ejecuta dentro del aislamiento de BuildKit, no en el contenedor del propio demonio, pero un escape de BuildKit llegaría a ese nodo. BuildKit sin privilegios está en la [hoja de ruta](../future/roadmap.md).

## Secretos {#secrets}

- **Nunca en git en texto plano.** Las aplicaciones se refieren a los secretos por su nombre en `rendimiento.yaml`; los valores se definen en la interfaz (**Configuración → Secretos**) o se confirman como **secretos sellados** (cifrados con la llave del clúster).
- **Nunca en la base de datos de la plataforma.** Postgres guarda huellas de las sesiones, no tokens.
- **Generados cuando se puede.** Las credenciales de `needs:` se generan una vez, se guardan en un Secret y se inyectan por referencia (`secretKeyRef`), así la contraseña nunca aparece en una especificación ni en un registro.
- **De corta vida** donde se puede: los tokens de instalación de GitHub duran una hora y se emiten para cada construcción y cada ejecución de Renovate.
- **Fuera de los registros.** El analizador de registros de Renovate, los registros de construcción y los mensajes de error nunca incluyen tokens.

## La superficie web {#the-web-surface}

- Inicio de sesión con OAuth de GitHub; las sesiones son tokens aleatorios guardados como huella, válidos 7 días y comprobados contra la lista de permitidos en cada petición.
- Las galletas son `HttpOnly`, `Secure` (en HTTPS) y `SameSite=Lax`; las peticiones que cambian el estado desde otro origen se rechazan.
- Los avisos web se verifican con su firma HMAC, comparada en tiempo constante.
- La página de configuración de la aplicación de GitHub requiere un token de configuración de un solo uso.
- Encabezados de seguridad: sin adivinar el tipo MIME, sin marcos, referencias solo del mismo origen.

## Validación en las orillas {#validation-at-the-edges}

`rendimiento.yaml` se valida antes de ejecutar cualquier cosa: los nombres son etiquetas DNS, las rutas no pueden salirse del repositorio, las anotaciones de la entrada se limitan a `nginx.ingress.kubernetes.io/*` y **se rechazan los fragmentos de código** (permiten configuración arbitraria de nginx), las direcciones de la red local deben ser privadas y se comprueban las referencias a secretos. El controlador rechaza espacios de nombres y nombres de host que no le pertenecen.

## Huecos conocidos {#known-gaps}

Estos son los puntos débiles, dichos con honestidad, más o menos en orden de importancia para una instalación con varios usuarios o pública:

1. **Un solo inquilino.** Cada usuario permitido puede hacer todo con cada aplicación. Hacen falta permisos por aplicación (dueños, observadores) y espacios de nombres por equipo antes de que desconocidos compartan un rendimiento. Vea [Convertirse en un proyecto de código abierto](../future/open-source.md).
2. **Permisos en todo el clúster** para la plataforma (para crear espacios de nombres para cualquier aplicación). Un modo más restringido le daría a cada aplicación un espacio de nombres creado de antemano por un administrador.
3. Demonios de BuildKit **privilegiados**.
4. **Registro en HTTP simple** en la red local.
5. **La base de datos de la plataforma** todavía no tiene respaldos.
6. **Sin bitácora de auditoría en la aplicación** de quién cambió qué (la bitácora de auditoría de Kubernetes y el historial de git cubren una parte).
