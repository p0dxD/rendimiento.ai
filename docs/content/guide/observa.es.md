# Bibliotecas: rendimiento para Node, Python y Java {#libraries-rendimiento-for-node-python-and-java}

Con `observe: true`, el [universo](universe.md#latency-on-every-line) ya mide cada llamada HTTP, consulta a la base de datos y mensaje de un servicio en Java, Node o Python, sin cambiar el código. Las bibliotecas **observa** agregan lo que solo el código sabe: los momentos del negocio («cobrar», «generar el reporte»), eventos dentro de ellos y banderas de funciones. Son capas delgadas sobre [OpenTelemetry](https://opentelemetry.io), así que todo lo que registran se une a las mismas trazas.

Las bibliotecas viven en este repositorio, en `sdk/`, y todavía no se publican en npm, PyPI ni Maven Central; se instalan desde git.

## Lo que ofrece cada una {#what-each-one-offers}

| | Node | Python | Java |
|---|---|---|---|
| Iniciar las trazas (no hace nada con el agente de la plataforma) | `iniciar()` | `iniciar()` | `Observa.iniciar()` |
| Un tramo alrededor de un trabajo, dentro del actual | `tramo(nombre, fn, atributos)` | `with tramo(nombre, atributos):` o `@tramo(nombre)` | `Observa.tramo(nombre, atributos, () -> …)` o `try (var t = Observa.abrir(nombre))` |
| Un evento en el tramo actual | `evento(nombre, atributos)` | `evento(nombre, atributos)` | `Observa.evento(nombre, atributos)` |
| Llevar la traza a otro servicio | `llamar(url, init)` (fetch) | `cabeceras()`, `llamar(url)` | `Observa.cabeceras()` |
| Una bandera, registrada en la traza | `bandera(nombre, porDefecto)` | `bandera(nombre, por_defecto)` | `Observa.bandera(nombre, porDefecto)` |

Cada función tiene también un nombre en inglés: `start`, `span`, `event`, `call` / `headers`, `flag`.

- **Los tramos** se vuelven el tramo actual, así que las llamadas que se hacen dentro quedan anidadas en la traza. Un error marca el tramo como fallido y se vuelve a lanzar.
- **Los eventos** van en el tramo actual; si no hay ninguno, se vuelven un tramo corto propio para no perderse.
- **Las banderas** se leen de `RENDIMIENTO_FLAG_<NOMBRE>` (`1`, `true`, `on`, `yes`, `sí`) y se registran como `rendimiento.flag.<nombre>` en el tramo actual, así una traza dice qué lado tomó. Es el gancho que usarán las pruebas A/B y las versiones canario.
- **Iniciar**: dentro del clúster con `observe`, el agente de la plataforma ya traza e `iniciar()` no hace nada. En otro lado (su computadora, un servicio sin el agente) inicia el SDK de OpenTelemetry cuando está instalado y `OTEL_EXPORTER_OTLP_ENDPOINT` está definido; sin ellos, cada función sigue sirviendo y no registra nada.

## Node {#node}

npm no puede instalar una carpeta de un repositorio de git, así que mientras no se publique, instálela desde una copia:

```bash
git clone https://github.com/p0dxD/rendimiento.ai
npm install ./rendimiento.ai/sdk/node   # o copie sdk/node a su repositorio e instale esa carpeta
```

```js
const { tramo, evento, bandera } = require("@rendimiento/observa");

app.post("/pedidos/:id/cobrar", (req, res) =>
  tramo("cobrar", async () => {
    const nuevo = bandera("nuevo-cobro");
    const pago = await (nuevo ? cobrarV2 : cobrar)(req.params.id);
    evento("pagado", { monto: pago.monto });
    res.json(pago);
  }, { "pedido.id": req.params.id }));
```

`@opentelemetry/api` es una dependencia compartida (peer). Para trazar sin el agente, instale también `@opentelemetry/sdk-node` y `@opentelemetry/auto-instrumentations-node`.

## Python {#python}

```bash
pip install "rendimiento-observa @ git+https://github.com/p0dxD/rendimiento.ai#subdirectory=sdk/python"
# sin el agente: agregue [sdk] para el SDK de OpenTelemetry y el exportador OTLP
```

```python
from rendimiento_observa import tramo, evento, bandera

@tramo("generar-reporte")
def generar(usuario):
    if bandera("reporte-rapido"):
        ...
    evento("reporte-listo", {"filas": 1200})
```

El agente instrumenta `requests` y `httpx`; para otro cliente, pase `cabeceras()` como sus encabezados.

## Java {#java}

Constrúyala e instálela localmente (`mvn -f sdk/java install`) y dependa de `ai.rendimiento:observa:0.1.0`:

```java
import ai.rendimiento.observa.Observa;

Pago pago = Observa.tramo("cobrar", Map.of("pedido.id", id), () -> {
  Pago p = Observa.bandera("nuevo-cobro") ? cobrarV2(id) : cobrar(id);
  Observa.evento("pagado", Map.of("monto", p.monto()));
  return p;
});
```

Las excepciones verificadas pasan a través de `tramo`. Con el agente, los tramos se unen a sus trazas por medio de `GlobalOpenTelemetry`.

## Pruebas {#tests}

Cada biblioteca se prueba como una [tarea](tasks.md) del `rendimiento.yaml` de rendimiento mismo (`sdk-node`, `sdk-python`, `sdk-java`) en cada ejecución que cambia su carpeta, ramas incluidas.
