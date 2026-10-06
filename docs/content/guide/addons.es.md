# Complementos

Los complementos son los programas que su clúster necesita y que no son una de sus aplicaciones: almacenamiento, paneles, operadores, el grupo de construcción. rendimiento los instala desde **paquetes de Helm** o desde **carpetas de manifiestos en git**, muestra cada cambio antes de aplicarlo y puede asumir el control de lo que ya está en ejecución sin tocarlo. Cómo funciona por dentro: [el motor de complementos](../architecture/addons.md).

## Lo que está instalado hoy {#what-is-installed-today}

| Complemento | Origen | Modo |
|---|---|---|
| `longhorn` | paquete de Helm `longhorn` v1.10.2 | sincronización manual, sin eliminación |
| `longhorn-backups` | `p0dxD/gitops/longhorn-backups` | automático |
| `umami` | `p0dxD/gitops/umami` | automático |
| `hajimari` | paquete de Helm `hajimari` 2.0.2 | automático |
| `buildkit` | `p0dxD/gitops/buildkit` (el grupo de BuildKit) | automático |

`kubectl get radd` los muestra con su fase.

Un complemento cuyos servicios tienen una dirección de MetalLB la muestra en su tarjeta y en su página. Las interfaces web, como la de Longhorn o la de Hajimari, son ligas que se pueden abrir. Los demás servicios, como el puerto de una base de datos, muestran la dirección como texto simple.

## Instalar uno desde el catálogo {#install-one-from-the-catalog}

**Complementos → Agregar desde el catálogo → Instalar**, llene el nombre, el espacio de nombres, la versión y los pocos ajustes que se muestran, pegue si quiere valores del paquete en YAML (se combinan sobre los ajustes), elija las opciones y presione **Instalar**. Eso confirma `addons/<nombre>.yaml` en `p0dxD/gitops`; en segundos el complemento aparece en *Instalados*, se muestra su vista previa y se aplica.

Las opciones:

- **Asumir el control de lo que ya está en ejecución**: adoptar los objetos que existen (de ArgoCD, Helm o `kubectl`). La primera sincronización solo continúa si no cambiaría nada.
- **Sincronización manual**: los cambios esperan a que usted los revise y presione Sincronizar.
- **Eliminar los objetos que se quiten del paquete**: la eliminación (nunca de CRD, espacios de nombres, volúmenes ni clases de almacenamiento).

*Cualquier paquete de Helm* pide la URL de un repositorio, el nombre del paquete y la versión. *Manifiestos desde una carpeta de git* pide una carpeta del repositorio de gitops (o `propietario/repositorio` y una carpeta).

## Escribir uno a mano {#write-one-by-hand}

Confirme un archivo en `p0dxD/gitops/addons/`:

```yaml
name: uptime-kuma
title: Uptime Kuma
category: monitoring
description: Uptime monitoring and a status page.
namespace: uptime-kuma
createNamespace: true
helm:
  repo: https://dirsigler.github.io/uptime-kuma-helm
  chart: uptime-kuma
  version: 4.2.0
values:
  volume:
    size: 2Gi
    storageClassName: longhorn
prune: true
```

| Campo | Qué significa |
|---|---|
| `name` | Etiqueta DNS; también es el nombre predeterminado de la instalación de Helm. |
| `title`, `category`, `description` | Cómo se muestra. |
| `namespace`, `createNamespace` | Adónde van los objetos con espacio de nombres; crearlo si falta. |
| `helm: {repo, chart, version}` | Un paquete de un repositorio clásico de Helm (`index.yaml`). |
| `git: {repo, path}` | Una carpeta (kustomization o YAML simple); `repo` es de forma predeterminada el repositorio de gitops. |
| `releaseName` | Conservar el nombre de una instalación de Helm existente al adoptarla (las etiquetas dependen de él). |
| `values` | Valores del paquete (solo Helm). |
| `adopt`, `allowAdoptChanges` | Asumir el control de lo que existe; permitir que la adopción lo cambie. |
| `manualSync` | Los cambios esperan a **Sincronizar**. |
| `prune` | Eliminar los objetos que salen del origen. |
| `suspend` | Dejar de sincronizar. |
| `skipHooks`, `hookTimeout` | No ejecutar nunca los ganchos de Helm; segundos que puede durar la tarea de un gancho (600 de forma predeterminada). |

Los campos desconocidos son errores; un archivo no válido se reporta en la página Complementos y se ignora.

## Cambiar, revisar, sincronizar {#change-review-sync}

Edite la definición en la página del complemento (**Editar la definición**, que confirma el cambio) o en git. La página del complemento muestra la **vista previa**: cuántos objetos se crearían, actualizarían o eliminarían, cada uno con sus diferencias, y qué ganchos de Helm se ejecutarían. En modo manual, presione **Sincronizar** cuando esté conforme.

## Cuando un complemento dice… {#when-an-add-on-says}

| Fase | Qué significa | Qué hacer |
|---|---|---|
| **Sincronizado** | Todo se aplicó y no ha cambiado desde entonces. | Nada. |
| **Cambios pendientes** | Sincronización manual, y el origen cambió. | Revise, y luego **Sincronizar**. |
| **Requiere revisión** | Asumir el control cambiaría algo que está en ejecución. | Lea las diferencias; corrija la definición, o **Permitir estos cambios y asumir el control**. |
| **Error** | Falló la generación, la aplicación o un gancho; el mensaje dice cuál. | Corrija el origen; se reintenta cada 5 minutos. |
| **Suspendido** | `suspend: true` o la anotación `rendimiento.ai/paused`. | **Reanudar**, o quite la anotación. |

## Quitar un complemento {#remove-an-add-on}

- **Dejar de administrar (sigue en ejecución)** borra la definición; todo lo que instaló sigue en ejecución, sin administrar.
- **Desinstalar** primero ejecuta los ganchos `pre-delete` del paquete (si uno falla, la desinstalación se detiene), elimina lo que instaló el complemento excepto los CRD, los espacios de nombres, los volúmenes y las clases de almacenamiento, y después ejecuta los ganchos `post-delete`.

## Adoptar algo de ArgoCD, paso a paso {#adopting-something-from-argocd-step-by-step}

Así se mudaron umami, Hajimari, longhorn-backups y Longhorn:

1. Escriba la definición de modo que genere **exactamente** lo que está en ejecución: el mismo paquete, versión, nombre de instalación y valores (cópielos de la Application de ArgoCD), o la misma carpeta de git. Ponga `adopt: true`.
2. Confírmela. El complemento compara la vista previa con los objetos en vivo: **0 por actualizar** significa que coinciden perfectamente, y asume el control. Cualquier otra cosa se detiene como *Requiere revisión*, con las diferencias.
3. En cuanto muestre *Sincronizado*, elimine la Application de ArgoCD **sin cascada** (sin finalizador), para que ArgoCD la suelte sin borrar nada.
