# Las aplicaciones en el día a día

## Desplegar un repositorio nuevo {#deploy-a-new-repository}

1. **Nueva aplicación** → elija el repositorio.
2. Revise la propuesta: un formulario por cada servicio detectado. Defina la URL pública (un subdominio en una de sus zonas de Cloudflare), el tamaño, la comprobación de estado, el almacenamiento y las necesidades. Elija **desde el código fuente** (Railpack) o **agregar un Dockerfile** si la carpeta no tiene uno.
3. **Abrir solicitud y desplegar**. rendimiento abre una solicitud de incorporación que agrega `rendimiento.yaml` (y el Dockerfile, si lo eligió). La rama de la solicitud se construye y se comprueba de inmediato.
4. **Integre la solicitud.** Ese envío a la rama principal construye, publica y despliega. El registro DNS y el certificado llegan en uno o dos minutos.

Un repositorio que ya contiene `rendimiento.yaml` se salta la solicitud: su primera construcción empieza de inmediato.

!!! tip "Repositorios con varios proyectos"
    Cada carpeta con su propio manifiesto (`package.json`, `go.mod`…) se convierte en un servicio. El primer servicio con aspecto de interfaz recibe el dominio a secas; los demás reciben `<servicio>-<aplicación>.<zona>`. Ajústelo como quiera.

## Migrar una aplicación que ya está en ejecución {#migrate-an-app-that-is-already-running}

Cuando el espacio de nombres que usaría la aplicación ya existe (desplegado con ArgoCD, Helm o `kubectl`), el asistente muestra lo que se ejecuta ahí (despliegues, hosts, secretos, la aplicación de ArgoCD) y ofrece **asumir su control**. Los pasos típicos para una aplicación que viene de ArgoCD:

1. En `rendimiento.yaml`, conserve los nombres existentes donde importan: `tlsSecret` (reutilizar el certificado), `ingress.name`, `volume.existingClaim` (reutilizar los datos) y `secrets` (reutilizar los Secrets).
2. Desvincule la Application de ArgoCD **sin cascada** (que no tenga finalizador, o quíteselo antes), para que ArgoCD deje de administrarla sin borrar nada.
3. Incorpórela con **asumir el control**. Los pods nuevos arrancan junto a los anteriores; el tráfico se mueve solo cuando están listos; los objetos anteriores se reemplazan en su lugar o se eliminan. Vea [la adopción](../architecture/controller.md#adoption-taking-over-what-is-already-running).
4. Si ArgoCD también es dueño del espacio de nombres (porque ahí viven otras cosas), use `sharedNamespace: true`.

Con un volumen que guarda una base de datos, el cambio tiene un hueco breve (el volumen pasa del pod anterior al nuevo): de 15 a 50 segundos en las migraciones hechas hasta ahora.

## Dominios, alias y rutas {#domains-aliases-and-routes}

- `domain` le da a un servicio `https://<dominio>`: una entrada, un certificado de Let's Encrypt y un registro en Cloudflare que sigue a la IP pública de la red.
- `aliases` agrega hosts con el mismo certificado (por ejemplo `www.`).
- `routes` manda una ruta en el host de otro servicio a este servicio: una interfaz en `wellness.jobsentry.net` y su API en `wellness.jobsentry.net/api`, cada una desplegada por su cuenta.

El **Resumen** de la aplicación muestra todas las direcciones de cada servicio: su dominio, sus alias y sus rutas.

## Secretos {#secrets}

Declare los nombres en `rendimiento.yaml` (`secrets`, `secretEnv`, `secretFiles`); defina los valores en la página **Configuración** de la aplicación, o confirme **secretos sellados** (cifrados con la llave pública del clúster, que solo el clúster puede descifrar). Los valores nunca van a git en texto plano ni a la base de datos de la plataforma.

## Volúmenes {#volumes}

`volume: { size: 5Gi, mount: /data }` crea un volumen de Longhorn que sobrevive a los reinicios, a los despliegues e incluso a quitar el servicio del archivo. Use `existingClaim` para conservar un volumen existente. Agregue una etiqueta de respaldo de Longhorn para incluirlo en los respaldos de cada noche.

## Tareas programadas {#scheduled-jobs}

Las tareas de `jobs:` se ejecutan según un horario cron, con la imagen de un servicio, su propia carpeta o una imagen lista para usar; los recursos, los tiempos límite y los secretos funcionan igual que en los servicios. Vea sus ejecuciones con `kubectl -n <aplicación> get cronjobs,jobs`.

## GPU {#gpus}

`gpu: 1` pide una GPU. El perfil de GPU del clúster (los ajustes `GPU_*`) agrega la clase de ejecución de NVIDIA, las bibliotecas del controlador del nodo (de solo lectura), las variables `NVIDIA_*` y un montaje de memoria compartida más grande. Un servicio con GPU ejecuta una sola réplica con la estrategia Recreate.

## Exponer un servicio en la red local {#expose-a-service-on-the-lan}

`lan: { ip: 192.168.1.50 }` agrega un Service de tipo LoadBalancer con esa dirección, tomada del grupo de MetalLB, en el puerto 80 (o en `lan.port`). Sirve para paneles y herramientas que no deben ser públicos. Este libro se sirve así.

## Revertir {#rollback}

**Versiones → Revertir** en cualquier versión. Se crea y se despliega una versión nueva con las imágenes y la especificación de aquella. No se vuelve a construir nada; tarda lo mismo que un despliegue.

## Suspender {#suspend}

`kubectl patch apps.rendimiento.ai <aplicación> --type merge -p '{"spec":{"suspend":true}}'` detiene la conciliación: los cambios manuales (como bajar a cero una base de datos durante una tormenta) se respetan hasta que la reanude con `"suspend":false`.

## Eliminar o desconectar {#delete-or-disconnect}

En **Configuración → Zona de peligro**:

- **Eliminar** primero muestra exactamente qué se quitaría (espacio de nombres, volúmenes y sus tamaños, secretos), y luego elimina la aplicación, su espacio de nombres y sus registros DNS. En un espacio de nombres compartido, solo se eliminan los objetos de la propia aplicación.
- **Desconectar** deja de administrar la aplicación y deja todo en ejecución (incluidos los registros DNS), para cuando quiera devolvérsela a otra herramienta.
