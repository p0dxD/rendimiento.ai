# El universo: todas las aplicaciones desde arriba {#the-universe-every-app-from-above}

La página **Universo** muestra todo su mundo de aplicaciones en un solo mapa: cada aplicación, los servicios que ejecuta, las bases de datos y cachés que usa, quién llama a quién y a qué llega internet. La página Servicios enlista cada servicio; el universo muestra cómo encajan entre sí.

No hace falta configurar nada: el mapa se arma con lo que rendimiento ya sabe.

## Qué hay en el mapa {#what-is-on-the-map}

- **Los sistemas** son los círculos grandes: uno por cada aplicación de rendimiento (borde continuo). Con **Desplegado de otra forma** activado, también aparecen las aplicaciones de ArgoCD, Helm o `kubectl` y los complementos (borde punteado), uno por espacio de nombres. **Los internos del clúster** (ingress, cert-manager, monitoreo…) se ocultan, salvo que una aplicación los llame o usted los active.
- **Los nodos** son lo que corre dentro de cada sistema:
    - un **círculo** es un servicio; las letras de adentro son su lenguaje (`JS`, `Py`, `J`, `Go`);
    - un **cuadrado** es una base de datos o caché que la aplicación corre por sus `needs` (`PG`, `R`);
    - un **círculo punteado** es una tarea o proceso que solo llama a otros (una tarea programada).
- **El anillo** de cada servicio es su última [revisión de disponibilidad](reliability.md): verde cuando pasaron todas las revisiones de los últimos 15 minutos, rojo cuando una falló, gris cuando no se ha revisado.
- **Internet** está en el centro, con una línea hacia cada servicio que tiene una dirección pública.

## De dónde salen las líneas {#where-the-lines-come-from}

| Línea | Origen | Ejemplo |
|---|---|---|
| continua | una entrada `needs:` en el `rendimiento.yaml` de quien llama | `needs: [postgres]`, `needs: [{service: api}]` |
| punteada | una variable de entorno de quien llama que apunta al servicio (la misma detección del [catálogo de servicios](needs.md)) | `API_URL=http://api.wellness.svc.cluster.local` |
| de puntos, desde Internet | una dirección pública (un Ingress) dirigida al servicio | `https://wellness.example.com` |

Una línea que se encuentra de las dos formas se dibuja continua. Los valores que se leen de secretos no se ven, así que una conexión cuya dirección solo está en un secreto aparece únicamente cuando se declara con `needs:`. Declarar las necesidades completa el mapa.

## Cómo usarlo {#using-it}

- **Haga zoom** con la rueda del ratón o pellizcando, y **arrastre** para moverse; los botones **+**, **−** y **⤢** de la esquina acercan, alejan y vuelven a la vista completa.
- **Zoom a aplicaciones**: las fichas sobre el mapa eligen una o varias aplicaciones (u otros sistemas); el mapa se desliza hacia ellas y atenúa lo demás. **Todo** vuelve al mapa completo. Un doble clic en el círculo de una aplicación, o **Zoom a …** en el panel lateral, elige solo esa. La elección queda en la dirección (`?apps=wellness,jobsentry`), así que una vista se puede guardar o compartir.
- **Pase el cursor** sobre un nodo para iluminar sus conexiones; lo demás se atenúa.
- **Haga clic** en un nodo para fijarlo. El panel lateral muestra qué es, su salud y sus pods, sus direcciones públicas, **Llama a** (lo que usa) y **La llaman** (quién lo usa), cada una con la variable o dirección y de dónde sale la línea. Haga clic en un nombre de esas listas para ir a ese nodo.
- **Abrir aplicación** lleva a la página de la aplicación; **Abrir en Servicios** lleva a su entrada en el catálogo.
- **Actualizar** reconstruye el catálogo desde el clúster en ese momento (si no, tiene a lo más 30 segundos, y la página se recarga cada minuto).

## La latencia en cada línea {#latency-on-every-line}

Con las trazas activadas, cada línea lleva también lo medido: **llamadas por minuto**, la proporción que **falló** y cuánto tardaron según quien llama (**p50**, la mitad fue más rápida; **p95**, el 5% más lento). Elija el periodo (15 minutos, una hora, 24 horas) arriba. Las líneas se vuelven **más gruesas** con más llamadas, **rojas** cuando falla el 5% o más y **ámbar** cuando el 5% más lento tarda medio segundo o más. Al pasar el cursor por un servicio se ve el tiempo típico de sus líneas.

Las líneas **vistas solo en las trazas** (nadie las declaró) se dibujan en verde: una llamada a un servicio que ningún `needs:` menciona, a una base de datos o a una API **fuera del clúster**, agrupadas en *Fuera del clúster*.

### Cómo activarla {#turning-it-on}

1. Instale el complemento **OpenTelemetry Collector** (Complementos). Recibe las trazas y escribe solo los números, el grafo de servicios, en el almacén de métricas; las trazas mismas no se guardan. Ponga como dirección de escritura su VictoriaMetrics o Prometheus. Antes se descartan las revisiones de salud (las sondas de Kubernetes, `kube-probe/…`, y las revisiones de disponibilidad de rendimiento, `rendimiento-uptime/…`), para que no dibujen una línea desde Internet hacia cada servicio.
2. En la plataforma, defina `TELEMETRY_ENDPOINT` con el colector y `METRICS_URL` con el almacén de métricas ([Ajustes](../reference/config.md#traces-and-the-universe)).
3. En el `rendimiento.yaml` de una aplicación, agregue `observe: true` (o `observe: true` en un solo servicio). El siguiente despliegue agrega las trazas.

Lo que recibe cada servicio:

| `language` | Lo que agrega rendimiento |
|---|---|
| `java` | el agente de OpenTelemetry para Java, cargado con `JAVA_TOOL_OPTIONS` |
| `node` | el agente de OpenTelemetry para Node, cargado con `NODE_OPTIONS` (después de las opciones propias de la aplicación) |
| `python` | el agente de OpenTelemetry para Python, en `PYTHONPATH` |
| `go`, otros, imágenes listas | solo las variables `OTEL_*`, para un SDK de OpenTelemetry en el código |

Cada servicio observado se llama `aplicación/servicio` (`OTEL_SERVICE_NAME`); así una línea medida encuentra sus nodos. Un contenedor de inicio pequeño copia el agente al pod; agrega memoria (el de Java es el que más, unos 100 MB) y algo de tiempo de arranque, así que actívelo donde quiera ver. Se conserva una cuarta parte de las trazas (`TELEMETRY_SAMPLE_RATIO`), que basta para los promedios.

!!! note "Python en Alpine"
    El agente de Python está hecho para glibc. Un servicio con una imagen Alpine (musl) puede no cargarlo: use una imagen basada en Debian, o ponga `observe: false` en ese servicio.

Un servicio en Go se mide de todos modos desde el otro lado: las llamadas *hacia* él desde un servicio observado las cronometra quien llama.

Para agregar a las trazas los momentos que solo el código conoce (un pago, un reporte) y banderas de funciones, use las [bibliotecas](observa.md) para Node, Python y Java.
