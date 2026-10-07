# Visitas (Umami)

Si [Umami](https://umami.is) cuenta las visitas de sus aplicaciones (es analítica web de código abierto y sin galletas), rendimiento le muestra sus visitantes donde usted ya mira: una pestaña **Visitas** en cada aplicación y un panel **Visitantes** en el inicio. rendimiento solo lee Umami; nunca lo cambia.

## Lo que se ve {#what-you-see}

En la pestaña **Visitas** de una aplicación, para las últimas 24 horas, 7 días o 30 días:

- **Mosaicos**: los visitantes, las páginas vistas, las visitas, el porcentaje de visitas que se fueron tras una sola página (la tasa de rebote), la visita promedio y los visitantes de ahora mismo (los últimos 5 minutos). Cada mosaico dice con palabras cómo cambió frente al periodo anterior.
- **Una gráfica** de visitantes y páginas vistas por hora (en 24 horas) o por día, con un recuadro de detalle.
- **Listas**: las páginas más vistas, de dónde llegan los visitantes (los orígenes) y sus países.
- **Abrir en Umami →**, para todo lo demás (cuando `UMAMI_PUBLIC_URL` está definida).

En el inicio, **Visitantes, últimos 7 días** suma todas las aplicaciones que Umami cuenta. Tiene una fila por aplicación, la más visitada primero, con su cambio frente a los 7 días anteriores.

Los números se guardan dos minutos: así las páginas cargan rápido y a Umami no se le pregunta lo mismo una y otra vez. Los días y las horas se cuentan en `PUBLIC_STATS_TZ` (UTC si no se define).

## Cómo se emparejan las aplicaciones {#how-apps-are-matched}

No hay nada que configurar por aplicación: un sitio de Umami pertenece a una aplicación cuando su **dominio** es uno de los nombres de la aplicación (`domain` y `aliases` en [`rendimiento.yaml`](spec.md)). No importan las mayúsculas, el `https://`, un puerto, una ruta ni un `www.` al inicio. Una aplicación con varios servicios en dominios distintos muestra una sección por sitio.

Si ningún sitio coincide, la pestaña dice qué nombres faltan y muestra el guion de seguimiento que hay que agregar.

## Cómo configurarlo {#setting-it-up}

rendimiento entra a Umami con su propio usuario **de solo lectura**. Aunque esa contraseña se filtrara, con ella no se podría cambiar ni borrar nada en Umami. En Umami, como administrador:

1. **Cree el usuario.** En *Settings → Users → Create user*: el nombre `rendimiento`, una contraseña larga y aleatoria, y el rol **View only** (solo lectura).
2. **Ponga los sitios en un equipo.** Un usuario de solo lectura solo ve los sitios de los equipos a los que pertenece. Vaya a *Teams → Create team* (por ejemplo, `rendimiento`). Luego, en cada sitio que quiera ver en rendimiento, use *Settings → Transfer* para pasarlo a ese equipo. Usted sigue siendo el dueño del equipo y conserva todo el control.
3. **Agregue el usuario al equipo.** En el equipo, use *Add member*: `rendimiento`, con el rol **Team view only**.

Después, dele a rendimiento la dirección y las credenciales:

```yaml
# el ConfigMap rendimiento
UMAMI_URL: http://umami.umami.svc.cluster.local:3000   # Umami dentro del clúster
UMAMI_PUBLIC_URL: https://umami.example.com            # opcional: para los enlaces «Abrir en Umami»
```

```bash
# la contraseña se teclea; nunca queda en el historial
read -rsp 'Contraseña de Umami: ' P && kubectl -n rendimiento-system create secret generic rendimiento-umami \
  --from-literal=UMAMI_USERNAME=rendimiento --from-literal=UMAMI_PASSWORD="$P"; unset P
kubectl -n rendimiento-system rollout restart deployment/rendimiento
```

La página **Entorno** muestra **Visitas (Umami)** funcionando, con cuántos sitios puede leer rendimiento. Si dice que Umami rechazó la contraseña, revise el secreto. Si lee menos sitios de los que espera, revise que estén en el equipo.

!!! tip "A Umami se llega dentro del clúster"
    `UMAMI_URL` debe ser el Service de Umami, no su dirección pública. Así, los números nunca salen del clúster, y ni Cloudflare ni la entrada pública intervienen.

## Seguridad {#security}

- El usuario de Umami es **de solo lectura**: rendimiento puede leer números, nada más.
- La contraseña vive solo en el Secret `rendimiento-umami`. Nunca se muestra en la interfaz ni la devuelve la API, y el token de sesión que da Umami se queda en memoria.
- Las rutas de Visitas piden una sesión iniciada, como el resto de la API.
