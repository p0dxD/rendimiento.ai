# Flujos, paso a paso

Los mismos pocos caminos por el sistema, dibujados como secuencias. Los nombres de las funciones son reales, así que puede seguir cada flecha hasta el código.

## Incorporar un repositorio {#onboarding-a-repository}

```mermaid
sequenceDiagram
    actor You as Usted
    participant UI as Interfaz
    participant API as api.Server
    participant P as platform.Platform
    participant GH as GitHub
    participant K as Kubernetes
    You->>UI: Nueva aplicación → elige un repositorio
    UI->>API: POST /api/propose
    API->>P: Propose(installation, repo, branch)
    P->>GH: lee el árbol (RepoFS)
    P->>P: detect.Detect → generate.Generate
    P->>K: ¿su espacio de nombres ya está en uso? (InspectNamespace)
    P-->>UI: propuesta: especificación, archivos generados, datos de migración
    You->>UI: ajusta el formulario → Desplegar
    UI->>API: POST /api/apps
    API->>P: Onboard(request)
    P->>P: store.CreateApp
    P->>K: crea el objeto App (esperando una construcción)
    alt el repositorio no tiene rendimiento.yaml
        P->>GH: OpenPR(rendimiento.yaml [+ Dockerfile])
        Note over GH: la rama de la solicitud se construye y recibe una comprobación.<br/>Integrarla inicia el primer despliegue
    else el repositorio ya tiene uno
        P->>P: QueueRun(rama principal) → la primera construcción empieza ya
    end
```

- **La detección** (`internal/detect`) revisa cada carpeta: `go.mod`, `package.json` (Next.js, Vite…), `pyproject.toml` o `requirements.txt` (FastAPI, Flask, Django), `pom.xml` o `build.gradle`, un `index.html`, un `Dockerfile` existente y las bibliotecas cliente de Postgres y Redis. Devuelve el lenguaje, el puerto, el comando de pruebas y las necesidades, con sus motivos.
- **La generación** (`internal/generate`) convierte eso en servicios, en un dominio bajo su zona predeterminada y en un Dockerfile de `templates/dockerfiles/` para las carpetas que no tienen uno.
- **La migración**: si en el espacio de nombres que usaría la aplicación ya hay algo en ejecución (desplegado con ArgoCD o `kubectl`), la propuesta lo describe y ofrece **adoptarlo** (vea [la adopción](controller.md#adoption-taking-over-what-is-already-running)).

## Un envío: de la confirmación al código en ejecución {#a-push-from-commit-to-running-code}

```mermaid
sequenceDiagram
    participant GH as GitHub
    participant API as api.Server
    participant P as platform.Platform
    participant DB as Postgres
    participant W as trabajador de integración continua
    participant X as KubeExecutor
    participant BK as grupo de BuildKit
    participant K as Kubernetes
    participant C as controlador de aplicaciones
    GH->>API: POST /api/webhooks/github (envío)
    API->>API: verifica la firma HMAC
    API->>P: HandlePush(repo, branch, sha)
    P->>DB: aplicaciones de este repositorio
    P->>GH: rendimiento.yaml en esta confirmación
    P->>DB: CreateRun + pasos (en cola)
    W->>DB: ClaimRun (FOR UPDATE SKIP LOCKED)
    W->>GH: crea la comprobación "en curso"
    W->>GH: archivos que cambiaron desde la última versión
    Note over W: los servicios sin cambios reutilizan su imagen
    loop cada paso, en orden de dependencias, como máximo MAX_PARALLEL_STEPS a la vez
        W->>X: Execute(step)
        X->>K: pod: clonar → (plan) → pruebas o construcción
        X->>BK: buildctl build … (desde el pod)
        BK-->>X: imagen enviada, huella
    end
    W->>DB: versión n (huellas de las imágenes + especificación)
    W->>K: actualiza el objeto App (pointApp)
    W->>GH: comprobación ✓ / ✗
    K-->>C: cambió el App
    C->>K: aplica Deployments, Services, Ingresses… (aplicación del lado del servidor)
    C->>K: actualización gradual, la salud se resume en el estado del App
    Note over P: verificación: esperar a que esté sana, comprobar cada servicio<br/>durante 5 min, revertir si uno se descompuso
```

- Los envíos a **otras ramas** se detienen después de las construcciones: no se publica nada; la comprobación aparece en la confirmación y en cualquier solicitud de incorporación.
- **Las ejecuciones manuales** (el botón *Construir ahora*) primero resuelven la rama a su confirmación actual, así la imagen, la comprobación y la detección de cambios se refieren a una confirmación real.
- Si la plataforma se reinicia a media ejecución, `RequeueOrphans` vuelve a poner la ejecución en cola al arrancar (hasta dos intentos) y se borran los pods de construcción viejos.

## Versiones y reversión {#releases-and-rollback}

```mermaid
flowchart LR
    run1[ejecución n.º 41 ✓] --> r1[versión n.º 7<br/>web@sha256:aa…<br/>api@sha256:bb…]
    run2[ejecución n.º 42 ✓] --> r2[versión n.º 8<br/>web@sha256:cc…<br/>api@sha256:bb… reutilizada]
    r1 -. "Revertir a la n.º 7" .-> r3[versión n.º 9<br/>= imágenes y especificación de la n.º 7]
    r3 --> app[objeto App<br/>release: 9]
```

Una versión fija cada servicio a la **huella** de una imagen, así lo que se ejecuta es exactamente lo que se construyó y se probó. Revertir nunca vuelve a construir: crea una versión nueva con las imágenes y la especificación anteriores, y apunta el objeto `App` a ella.

## Un cambio de complemento en git {#an-add-on-change-in-git}

```mermaid
sequenceDiagram
    participant You as Usted
    participant G as p0dxD/gitops
    participant API as api.Server
    participant S as addon.Syncer
    participant K as Kubernetes
    participant AC as controlador de complementos
    You->>G: confirma addons/umami.yaml (o Instalar en la interfaz, que lo confirma)
    G->>API: aviso web de envío
    API->>S: Trigger()
    S->>G: lee addons/*.yaml en la confirmación nueva
    S->>K: crea, actualiza o elimina objetos Addon
    K-->>AC: cambió el Addon
    AC->>AC: genera (plantilla de Helm o construcción de kustomize)
    AC->>K: prueba en seco cada objeto → vista previa
    alt adoptando y algo cambiaría, o sincronización manual
        AC->>K: estado: Blocked u OutOfSync (no se aplica nada)
    else
        AC->>K: ganchos previos → aplicar (como rendimiento-addons) → eliminar → ganchos posteriores
        AC->>K: estado: Synced
    end
```

La sincronización también corre cada 3 minutos por si se perdió un aviso web, y el controlador vuelve a sincronizar cada complemento cada 5 minutos, lo que además corrige las desviaciones.

## Una ejecución de Renovate {#a-renovate-run}

```mermaid
sequenceDiagram
    participant Sch as renovate.Runner (programador)
    participant DB as Postgres
    participant GH as GitHub
    participant K as Kubernetes
    participant R as pod de Renovate
    participant CI as integración continua de rendimiento
    Sch->>DB: ¿toca una ejecución? apartar el turno (solo uno gana)
    Sch->>GH: token de instalación nuevo, permisos, identidad del bot
    Sch->>K: secreto (token, config.js) + pod en rendimiento-builds
    R->>GH: por cada repositorio: abre o actualiza ramas y solicitudes de actualización
    GH->>CI: avisos web de envío de las ramas renovate/*
    CI->>GH: las construye y reporta las comprobaciones
    R-->>Sch: registro en JSON
    Sch->>DB: resultado de la ejecución por repositorio, registro legible
    Note over R,GH: siguiente ejecución: las solicitudes cuyas comprobaciones pasaron se integran solas<br/>(parche y menores, según la configuración) → un despliegue normal
```

## Necesidades: una base de datos para una aplicación {#needs-a-database-for-an-app}

```mermaid
sequenceDiagram
    participant C as controlador de aplicaciones
    participant K as Kubernetes
    C->>C: la especificación dice services[api].needs: [postgres]
    C->>K: espacio de nombres (si no es compartido)
    C->>K: Secret postgres-credentials (solo si falta: se genera una vez)
    C->>K: Deployment + Service postgres + volumen postgres-data
    C->>K: Deployment de api con DATABASE_URL, PGHOST… tomados del Secret
```
