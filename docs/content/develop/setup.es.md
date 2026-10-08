# Preparar y construir

## Herramientas {#tools}

| Herramienta | Versión | Dónde está en `main` | Se usa para |
|---|---|---|---|
| Go | 1.27 | `~/.local/go/bin` | construir, probar, generadores |
| Node.js + npm | 20 | el sistema | la interfaz |
| controller-gen | v0.22.0 | `~/go/bin` | código de copia profunda y CRD a partir de los tipos de Go |
| setup-envtest | v0.25.1 | `~/go/bin` | descarga un servidor de la API de Kubernetes de prueba + etcd |
| kubectl | la del clúster | el sistema | todo lo del lado del clúster |
| docker | cualquiera | el sistema | solo para correr el cliente `buildctl` de `make image` |

El Makefile agrega `~/.local/go/bin` y `~/go/bin` al `PATH` de sus objetivos. Para una máquina nueva:

```bash
# Go
curl -sL https://go.dev/dl/go1.27.1.linux-arm64.tar.gz | tar -C ~/.local -xz
export PATH=$HOME/.local/go/bin:$HOME/go/bin:$PATH
go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.22.0
go install sigs.k8s.io/controller-runtime/tools/setup-envtest@v0.25.1
```

## El repositorio {#the-repository}

```text
rendimiento.ai/
├── cmd/rendimiento/        main.go: ajustes, conexiones, arranque (la raíz de composición)
├── api/v1alpha1/           recursos personalizados App y Addon (tipos de Go → CRD)
├── internal/
│   ├── spec/               rendimiento.yaml: tipos, valores predeterminados, validación
│   ├── detect/             qué contiene un repositorio (lenguaje, puerto, pruebas, necesidades)
│   ├── generate/           propuestas a partir de la detección (+ Dockerfiles en templates/)
│   ├── github/             la aplicación de GitHub: JWT, tokens, cliente de la API, árboles, OAuth
│   ├── platform/           orquestación: avisos web → ejecuciones → versiones → objetos App
│   ├── pipeline/           integración continua: plan, corredor, ejecutor de Kubernetes (pods de construcción)
│   ├── store/              Postgres: aplicaciones, ejecuciones, registros, versiones, sesiones, complementos
│   ├── render/             especificación + imágenes → objetos de Kubernetes (puro)
│   ├── controller/         controladores de App y Addon, ganchos, necesidades
│   ├── addon/              generación de complementos (Helm, kustomize), sincronización con git, catálogo
│   ├── renovate/           el complemento de Renovate
│   ├── catalog/            la página Servicios
│   ├── environment/        la página Entorno
│   ├── dns/                registros de Cloudflare, DNS dinámico
│   ├── events/             concentrador de actualizaciones en vivo (SSE)
│   └── api/                API HTTP, autenticación, avisos web, configuración, servidor de la interfaz
├── web/                    la interfaz de React (src/) y su incrustación (embed.go)
├── templates/dockerfiles/  plantillas de Dockerfile para las tecnologías detectadas
├── deploy/                 manifiestos de la propia plataforma (kubectl apply -k)
├── docs/                   este libro (MkDocs Material)
├── hack/                   herramientas de desarrollo: test-remote.sh, codemap, undoc
├── Dockerfile              la imagen de la plataforma (interfaz + Go, en varias etapas)
├── Makefile                construir, probar, imagen, desplegar
└── rendimiento.yaml        cómo despliega rendimiento este libro
```

## Objetivos de `make` {#make-targets}

```makefile
--8<-- "Makefile"
```

| Objetivo | Hace | Dónde corre |
|---|---|---|
| `make generate` | código de copia profunda y CRD desde `api/v1alpha1` e `internal/spec` | aquí (ligero) |
| `make test-remote` | generar, vet, todas las pruebas de Go, revisión de tipos de la interfaz | **un nodo trabajador** |
| `make test` | lo mismo | aquí: evítelo en `main` |
| `make test-db` | un Postgres desechable para pruebas locales | aquí |
| `make itest` | construcciones reales en el grupo de BuildKit (Dockerfile y Railpack) | el clúster |
| `make image` | construye y envía `rendimiento:latest` | el grupo de BuildKit |
| `make railpack-image` | la imagen con la herramienta de Railpack para los pods de construcción | el grupo de BuildKit |
| `make deploy` | `kubectl apply -k deploy` | — |
| `make docs-codemap` | vuelve a generar `docs/content/reference/code-map.md` | aquí |
| `make ui`, `make build` | la interfaz y un programa local | aquí (pesado) |

## El ciclo de un cambio {#the-change-loop}

```bash
# 1. editar el código (y el libro)
# 2. respuesta rápida sobre el paquete que tocó (lo bastante ligero para main)
go vet ./internal/render && go test ./internal/render
# 3. todo, en el clúster: envíe una rama, y rendimiento la prueba y la
#    construye como la de cualquier aplicación (o make test-remote, en un nodo trabajador)
git push origin HEAD:mi-cambio
# 4. intégrela; cuando pase la ejecución de main, el libro se vuelve a desplegar
#    solo y la página Entorno ofrece la plataforma nueva: Actualizar
```

## Ejecutar rendimiento {#running-rendimiento}

rendimiento es un controlador: **nunca corra una segunda copia contra el clúster de producción.** Conciliaría los mismos objetos `App` que la copia real, y las dos se pelearían.

Para ejecutarlo fuera del clúster, use un clúster aparte (por ejemplo [k3d](https://k3d.io) o [kind](https://kind.sigs.k8s.io), en una laptop) con su propio Postgres:

```bash
make test-db                                     # Postgres en 127.0.0.1:55432
kubectl apply -f deploy/crds/                    # contra el clúster de prueba
export DATABASE_URL=postgres://postgres:test@127.0.0.1:55432/rendimiento?sslmode=disable
export ALLOWED_USERS=<su-cuenta-de-github> BASE_URL=http://localhost:8080 SETUP_TOKEN=dev LEADER_ELECTION=false
go run ./cmd/rendimiento                         # usa su kubeconfig actual
```

Luego abra `http://localhost:8080/api/setup/github?token=dev` para crear una aplicación de GitHub de desarrollo. Los avisos web necesitan una URL pública (un túnel como `cloudflared`); si no, use el botón **Ejecutar** en lugar de los envíos.

## Trabajar en la interfaz {#working-on-the-ui}

```bash
cd web && npm ci
npm run dev          # Vite en :5173 con recarga en caliente; /api pasa a localhost:8080
npm run typecheck    # lo que corren las pruebas
npm run build        # web/dist, que incrusta la construcción de Go
```

El servidor de desarrollo necesita una API de rendimiento en `localhost:8080`, la local de arriba. La galleta de sesión la pone el inicio de sesión de ese servidor, así que primero inicie sesión en `http://localhost:8080`.

## Generar código {#generating-code}

`make generate` corre **controller-gen** dos veces:

- `object` escribe `zz_generated.deepcopy.go`: los métodos `DeepCopy` que necesita cada tipo de Kubernetes (la caché entrega copias);
- `crd` escribe `deploy/crds/*.yaml` a partir de los tipos de Go y sus marcas `// +kubebuilder:` (validación, columnas para mostrar, alcance, nombres cortos).

Las dos salidas se confirman en git. Después de cambiar `api/v1alpha1` o `internal/spec`, vuelva a generar, aplique el CRD y reinicie la plataforma.
