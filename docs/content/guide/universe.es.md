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

- **Pase el cursor** sobre un nodo para iluminar sus conexiones; lo demás se atenúa.
- **Haga clic** en un nodo para fijarlo. El panel lateral muestra qué es, su salud y sus pods, sus direcciones públicas, **Llama a** (lo que usa) y **La llaman** (quién lo usa), cada una con la variable o dirección y de dónde sale la línea. Haga clic en un nombre de esas listas para ir a ese nodo.
- **Abrir aplicación** lleva a la página de la aplicación; **Abrir en Servicios** lleva a su entrada en el catálogo.
- **Actualizar** reconstruye el catálogo desde el clúster en ese momento (si no, tiene a lo más 30 segundos, y la página se recarga cada minuto).

## Lo que sigue {#what-comes-next}

La latencia, las solicitudes por minuto y los errores en cada línea, medidos con trazas (OpenTelemetry), para las aplicaciones que activen las trazas. Los servicios en Java, Node y Python se instrumentarán sin cambiar su código; las bibliotecas de cada lenguaje agregarán eventos propios.
