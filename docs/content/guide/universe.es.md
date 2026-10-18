# El universo: todas las aplicaciones desde arriba {#the-universe-every-app-from-above}

La página **Universo** muestra todo su mundo de aplicaciones en un solo mapa: cada aplicación, los servicios que ejecuta, las bases de datos y cachés que usa, quién llama a quién y a qué llega internet. La página Servicios enlista cada servicio; el universo muestra cómo encajan entre sí.

No hace falta configurar nada: el mapa se arma con lo que rendimiento ya sabe.

## Qué hay en el mapa {#what-is-on-the-map}

El mapa es una maqueta de pueblo vista desde un cerro, en ángulo:

- **Los barrios** son las placas elevadas: grupos de aplicaciones (vea [Barrios](#barrios) más abajo). De lejos solo se ven los barrios y los caminos grandes entre ellos, así que un clúster con muchas aplicaciones se sigue leyendo bien.
- **Los pueblos** son las aplicaciones de un barrio. Cada uno es un grupo de casas encaladas con un guardapolvo del color de su barrio: más casas, más servicios; un **pozo** cuando corre una base de datos y un **cántaro** cuando corre una caché. Con **Desplegado de otra forma** activado también aparecen, en *Las afueras*, las aplicaciones de ArgoCD, Helm o `kubectl`, los complementos y las llamadas fuera del clúster. **Los internos del clúster** (ingress, cert-manager, monitoreo…) se ocultan, salvo que una aplicación los llame o usted los active.
- **El anillo** en el suelo alrededor de un pueblo o una casa es su salud: verde cuando pasaron sus últimas [revisiones de disponibilidad](reliability.md) y sus llamadas van bien, **rojo** cuando falló una revisión o falla el 5 % o más de sus llamadas, **ámbar** cuando va lento, gris cuando no se ha revisado ni medido nada.
- **Los caminos** son las conexiones: desde el arco de **Internet** hacia cada servicio con dirección pública, y entre aplicaciones. Una flecha en cada camino apunta del que llama al que responde.

Dentro de un pueblo, cada **casa** es un servicio, con su lenguaje en una insignia (`JS`, `Py`, `Jv`, `Go`); una casa alta es una tarea o proceso que solo llama a otros. Las llamadas de otras aplicaciones y de internet entran por el **arco**, y las llamadas a otras aplicaciones salen por **letreros** con su nombre.

Lo demás está ahí para darle vida y no significa nada: laureles con el tronco encalado, bancas de hierro frente al kiosco, una fuente, jardineras de cempasúchil, la **presidencia municipal** con sus portales y una torre con un reloj que da la hora real, y gente que da la vuelta a la plaza, la mitad en un sentido y la mitad en el otro, como en el *paseo* de antes. Entre los pueblos de un barrio crecen más laureles, y un barrio con lugar de sobra tiene su propia placita. Con el movimiento apagado, la gente y la fuente se quedan quietas.

## De dónde salen los caminos {#where-the-roads-come-from}

| Camino | Origen | Ejemplo |
|---|---|---|
| declarado | una entrada `needs:` en el `rendimiento.yaml` de quien llama | `needs: [postgres]`, `needs: [{service: api}]` |
| encontrado en su entorno | una variable de entorno de quien llama que apunta al servicio (la misma detección del [catálogo de servicios](needs.md)) | `API_URL=http://api.wellness.svc.cluster.local` |
| dirección pública | un Ingress dirigido al servicio, desde el arco de Internet | `https://wellness.example.com` |
| visto en trazas | una llamada medida por el [colector](#latency-on-every-line) que nadie declaró | una llamada a `api.openweathermap.org` |

Un camino que nadie ha medido es una **vereda punteada**; uno medido está empedrado y lleva tráfico (vea abajo). El panel dice cómo se sabe de cada camino. Los valores que se leen de secretos no se ven, así que una conexión cuya dirección solo está en un secreto aparece únicamente cuando se declara con `needs:` o se mide. Declarar las necesidades completa el mapa.

## Barrios {#barrios}

Cada aplicación queda en un barrio:

1. **El suyo**, cuando su `rendimiento.yaml` lo dice: `barrio: Pagos` (letras, dígitos, espacios y guiones, hasta 40; los acentos se permiten). Úselo para un equipo o un dominio.
2. Si no, **el barrio de las aplicaciones con las que más habla**: las aplicaciones que se llaman entre sí terminan juntas, con el nombre de la más grande (*Barrio de stockpulse*). El tráfico medido cuenta más que una conexión declarada, así que ganan los vecinos más ocupados.
3. Las aplicaciones que no llaman a ninguna otra van a **el centro**.
4. Lo que rendimiento no despliega (complementos, lo desplegado de otra forma, las llamadas fuera del clúster) está en **las afueras**.

## Cómo usarlo {#using-it}

- **Tres niveles.** La **región** muestra los barrios; toque uno para acercarse. En un **barrio**, toque un pueblo para ver en el panel lo que entra y lo que sale; tóquelo dos veces, o use **Entrar al pueblo**, para entrar. Dentro del **pueblo**, toque una casa, el pozo o un camino. **Esc** sube un nivel, y la ruta sobre el mapa (*Región › El centro › wellness*) regresa a cualquiera de ellos. El lugar queda en la dirección (`?barrio=centre&pueblo=wellness`), así que una vista se puede guardar o compartir.
- **Toque un camino** para ver lo que pasa por él: llamadas por minuto, fallas, p50 y p95 con una gráfica del periodo, cómo se sabe de él y botones para ir a cualquiera de sus extremos. Un camino entre barrios enlista las conexiones que lo forman. Los caminos responden a un toque aunque el dedo quede un poco al lado.
- **Acerque** con la rueda del ratón o pellizcando, y **arrastre** para moverse; los botones **+**, **−** y **⤢** acercan, alejan y vuelven al nivel completo.
- **2.5D o 3D.** El interruptor arriba a la izquierda del mapa dibuja el mismo pueblo plano y en ángulo (**2.5D**, la opción inicial) o en **3D**. En 3D, arrastre con el botón derecho del ratón o con dos dedos para **girarlo e inclinarlo**, o gírelo de esquina en esquina con **⟲** y **⟳**; todo lo demás funciona igual (los niveles, los toques, el panel, la dirección). Los caminos entre barrios se levantan en arcos sobre el terreno, así que no cruzan los barrios de en medio. La elección se guarda en este dispositivo. El 3D necesita WebGL; en un navegador que no lo tiene, el mapa se queda en 2.5D y lo avisa.
- **Ir a una aplicación** lleva directo a una aplicación por su nombre.
- **El panel** se lee como una pizarra de mercado: cada barrio, pueblo y servicio con sus llamadas por minuto, una pequeña tendencia del periodo y el cambio (▲▼) de su inicio a su final; **Pide atención** enlista lo que falla o va lento. **Abrir aplicación** lleva a la página de la aplicación; **Abrir en Servicios** lleva a su entrada en el catálogo.
- **Los momentos de un servicio.** Toque un servicio observado y el panel enlista también lo que hace, según sus trazas: **sus propios momentos** (lo que las [bibliotecas observa](observa.md) marcan con `tramo`, y el trabajo del propio agente), las **rutas que atiende** (`GET /api/moments`) y las **llamadas y consultas que hace** (`SELECT wellness`), cada uno con llamadas por minuto, fallas, p95 y tendencia. Toque uno para ver su latencia en el periodo.
- **Las solicitudes recientes de un servicio.** Debajo de sus momentos, el panel enlista las solicitudes que pasaron por ese servicio y que guardó el [servicio de trazas](#following-one-request), de la más nueva a la más vieja: si **falló**, **fue lenta** o es **de muestra**, cuánto tardó y cuándo. **Solo las que fallaron** acota la lista. Toque una para seguirla paso a paso.
- **Actualizar** reconstruye el catálogo desde el clúster en ese momento (si no, tiene a lo más 30 segundos, y la página se recarga cada minuto).

## La latencia en cada camino {#latency-on-every-line}

Con las trazas activadas, cada camino lleva también lo medido en el **Periodo** que se elige arriba (15 minutos, una hora, 24 horas): **llamadas por minuto**, la proporción que **falló** y cuánto tardaron (**p50**, la mitad fue más rápida; **p95**, el 5 % más lento), cada una con su tendencia a lo largo del periodo. Los caminos se vuelven **más anchos** con más llamadas, **rojos** cuando falla el 5 % o más y **ámbar** cuando el 5 % más lento tarda medio segundo o más. Por los caminos medidos van luciérnagas (de noche) o confeti (de día) del que llama al que responde: más mientras más llamadas, y más rápido mientras más rápidas sean. Si desactiva el movimiento en la barra superior, se quedan quietos.

Las llamadas **vistas solo en las trazas** (nadie las declaró) tienen sus propios caminos: una llamada a un servicio que ningún `needs:` menciona, a una base de datos o a una API **fuera del clúster**, agrupadas en *Fuera del clúster*, en las afueras.

### Cómo activarla {#turning-it-on}

1. Instale el complemento **OpenTelemetry Collector** (Complementos). Recibe las trazas y escribe los números, el grafo de servicios, en el almacén de métricas; con un exportador `otlphttp` hacia `rendimiento-traces`, también le envía cada tramo al [servicio de trazas](#following-one-request). Ponga como dirección de escritura su VictoriaMetrics o Prometheus. Antes se descartan las revisiones de salud (las sondas de Kubernetes, `kube-probe/…`, y las revisiones de disponibilidad de rendimiento, `rendimiento-uptime/…`), para que no dibujen una línea desde Internet hacia cada servicio.
2. En la plataforma, defina `TELEMETRY_ENDPOINT` con el colector y `METRICS_URL` con el almacén de métricas ([Ajustes](../reference/config.md#traces-and-the-universe)).
3. En el `rendimiento.yaml` de una aplicación, agregue `observe: true` (o `observe: true` en un solo servicio). El siguiente despliegue agrega las trazas.

Lo que recibe cada servicio:

| `language` | Lo que agrega rendimiento |
|---|---|
| `java` | el agente de OpenTelemetry para Java, cargado con `JAVA_TOOL_OPTIONS` |
| `node` | el agente de OpenTelemetry para Node, cargado con `NODE_OPTIONS` (después de las opciones propias de la aplicación) |
| `python` | el agente de OpenTelemetry para Python, en `PYTHONPATH` |
| `go`, otros, imágenes listas | solo las variables `OTEL_*`, para un SDK de OpenTelemetry en el código |

Cada servicio observado se llama `aplicación/servicio` (`OTEL_SERVICE_NAME`); así un camino medido encuentra sus casas. Un contenedor de inicio pequeño copia el agente al pod; agrega memoria (el de Java es el que más, unos 100 MB) y algo de tiempo de arranque, así que actívelo donde quiera ver. Se conserva una cuarta parte de las trazas (`TELEMETRY_SAMPLE_RATIO`), que basta para los promedios.

!!! note "Python en Alpine"
    El agente de Python está hecho para glibc. Un servicio con una imagen Alpine (musl) puede no cargarlo: use una imagen basada en Debian, o ponga `observe: false` en ese servicio.

Un servicio en Go se mide de todos modos desde el otro lado: las llamadas *hacia* él desde un servicio observado las cronometra quien llama.

## Seguir una solicitud {#following-one-request}

Un camino dice cómo le va a un servicio en promedio; la **cascada de una solicitud** dice en qué se le fue el tiempo a una sola. Ábrala desde **Solicitudes recientes** de un servicio: cada paso (tramo) de la solicitud en el orden de las llamadas, cada uno debajo del que lo hizo, con una barra de cuándo corrió dentro de la solicitud y cuánto tardó. Los colores siguen al servicio, así que se ven los saltos entre servicios; un paso que falló está en rojo. Toque un paso para ver qué hizo (atendió una solicitud, hizo una llamada, un momento propio), cuándo empezó, su mensaje de estado, sus atributos (la ruta, la consulta, el código de estado) y sus eventos (una excepción con su pila, una bandera que anotaron las [bibliotecas observa](observa.md)).

Las solicitudes las guarda el **servicio de trazas** (`rendimiento-traces`, [`ROLE=traces`](../architecture/overview.md#three-services)). El colector le envía cada tramo; espera 20 segundos a que lleguen los tramos de una solicitud y luego guarda:

- cada solicitud en la que **falló** un paso;
- cada solicitud que tardó **`TRACE_SLOW`** o más (1 segundo);
- **`TRACE_SAMPLE`** de las demás (una de cada diez), elegidas por el identificador de la solicitud, así que una solicitud se guarda o se descarta completa.

Van a Postgres, una partición por día, y un día se borra completo después de **`TRACE_RETENTION_DAYS`** (7). Al colector solo llega lo que muestrean los servicios (`TELEMETRY_SAMPLE_RATIO`, una cuarta parte), así que el servicio de trazas ve una cuarta parte de las solicitudes y guarda las interesantes entre ellas. Ponga la proporción en `1` para ver cada falla.

Para agregar a las trazas los momentos que solo el código conoce (un pago, un reporte) y banderas de funciones, use las [bibliotecas](observa.md) para Node, Python y Java.
