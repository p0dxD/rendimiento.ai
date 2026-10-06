# Código abierto

El repositorio [p0dxD/rendimiento.ai](https://github.com/p0dxD/rendimiento.ai) ya es público. Convertirlo en un proyecto que otras personas instalen y al que contribuyan es más que nada *empaque y proceso*, no funciones nuevas. Esta página es la lista de pendientes.

## Que se pueda instalar en cualquier lado {#make-it-installable-anywhere}

Hoy varios ajustes suponen este clúster (`registry.example.lan:5000`, `joserod.space`, `letsencrypt-prod`, el espacio de nombres `devops-tools`). Cada uno ya es una variable de entorno (vea [Ajustes](../reference/config.md)). Lo que falta:

- **Un paquete de Helm** (`charts/rendimiento`) que genere `deploy/` con valores para esos ajustes, Postgres (incluido o externo) y el grupo de BuildKit. Así la gente puede correr `helm install rendimiento oci://ghcr.io/…`.
- **Una primera experiencia que funcione en todas partes.** La página Entorno ya revisa la entrada, cert-manager, el almacenamiento, un registro y BuildKit, y dice cómo arreglar lo que falta. Falta agregar **instalaciones con un clic** de las piezas que faltan a través del catálogo de complementos (un registro, BuildKit, cert-manager).
- **Proveedores de DNS además de Cloudflare** (Route 53, y un proveedor "manual" que solo muestre el registro que hay que crear). Vea la [receta](../develop/recipes.md#add-a-dns-provider).
- **Una guía de inicio rápido probada** en k3d o kind en una laptop, lo primero que prueba alguien nuevo.

## Versiones e imágenes para varias arquitecturas {#releases-and-multi-arch-images}

Hoy las imágenes se construyen solo para arm64, con el propio BuildKit del clúster. Para los demás:

- **GitHub Actions** con las etiquetas `v*`: correr las pruebas, luego `docker buildx build --platform linux/amd64,linux/arm64` y enviar a **ghcr.io/p0dxD/rendimiento:vX.Y.Z**. La etapa de Go compila de forma nativa para otras plataformas: agregue `--platform=$BUILDPLATFORM` a la etapa de construcción y pase `GOARCH=$TARGETARCH`, así no hace falta emulación. Solo la etapa de la interfaz no depende de la plataforma.
- **Versiones semánticas.** Suba la versión del CRD (`v1alpha1` → `v1beta1`) solo con un plan de conversión.
- **Una bitácora de cambios** generada a partir de Conventional Commits, o escrita a mano en `CHANGELOG.md`.
- **Imágenes firmadas** (cosign sin llaves) y una lista de componentes del software (`buildx --sbom`), las dos fáciles de agregar en Actions.
- La imagen de Railpack de la misma forma (`make railpack-image` → un trabajo de Actions).

## Cómo contribuir {#contribution-flow}

| Archivo | Contenido |
|---|---|
| `LICENSE` | Se recomienda **Apache-2.0**: lo normal en el ecosistema de Kubernetes, incluye la concesión de patentes y le sirve a las empresas. |
| `CONTRIBUTING.md` | la preparación (liga a [Preparar y construir](../develop/setup.md)), las pruebas, la regla de "el libro en la misma solicitud", el estilo de las confirmaciones |
| `CODE_OF_CONDUCT.md` | Contributor Covenant |
| `SECURITY.md` | cómo reportar vulnerabilidades en privado (avisos privados de GitHub) |
| `.github/ISSUE_TEMPLATE/` | reporte de fallo (versión, clúster, registros), petición de función |
| `.github/pull_request_template.md` | qué, por qué, cómo se probó, ¿se actualizó el libro? |
| `.github/workflows/ci.yml` | `go vet`, `go test` con envtest y un servicio de Postgres, revisión de tipos de la interfaz, `mkdocs build --strict` en cada solicitud |

Marque unas cuantas incidencias como **good first issue**. Las reestructuraciones de [Reestructuración](../develop/refactoring.md) (dividir `server.go`, tygo) son ideales: bien delimitadas, mecánicas y bien probadas.

## Documentación {#documentation}

Este libro es la documentación. Para un público general:

- Publíquelo con **GitHub Pages** (`mkdocs gh-deploy` en Actions) además de la copia en la red local.
- Divídalo en un camino para *usuarios* (Usarlo, Referencia) y otro para *contribuidores* (Arquitectura, Desarrollarlo). La navegación ya lo hace.
- Mueva las páginas sobre este clúster (Su entorno) bajo un encabezado de "despliegue de ejemplo".

## Gobierno y sostenibilidad {#governance-and-sustainability}

- Empiece como **dictador benévolo** (usted decide). Escriba el proceso de decisión en `GOVERNANCE.md` cuando haya contribuidores habituales.
- **Diga qué entra y qué no** en el README: "despliegues al estilo Heroku en su propio Kubernetes; el 80 % con opiniones firmes, no igualar a Jenkins".
- Una **hoja de ruta** pública (GitHub Projects) que refleje la [Hoja de ruta](roadmap.md).
- **La postura de seguridad** importa en una herramienta con poderes casi de cluster-admin. RBAC de privilegio mínimo y suplantación para los complementos (ya están), una bitácora de auditoría y un modelo de amenazas documentado ([Seguridad](../architecture/security.md)).

## Lista de pendientes {#checklist}

- [ ] LICENSE, CONTRIBUTING, CODE_OF_CONDUCT, SECURITY
- [ ] Flujo de integración continua en las solicitudes de incorporación
- [ ] Flujo de versiones: imágenes para varias arquitecturas en ghcr.io, bitácora de cambios
- [ ] Paquete de Helm
- [ ] Guía de inicio rápido en k3d, probada en la integración continua
- [ ] Un proveedor de DNS además de Cloudflare
- [ ] El libro en GitHub Pages
- [ ] Incidencias para quien empieza
