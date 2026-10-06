# Recursos personalizados

rendimiento agrega dos recursos **de alcance de clúster** en el grupo de API `rendimiento.ai/v1alpha1`. Se generan desde `api/v1alpha1/*_types.go` en `deploy/crds/`.

```bash
kubectl get apps.rendimiento.ai          # o: kubectl get app
kubectl get addons.rendimiento.ai        # o: kubectl get radd
```

!!! note "¿Por qué `radd`?"
    k3s ya tiene un recurso llamado `addons` (`addons.k3s.cattle.io`). Un simple `kubectl get addons` sería ambiguo, así que el nombre corto es `radd`.

## App {#app}

Un `App` por cada repositorio incorporado. La **plataforma** lo escribe (a partir de `rendimiento.yaml` y de la última versión). El **controlador de aplicaciones** lo convierte en cargas de trabajo. Rara vez se edita a mano.

| Campo | Significado |
|---|---|
| `spec.repo` | `dueño/nombre` en GitHub |
| `spec.services[]` | los servicios de `rendimiento.yaml` ([especificación](../guide/spec.md)) |
| `spec.jobs[]` | las tareas programadas o de una sola vez |
| `spec.sharedNamespace` | desplegar en un espacio de nombres existente, compartido con otras cosas |
| `spec.postgres`, `spec.redis` | opciones de las bases de datos de `needs:` |
| `spec.images` | servicio → referencia de la imagen, fijada por huella |
| `spec.release` | el número de versión al que pertenecen estas imágenes |
| `spec.suspend` | dejar de conciliar (los cambios a mano se dejan en paz) |
| `spec.adopt` | permitir tomar el control de objetos existentes con nombres que coinciden |
| `status.phase` | `WaitingForBuild`, `Progressing`, `Healthy`, `Degraded`, `Suspended`, `Error` |
| `status.message` | el porqué, en una línea |
| `status.release` | la versión que de verdad se aplicó |
| `status.services[]` | por servicio: réplicas, réplicas listas, imagen, URL, URL de la red local (`lanURL`) y si el certificado y el DNS están listos |
| `status.conditions` | las condiciones estándar de Kubernetes |

`kubectl get app` muestra **Phase**, **Release**, **Repo** y **Age**.

## Addon {#addon}

Un `Addon` por cada complemento del clúster instalado. Vienen de `addons/*.yaml` en el repositorio de gitops (`ADDONS_REPO`), que aplica el sincronizador. El **controlador de complementos** los genera y los aplica.

| Campo | Significado |
|---|---|
| `spec.namespace` | a dónde van los objetos |
| `spec.source.helm` | `repo`, `chart` y `version` de un paquete de Helm |
| `spec.source.git` | `repo`, `path` y `revision` de manifiestos simples o de una kustomization |
| `spec.values` | los valores de Helm (texto YAML) |
| `spec.releaseName` | el nombre de la instalación de Helm (de forma predeterminada, el nombre del complemento); importa al adoptar una instalación existente |
| `spec.createNamespace` | crear `spec.namespace` si falta |
| `spec.adopt` | permitir tomar el control de objetos que ya existen |
| `spec.allowAdoptChanges` | permitir la adopción aunque cambie objetos vivos (si no, **Blocked**) |
| `spec.prune` | borrar los objetos que ya no se generan |
| `spec.manualSync` | los cambios esperan en **OutOfSync** hasta que se aprueben |
| `spec.syncRequest` | se incrementa para aprobar (el botón **Sincronizar**) |
| `spec.suspend` | dejar de conciliar |
| `spec.skipHooks` | no correr los ganchos de Helm |
| `spec.hookTimeout` | los segundos que puede tardar un gancho |
| `spec.title`, `spec.category`, `spec.description` | cómo aparece en la interfaz |
| `status.phase` | `Pending`, `Synced`, `OutOfSync`, `Blocked`, `Error`, `Suspended` |
| `status.revision` | la versión del paquete o la confirmación de git aplicada |
| `status.objects[]` | el inventario: cada objeto del que es dueño (se usa para podar y desinstalar) |
| `status.preview` | los conteos de crear, actualizar, sin cambios y podar, con las diferencias de cada objeto |
| `status.hooks[]`, `status.hookRuns[]` | los ganchos encontrados en el paquete y sus ejecuciones recientes |
| `status.appliedHash` | la huella de lo que se aplicó; distingue una instalación de una actualización para los ganchos |
| `status.lastSynced`, `status.appliedSyncRequest`, `status.adopted` | contabilidad interna |

La anotación **`rendimiento.ai/paused: "true"`** pausa un complemento sin cambiar su especificación. La usan los guiones y la automatización.

`kubectl get radd` muestra **Phase**, **Namespace**, **Revision** y **Age**.

## Etiquetas y anotaciones {#labels-and-annotations}

| Llave | En | Significado |
|---|---|---|
| `app.kubernetes.io/managed-by: rendimiento` | todo lo que crea | la propiedad |
| `rendimiento.ai/app` | los objetos de las aplicaciones | de qué aplicación |
| `rendimiento.ai/addon` | los objetos de los complementos | de qué complemento |
| `rendimiento.ai/lan` | los Services `<service>-lan` | un balanceador de carga de la red local hecho por `lan:` |
| `rendimiento.ai/paused` | Addon | pausar la conciliación |
| `rendimiento.ai/backup: skip` | PersistentVolumeClaim | no necesita respaldo, a propósito; la comprobación *Respaldos de volúmenes* lo deja en paz |
| `metallb.io/loadBalancerIPs` | los Services de la red local | la IP fija que se le pide a MetalLB |
