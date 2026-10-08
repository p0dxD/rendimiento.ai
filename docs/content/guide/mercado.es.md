# El Mercado (páginas sin código)

El **Mercado** hace páginas web para quien no escribe código. Usted llena un formulario y rendimiento hace la página, la publica en su propia dirección y la cuida. Cada página es un **puesto**, y la sección Mercado de la barra superior los muestra todos bajo sus toldos.

## Poner un puesto {#opening-a-stall}

**Mercado → Poner un puesto** pregunta, en este orden:

- **Qué tipo de página**: *Personal* (sobre usted y dónde encontrarle), *Negocio* (lo que ofrece, su horario y cómo contactarle), *Evento* (cuándo y dónde, y su historia) o *Portafolio* (su trabajo en fotos). El tipo nombra las secciones de la página y sugiere datos: horario y dirección para un negocio; fecha, hora y lugar para un evento.
- **Su nombre**, una frase debajo y su **dirección**: una sola palabra bajo una de las zonas DNS (`ana.example.com`). Esa palabra también nombra la aplicación y su repositorio.
- **Colores**: una de cinco paletas de Cotija (cempasúchil, grana, añil, nopal y rosa mexicano).
- **Una foto principal**, que se ve redonda hasta arriba, y hasta ocho más.
- **Su historia** (párrafos separados por una línea en blanco), sus **datos** (etiqueta y texto) y **dónde encontrarle**: usuarios de Instagram, Facebook o TikTok, un número de WhatsApp o de teléfono, un correo o un sitio web.
- **El idioma de la página**: español o inglés, para las palabras propias de la página (sus títulos y su pie). Lo que usted escriba queda tal como lo escribió.

La vista previa junto al formulario es la página misma: la plataforma la genera mientras usted escribe. Después, **Abrir mi puesto**:

1. crea un repositorio público en la organización de GitHub del Mercado (`PAGES_ORG`), con el nombre de la dirección;
2. lo registra como aplicación y confirma en él la página: `index.html`, `pagina.json` (las respuestas del formulario), las fotos en `fotos/`, un `Dockerfile` (nginx) y su `rendimiento.yaml`;
3. invita al repositorio a quien lo abrió (*maintain*), para que también pueda cambiarlo en GitHub.

Esa confirmación es un envío como cualquier otro: rendimiento construye la página y la publica, con su registro DNS y su certificado. Una página es un servicio pequeño (10m de CPU y 32 MiB de memoria).

## Cambiar un puesto {#changing-a-stall}

**Cambiarlo**, en un puesto, abre el mismo formulario con la página como está. **Guardar y publicar** hace una sola confirmación (el `index.html` y el `pagina.json` nuevos, las fotos nuevas y sin las que ya no se usan), y el envío la publica. El historial de versiones, las reversiones, la disponibilidad y las visitas funcionan como en cualquier aplicación: **Detalles técnicos** abre la página de la aplicación.

El formulario lee la página de `pagina.json` en la rama principal, así que un cambio hecho en GitHub aparece en el formulario. `index.html` se reescribe cada vez que se guarda: cambie la página con `pagina.json` o con el formulario, no editando el HTML.

## Qué es seguro {#what-is-safe}

Nada de lo que se escribe en el formulario se vuelve HTML:

- los nombres, los textos y los datos se escapan;
- los enlaces se arman a partir de un usuario, un número o una dirección, y solo salen enlaces `https`, `mailto:` y `tel:`;
- las fotos deben ser JPEG, PNG, WebP o GIF por su contenido, no por su nombre. El formulario las achica a 1600 píxeles como máximo antes de enviarlas.

Se rechaza una dirección que ya use una aplicación o cualquier ingress del clúster, y también cualquiera fuera de las zonas DNS. La vista previa se muestra en un marco aislado, sin scripts.

## Cómo configurarlo {#setting-it-up}

El Mercado está apagado hasta que se define `PAGES_ORG`. Una sola vez:

1. **Cree una organización de GitHub** para las páginas (es gratis), por ejemplo `example-paginas`.
2. **Dele a la aplicación de GitHub el permiso de administración** (lectura y escritura): en GitHub, en la configuración de la aplicación → *Permissions & events* → *Repository permissions* → *Administration*. Con él puede crear repositorios e invitar personas. GitHub le pide a cada instalación que acepte el permiso nuevo.
3. **Instale la aplicación en la organización** para **todos los repositorios**, para que llegue a los repositorios que crea.
4. Ponga el nombre de la organización en `PAGES_ORG` y reinicie rendimiento.

Quien pone un puesto inicia sesión como todos: con GitHub, y debe estar en la lista permitida (`ALLOWED_USERS`).

Borrar la aplicación de un puesto (Configuración → Borrar) quita la página de internet y del Mercado. Su repositorio se queda en la organización, para conservarlo o borrarlo en GitHub.
