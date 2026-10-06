# Por qué existe rendimiento

## El problema que reemplazó {#the-problem-it-replaced}

Antes de rendimiento, llevar una aplicación de un repositorio de GitHub a una dirección HTTPS pública en este clúster requería unos once pasos manuales, repartidos en cuatro sistemas:

1. Escribir un `Jenkinsfile` y un `jenkinsconfig.yaml` para la biblioteca compartida de Jenkins.
2. Escribir un `Dockerfile`.
3. Escribir una carpeta `argocd/`: espacio de nombres, despliegue, servicio, entrada, kustomization y una `Application` de ArgoCD.
4. Agregar una tarea para el repositorio en el rol de Ansible que configura Jenkins, y ejecutar Ansible.
5. Agregar en GitHub un aviso web que apuntara a Jenkins.
6. Darle a ArgoCD credenciales para el repositorio.
7. Aplicar la Application de ArgoCD con `kubectl apply`.
8. Crear el registro DNS en Cloudflare a mano.
9. Ejecutar `kubectl create secret` para todo lo delicado.
10. Enviar el código.
11. Ver cómo la integración continua confirmaba un `[skip ci] bump image tags` de regreso en el repositorio para que ArgoCD notara la nueva imagen, y esperar que los dos sistemas estuvieran de acuerdo.

Cada uno de esos pasos era pegamento entre herramientas, y cada herramienta tenía sus propias credenciales, su propia idea del estado y sus propias formas de fallar. Los tokens personales de acceso vencían (el de Renovate venció, sin que nadie lo notara, por más de un mes). Los despliegues eran tan buenos como el último YAML editado a mano.

## El objetivo {#the-goal}

> Conectar GitHub, elegir un repositorio, presionar **Desplegar** y obtener una URL HTTPS en vivo. Después, que cada envío se construya, se pruebe y se publique, sin manifiestos que escribir y con espacio para crecer: visualizaciones, complementos y, más adelante, pruebas de caos, de rendimiento y A/B.

## Las decisiones que le dieron forma {#the-decisions-that-shaped-it}

Estas decisiones se tomaron al principio, y casi todo el resto del libro se desprende de ellas.

| Decisión | En lugar de | Por qué |
|---|---|---|
| **Construir nuestro propio plano de control** (API, motor de integración continua, controladores) y reutilizar las piezas que el clúster ya tiene (BuildKit, el registro, cert-manager, ingress-nginx, Longhorn, MetalLB) | Envolver Jenkins y ArgoCD, o adoptar una gran plataforma como servicio | El problema era el *pegamento* entre herramientas. Un solo sistema dueño de todo el camino puede convertirlo en un paso, y las piezas ya son sólidas. |
| **Go** para el servidor, **React + TypeScript** para la interfaz | Node, Python, Java | Go es el lenguaje de Kubernetes: sus bibliotecas cliente, controller-runtime, Helm y kustomize son bibliotecas de Go que rendimiento importa directamente. Se compila en un solo programa estático que funciona en cualquier lugar. Vea [Go para este código](../develop/go.md). |
| **Un solo programa** que es API, interfaz, trabajador de integración continua y controlador a la vez | Microservicios | Más sencillo de operar y de entender en un clúster pequeño. Las partes son paquetes separados detrás de interfaces, así que se pueden dividir después ([Escalar](../future/scaling.md)). |
| Una **aplicación de GitHub** | Tokens personales de acceso, avisos web por repositorio y llaves de despliegue | Una sola instalación cubre todos los repositorios: avisos web para todos y tokens de corta vida (1 hora) emitidos al momento. Nada de larga vida que se pueda filtrar o vencer. |
| **El puntero de la versión vive en un objeto de Kubernetes** (`App`), no en git | Que la integración continua confirme las etiquetas de imagen de regreso en el repositorio | Sin confirmaciones `[skip ci]`, sin carreras entre la integración continua y GitOps, y revertir es volver a apuntar a una versión anterior (las imágenes quedan fijadas por su huella). La *configuración* sigue en git (`rendimiento.yaml`). |
| **Detección con plantillas**, detrás de una interfaz `Generator` | Pedirle todo al usuario, o inteligencia artificial desde el primer día | Las reglas y plantillas son predecibles y se pueden probar; la interfaz deja espacio para un generador con inteligencia artificial más adelante. |
| **DNS de Cloudflare por aplicación**, administrado por la plataforma | DNS a mano | Un dominio en `rendimiento.yaml` debe simplemente funcionar, incluso siguiendo la IP pública cambiante de la red doméstica. |
| **Postgres** para el estado de la plataforma | Solo objetos de Kubernetes | Las ejecuciones de integración continua, los registros y el historial son datos relacionales con una cola; Kubernetes guarda el estado deseado. Vea [Datos y estado](../architecture/data.md). |

## Cómo se mudaron sus aplicaciones {#how-your-apps-moved-onto-it}

rendimiento se construyó junto al Jenkins y al ArgoCD que ya existían, y cada aplicación se migró una por una, **en su lugar y sin interrupciones**: las nuevas cargas de trabajo arrancan junto a las anteriores, el tráfico se mueve solo cuando están listas, y los objetos anteriores se adoptan o se eliminan. Las bases de datos conservaron sus volúmenes. Cada migración se vigiló segundo a segundo.

| Cuándo (2026) | Qué |
|---|---|
| 23 y 24 de septiembre | Primera versión: configuración de la aplicación de GitHub, detección, integración continua en BuildKit, el controlador de aplicaciones, DNS y TLS. Desplegada en `rendimiento.joserod.space`. |
| 24 al 26 de septiembre | Se migraron **secplus**, **simplerfc** (con su volumen de SQLite movido), **podoi** (con su Postgres), **stockpulse** (web, API, Postgres, tarea de extracción de datos) y **wellness** (interfaz, API, Postgres, funciones sin servidor). |
| 27 de septiembre | Se migró **jobsentry** (gate, ui, intel y whois, en un espacio de nombres compartido con ArgoCD), y luego **linguistic-ai + ollama** en el nodo con GPU. Se agregaron las construcciones con Railpack, el catálogo de Servicios y las categorías. Se apagó **Jenkins**. **Renovate** se convirtió en un complemento autenticado como la aplicación de GitHub. |
| 28 de septiembre | El **motor de complementos**; se adoptaron **umami**, **hajimari**, **longhorn-backups** y **Longhorn** desde ArgoCD sin un solo cambio; se apagó **ArgoCD**. Se agregaron `needs:`, los ganchos de Helm, el grupo de BuildKit, las pruebas remotas y este libro. |

## Lecciones aprendidas a la mala {#lessons-learned-the-hard-way}

Cada una de estas ya se resuelve en el código, y la mayoría tiene una prueba. Vale la pena conocerlas porque volverán a aparecer en otras formas.

??? failure "El `targetPort` por nombre de un Service rompió el tráfico durante una adopción"
    Los pods creados por ArgoCD no nombraban su puerto `http`. Cuando rendimiento adoptó el Service con `targetPort: http`, los pods anteriores dejaron de recibir tráfico durante tres minutos. Ahora los Services apuntan al **número** de puerto. ([controlador](../architecture/controller.md))

??? failure "La aplicación del lado del servidor conservaba campos de la herramienta anterior"
    Después de adoptar un objeto, la propiedad de campos de ArgoCD se quedaba, así que quitar un campo de `rendimiento.yaml` no lo quitaba del objeto. Ahora la adopción reemplaza el objeto una vez y convierte la propiedad de rendimiento en la única. ([controlador](../architecture/controller.md#adoption-taking-over-what-is-already-running))

??? failure "Un CRD desactualizado descartaba campos nuevos sin avisar"
    Agregar un campo a los tipos de Go sin volver a generar el CRD hace que el servidor de la API lo descarte sin ningún error. `make test` (y `make test-remote`) ahora siempre regeneran primero.

??? failure "La elección de líder mató a la plataforma a media construcción"
    En un plano de control ocupado, el arrendamiento del líder venció y el proceso terminó, llevándose consigo las construcciones en curso. Con una sola réplica, la elección de líder está desactivada; las ejecuciones interrumpidas se reintentan al reiniciar.

??? failure "Una dependencia de Python sin fijar tumbó una aplicación"
    SQLAlchemy 2.1 hizo de psycopg 3 el controlador predeterminado de Postgres, y wellness (que usa psycopg2) entró en un ciclo de reinicios. Las dependencias ahora están fijadas, y Renovate deja las actualizaciones menores de SQLAlchemy para revisión manual.

??? failure "Una aplicación \"saludable\" durante un despliegue atascado"
    Una aplicación aparecía como saludable mientras sus pods nuevos se reiniciaban sin parar detrás de los anteriores. Ahora estar saludable requiere que el despliegue esté completo, y se reportan los ciclos de reinicio.

??? failure "Se llenó el disco del plano de control"
    Viejas construcciones locales con `docker build` dejaron 24.7 GB de imágenes huérfanas en `main`, que también es el plano de control de k3s. Ahora las construcciones solo ocurren en el grupo de BuildKit, y las pruebas se ejecutan en un nodo trabajador con [`make test-remote`](../develop/testing.md#remote-tests).

??? failure "La GPU no podía reservar memoria después de un reinicio"
    CUDA en esa tarjeta reserva memoria de la RAM del sistema, y la memoria fragmentada hacía fallar reservas de 256 MB aunque hubiera gigabytes libres. Ahora el manual de actualización libera cachés y compacta la memoria antes de que el nodo vuelva a unirse.

??? failure "El token de Renovate estuvo vencido cinco semanas sin que nadie lo notara"
    Un token personal detrás de una CronJob dejó de funcionar y nadie se dio cuenta. Ahora Renovate se ejecuta como un complemento de rendimiento con un token nuevo de la aplicación de GitHub en cada ejecución, y sus ejecuciones se ven en la interfaz.

## Hacia dónde va {#where-it-is-going}

A corto plazo: tareas posteriores al despliegue (migraciones, pruebas rápidas de la nueva versión) y mudar a `needs:` las bases de datos que las aplicaciones crearon a mano. Jenkins y ArgoCD ya se retiraron. A largo plazo: varios clústeres y destinos en la nube a partir del mismo `rendimiento.yaml`, y un proyecto de código abierto que otros puedan operar y al que puedan contribuir. Vea la [Hoja de ruta](../future/roadmap.md) y [Convertirse en un proyecto de código abierto](../future/open-source.md).
