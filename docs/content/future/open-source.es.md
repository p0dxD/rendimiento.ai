# Código abierto

El repositorio [rendimiento-ai/rendimiento](https://github.com/rendimiento-ai/rendimiento) es público, con la licencia **Apache-2.0**. Cualquiera puede instalarlo desde una versión publicada: en una computadora con el instalador, o en un clúster de Kubernetes con el paquete de Helm. Lo que falta para que sea un proyecto que otras personas instalen y al que contribuyan es más que nada *empaque y proceso*, no funciones nuevas. Esta página dice qué hay y qué falta.

## Que se pueda instalar en cualquier lado {#make-it-installable-anywhere}

Cada ajuste que suponía este clúster (`registry.example.lan:5000`, el dominio del operador, `letsencrypt-prod`, el espacio de nombres `devops-tools`) es una variable de entorno (vea [Ajustes](../reference/config.md)). Hay tres formas de instalarlo:

- **En una computadora, sin Kubernetes:** `curl -fsSL https://rendimiento.ai/instalar | sh` descarga el programa de la última versión y lo corre como servicio del usuario con containerd (o Docker). Vea [En una computadora](../environment/deploy.md#on-one-computer).
- **En un clúster, con Helm:** `chart/rendimiento` instala la plataforma con la imagen pública, con valores para esos ajustes, su Postgres (incluido o externo) y el grupo de BuildKit. Vea [Instalar con Helm](../environment/deploy.md#installing-with-helm).
- **Con el `deploy/` de este repositorio**, más una configuración privada, como lo hace este clúster.

Falta:

- **Una primera experiencia que funcione en todas partes.** La página Entorno ya revisa la entrada, cert-manager, el almacenamiento, un registro y BuildKit, y dice cómo arreglar lo que falta. Falta agregar **instalaciones con un clic** de las piezas que faltan a través del catálogo de complementos (un registro, BuildKit, cert-manager).
- **Proveedores de DNS además de Cloudflare** (Route 53, y un proveedor «manual» que solo muestra el registro que hay que crear). Vea la [receta](../develop/recipes.md#add-a-dns-provider).
- **Un inicio rápido probado**, lo primero que intenta quien llega: de cero a una aplicación desplegada en menos de diez minutos, en una máquina limpia amd64 y en k3d o kind.

## Versiones e imágenes para varias arquitecturas {#releases-and-multi-arch-images}

rendimiento prueba cada cambio antes de que llegue a `main` ([en cada envío](../develop/testing.md#on-every-push)). GitHub Actions solo construye y publica, sin secretos propios:

- **Imágenes** (`.github/workflows/imagen.yml`): `ghcr.io/rendimiento-ai/rendimiento` para **amd64 y arm64**, como `:main` y `:sha-<commit>` en cada cambio a `main`, y `:1.2.3`, `:1.2` y `:latest` con una etiqueta `v1.2.3`. El Dockerfile compila de forma cruzada, así que no hay emulación.
- **Programas y paquete de Helm** (`.github/workflows/binarios.yml`), con cada etiqueta `v*`: una versión de GitHub con `rendimiento-linux-amd64`, `rendimiento-linux-arm64` y sus `SHA256SUMS` (lo que descarga el instalador), y el paquete de Helm (desde `v0.1.3`) con ese número como `version` y como `appVersion`, adjunto a la versión y publicado en `oci://ghcr.io/rendimiento-ai/charts/rendimiento`. En el repositorio, `chart/rendimiento` conserva `appVersion: main`, la imagen del código que tiene al lado.
- Las primeras versiones, de `v0.1.0` a `v0.1.2`, salieron el 2026-11-05.

Falta:

- **Versionado semántico.** Subir la versión de la CRD (`v1alpha1` → `v1beta1`) solo con un plan de conversión.
- **Un registro de cambios** en `CHANGELOG.md` (por ahora, las notas de cada versión se generan con las solicitudes de cambios juntadas).
- **Imágenes firmadas** (cosign sin llave) y un SBOM (`buildx --sbom`), las dos fáciles de agregar en Actions.
- La imagen de Railpack de la misma forma (`make railpack-image` → una tarea de Actions).

## Cómo contribuir {#contribution-flow}

| Archivo | Estado |
|---|---|
| `LICENSE` | **Apache-2.0**: lo normal en el ecosistema de Kubernetes, con concesión de patentes y amigable con las empresas |
| `CONTRIBUTING.md` | ya está: preparación ([Preparar y construir](../develop/setup.md)), pruebas, la regla de «el libro en el mismo cambio», estilo de los commits |
| `CODE_OF_CONDUCT.md` | falta: Contributor Covenant |
| `SECURITY.md` | falta: cómo reportar vulnerabilidades en privado (avisos privados de GitHub) |
| `.github/ISSUE_TEMPLATE/` | falta: reporte de error (versión, clúster o computadora, registros), solicitud de función |
| `.github/pull_request_template.md` | falta: qué, por qué, cómo se probó, ¿se actualizó el libro? |
| Revisiones de las solicitudes de cambios | rendimiento corre sus pasos (las pruebas de Go con envtest y Postgres, las pruebas en el navegador, el libro) en cada rama del repositorio y los reporta en la solicitud de cambios. **Una solicitud desde un fork todavía no recibe ninguna revisión**: quien no es del equipo no recibe respuesta automática. |

Marque unos cuantos issues como **good first issue**. Las reestructuraciones de [Reestructuración y salud del código](../develop/refactoring.md) (dividir `server.go`, tygo) son ideales: bien acotadas, mecánicas y bien probadas.

## Documentación {#documentation}

Este libro es la documentación, publicada en inglés en [learn.rendimiento.ai](https://learn.rendimiento.ai) y en español en [aprende.rendimiento.ai](https://aprende.rendimiento.ai), por rendimiento mismo (el servicio `docs`). Falta, para un público amplio:

- Separarlo en un camino de *usuario* (Uso, Referencia) y uno de *quien contribuye* (Arquitectura, Desarrollo). La navegación ya lo hace.
- Mover las páginas sobre este clúster (Su entorno) bajo un encabezado de «despliegue de ejemplo».

## Gobierno y sostenibilidad {#governance-and-sustainability}

- Empezar como **BDFL**: deciden los fundadores. Escribir el proceso de decisión en `GOVERNANCE.md` cuando haya quienes contribuyan con regularidad.
- **Decir en el README qué abarca**: que los agentes de código desplieguen en infraestructura propia, con seguridad, con roles, un registro de auditoría y personas que aprueban producción. Es decir, despliegues con opinión, no la paridad con Jenkins.
- Una **hoja de ruta** pública (GitHub Projects) que refleje la [Hoja de ruta](roadmap.md).
- **La postura de seguridad** importa en una herramienta con poderes parecidos a los de un administrador del clúster: RBAC de mínimo privilegio y suplantación para los complementos (ya están), un registro de auditoría y un modelo de amenazas documentado ([Seguridad](../architecture/security.md)).
- **Seguir con Apache-2.0.** Pasar después a una licencia de código disponible (BSL, SSPL) divide a la comunidad, como pasó con Terraform (OpenTofu) y Redis (Valkey). Los ingresos vienen de un plano de control alojado, un nivel de gobernanza de pago y la IA medida, no de cerrar el código.

## Lista de pendientes {#checklist}

- [x] LICENSE (Apache-2.0) y CONTRIBUTING
- [ ] CODE_OF_CONDUCT y SECURITY
- [x] Revisiones de las solicitudes de cambios del propio repositorio (rendimiento prueba cada rama)
- [ ] Revisiones de las solicitudes de cambios desde forks
- [x] Flujo de versiones: imágenes para varias arquitecturas en ghcr.io, programas para Linux con sumas de verificación
- [ ] Registro de cambios, imágenes firmadas, SBOM
- [x] Paquete de Helm, publicado con cada versión
- [x] Instalación en una computadora sin Kubernetes (`/instalar`)
- [ ] Inicio rápido en una máquina limpia y en k3d, probado
- [ ] Un proveedor de DNS además de Cloudflare
- [x] El libro, público en inglés y en español
- [ ] Good first issues
