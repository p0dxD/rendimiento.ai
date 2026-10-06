# Glosario

Cada término lleva entre paréntesis la palabra en inglés que verá en el código, en la interfaz en inglés o en otra documentación.

**Adopción** (*adoption*)
: Tomar el control de objetos que ya existen, para que rendimiento los administre de ahí en adelante. La adopción nunca ocurre en silencio. Necesita `adopt`, y se detiene en **Blocked** si fuera a cambiar objetos vivos.

**Aplicación** (*App*)
: Un repositorio incorporado y todo lo que se despliega a partir de él. También el recurso personalizado `App`.

**Aplicación del lado del servidor** (*server-side apply*, SSA)
: Kubernetes combina en el servidor un objeto deseado y lleva la cuenta de qué *administrador de campos* es dueño de cada campo.

**Archivo de referencia** (*golden file*)
: Una salida esperada guardada (`testdata/*.golden.yaml`) contra la que se compara una prueba.

**BuildKit**
: El motor de construcción detrás de `docker build`. rendimiento corre un grupo de demonios de BuildKit (`buildkitd`) y manda cada imagen a uno de ellos.

**Complemento** (*add-on*)
: Un programa del clúster (un paquete de Helm o manifiestos en git) que rendimiento instala y mantiene sincronizado. Se declara en el repositorio de gitops y se representa con un objeto `Addon`. Un *complemento por aplicación* (Renovate) es distinto: es una función que se enciende para una aplicación.

**Conciliar** (*reconcile*)
: Una pasada de un controlador: leer el estado deseado, leer el estado real y actuar sobre la diferencia. Se puede repetir sin riesgo.

**Controlador** (*controller*)
: Un ciclo que hace que el clúster coincida con un estado deseado (vea *conciliar*). rendimiento tiene los controladores de App y de Addon.

**CRD** (*Custom Resource Definition*, definición de recurso personalizado)
: Le enseña a Kubernetes un tipo nuevo de objeto (`App`, `Addon`).

**Ejecución** (*run*)
: Una pasada de integración continua (clonar → probar → construir) para una confirmación. Sus **pasos** (*steps*) forman un grafo.

**Elección de líder** (*leader election*)
: Asegura que solo una réplica corra los controladores a la vez.

**envtest**
: Un servidor de la API de Kubernetes y un etcd reales que arrancan las pruebas, sin nodos.

**Especificación** (*spec*)
: `rendimiento.yaml`: los servicios, las construcciones, las pruebas, los dominios, las necesidades y demás de una aplicación.

**Eventos enviados por el servidor** (*Server-Sent Events*, SSE)
: Un flujo HTTP de un solo sentido, del servidor al navegador. Es la forma en que la interfaz se actualiza en vivo.

**Gancho de Helm** (*Helm hook*)
: Un objeto de un paquete marcado para correr en un momento del ciclo de vida (pre-install, post-upgrade, pre-delete…). rendimiento corre los ganchos como lo hace Helm.

**Grupo de BuildKit** (*pool*)
: Varios demonios de BuildKit detrás de un Service sin IP propia, que se encuentran por registros DNS SRV.

**Hash de encuentro** (*rendezvous hashing*)
: Elige el demonio con el `hash(imagen, demonio)` más alto. Es estable, y mueve poco trabajo cuando cambia el grupo.

**Huella** (*digest*)
: El hash del contenido de una imagen (`sha256:…`). Las versiones fijan huellas, no etiquetas, así lo que corre es exactamente lo que se construyó.

**MetalLB**
: Les da a los Services de tipo balanceador de carga (`LoadBalancer`) una IP de la red doméstica, de un grupo de direcciones reservado para ello.

**Necesidades** (*needs*)
: `needs:` en `rendimiento.yaml`. Declara una base de datos, una caché u otro servicio, y rendimiento lo provee y conecta sus datos de conexión.

**Podar** (*prune*)
: Borrar los objetos que ya no se desean.

**Railpack**
: Construye una imagen a partir del código fuente sin Dockerfile, detectando el lenguaje.

**Raíz de composición** (*composition root*)
: El único lugar donde se crea y se conecta cada objeto: `cmd/rendimiento/main.go`.

**`SKIP LOCKED`**
: Una cláusula de Postgres que deja que muchos trabajadores aparten distintas ejecuciones en cola sin esperarse unos a otros.

**Suplantación** (*impersonation*)
: Actuar con otra identidad en Kubernetes. Los complementos se aplican como `rendimiento-addons`, no como la propia plataforma.

**Tarea** (*task*)
: Un comando que corre como paso de integración continua (`tasks:` en `rendimiento.yaml`): una construcción móvil, una prueba de humo, un guion. Puede leer los secretos de la aplicación y, de forma predeterminada, solo corre en los envíos a la rama principal. Vea [Tareas](../guide/tasks.md).

**Tarea programada** (*job*)
: Un comando que corre según un horario, como CronJob de Kubernetes (`jobs:` en `rendimiento.yaml`).

**Versión** (*release*)
: Un conjunto numerado de huellas de imagen, una por servicio, producido por una ejecución exitosa en la rama principal. Revertir significa cambiarse a una versión anterior.
