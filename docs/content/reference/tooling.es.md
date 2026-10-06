# Herramientas

## Objetivos de `make` {#make-targets}

| Objetivo | Hace |
|---|---|
| `make generate` | controller-gen: código de copia profunda y CRD |
| `make test` | generar, vet, todas las pruebas, revisión de tipos de la interfaz, *aquí* |
| `make test-remote` | lo mismo en un nodo trabajador (`hack/test-remote.sh`) |
| `make test-db` | un Postgres 17 desechable en `127.0.0.1:55432` |
| `make itest` | construcciones de integración en el clúster (`-tags integration`) |
| `make ui` / `make build` | la interfaz / un programa local |
| `make image` | construye y envía la imagen de la plataforma con BuildKit |
| `make railpack-image` | construye y envía la imagen con la herramienta de Railpack |
| `make deploy` | `kubectl apply -k deploy` |
| `make docs-codemap` | vuelve a generar el [mapa del código](code-map.md) |
| `make docs-check` | revisa cada diagrama de Mermaid del libro con el propio Mermaid |

## Generadores y ayudantes en `hack/` {#generators-and-helpers-in-hack}

| Herramienta | Hace |
|---|---|
| `hack/test-remote.sh` | corre las pruebas en un pod en un nodo trabajador, con un Postgres acompañante y una caché local del nodo ([Pruebas](../develop/testing.md#remote-tests)) |
| `hack/codemap` | recorre el repositorio con `go/ast` y escribe una tabla de cada paquete, tipo y función con la primera oración de su comentario de documentación |
| `hack/undoc` | enumera los nombres exportados sin comentario de documentación; la meta es cero |
| `hack/checkdiagrams` | un guion de Node que analiza cada bloque ` ```mermaid `. Los diagramas se dibujan en el navegador, así que `mkdocs build --strict` no ve sus errores de sintaxis. En los diagramas de secuencia, un `;` o un `#` dentro de un mensaje rompe el análisis. |

## El libro {#the-book}

```bash
python3 -m venv .venv && . .venv/bin/activate
pip install -r docs/requirements.txt
cd docs && mkdocs serve        # vista previa en vivo en http://127.0.0.1:8000
mkdocs build --strict          # lo que corre la construcción de la imagen; falla con ligas rotas
```

- **MkDocs Material** genera el libro a partir del Markdown de `docs/content/`. La navegación está en `docs/mkdocs.yml`.
- **Los diagramas** son bloques de código de Mermaid (` ```mermaid `), que se dibujan en el navegador.
- **Los fragmentos de código** se incluyen desde el código fuente, así nunca se quedan viejos:
  `--8<-- "internal/dns/dns.go:provider"` incluye las líneas entre `// --8<-- [start:provider]` y `// --8<-- [end:provider]`. Una ruta con `:A:B` incluye de la línea A a la B.
- **Las páginas en español** están junto a las de inglés (`spec.es.md` junto a `spec.md`; mkdocs-static-i18n construye `/es/`). Una página sin traducción usa la de inglés. Los encabezados traducidos conservan el ancla en inglés (`## Ejemplo {#example}`), así las ligas funcionan en los dos idiomas.
- **La imagen** (`docs/Dockerfile`) tiene tres etapas: Go vuelve a generar el mapa del código, Python construye el sitio y un nginx sin privilegios lo sirve en :8080. El `rendimiento.yaml` del repositorio lo despliega, con una dirección de la red local de MetalLB.

## Comandos útiles {#useful-commands}

```bash
kubectl get app,radd                                   # todo lo que administra rendimiento
kubectl -n rendimiento-system logs deploy/rendimiento -f
kubectl -n rendimiento-builds get pods -w              # las construcciones conforme ocurren
kubectl -n devops-tools get pods -l app=buildkitd -o wide
kubectl annotate radd <name> rendimiento.ai/paused=true
```
