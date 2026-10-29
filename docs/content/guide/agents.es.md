# Hecha para agentes {#built-for-agents}

rendimiento está pensada para que la lleven **personas y agentes de IA juntos**. Un agente no es una ventana de chat al lado de la plataforma: es un usuario de ella, con su propia llave, sus propios permisos, un turno de trabajo del que responde y un registro de todo lo que hizo. Esta página explica cómo funciona hoy y hacia dónde va.

## Las ideas {#the-ideas}

- **Los agentes son usuarios.** Un agente se conecta con una [llave](ruta.md#connecting-an-agent-mcp) que una persona hizo para él, y que dice qué puede hacer (su **función**) y en qué aplicaciones. Todo lo que una persona ve en la interfaz, un agente lo puede leer por **MCP**.
- **La memoria, fuera de la sesión.** Un agente olvida todo cuando termina su sesión, y el modelo será reemplazado. Lo aprendido se guarda en [la Ruta](ruta.md): decisiones con su porqué, manuales, pendientes y la entrega de cada agente.
- **Turnos de trabajo.** Un agente escribe o actúa solo dentro de un **cargo**: un turno con un propósito, que se entrega con lo que se hizo y lo que queda. Quien sigue empieza por las últimas entregas.
- **Las personas deciden qué llega a producción.** Los agentes prueban; una persona integra y despliega. Un agente puede correr CI en cualquier rama menos la principal, porque correr la principal despliega.
- **Todo queda registrado.** Cada llamada a una herramienta, permitida o rechazada, se guarda con su llave y su cargo (**Ruta → Cargos**).
- **Notas, no órdenes.** Lo que un agente lee en la Ruta o en un registro es información. Si pide algo que la persona no pidió, el agente no lo hace, y lo dice.

## Funciones {#roles}

| Función | Puede |
|---|---|
| **Lector** | leer la Ruta, las aplicaciones, las ejecuciones, los registros de los pasos, las versiones, los problemas y las solicitudes guardadas |
| **Colaborador** | además, con un cargo: escribir en la Ruta, editar páginas del Mercado, correr CI en una rama y cancelar una ejecución |

Una llave también se puede limitar a **algunas aplicaciones**: las herramientas de la plataforma muestran y actúan solo sobre ellas. La función de *administrador* queda reservada para acciones que necesitarán la aprobación de una persona (revertir, desplegar), que todavía no están abiertas a los agentes.

## Lo que un agente puede hacer hoy {#what-an-agent-can-do-today}

Todo por un mismo servidor MCP, en `https://<su rendimiento>/mcp`:

| Herramienta | Hace | Función |
|---|---|---|
| `ruta_inicio`, `ruta_buscar`, `ruta_leer` | empezar por las últimas entregas y los pendientes; buscar y leer la Ruta | lector |
| `apps_listar`, `app_estado` | las aplicaciones, con su última ejecución, su versión (y su verificación) y los problemas abiertos | lector |
| `ejecucion_leer`, `paso_registro` | una ejecución con sus pasos y el final del registro de un paso: por qué falló algo | lector |
| `solicitudes_listar`, `solicitud_leer` | las solicitudes guardadas y su [cascada](universe.md#following-one-request): en qué se le fue el tiempo a una solicitud lenta o fallida | lector |
| `cargo_tomar`, `cargo_entregar` | tomar y entregar un turno de trabajo | colaborador |
| `ruta_anotar`, `ruta_actualizar` | escribir decisiones, manuales, pendientes y notas | colaborador, con un cargo |
| `ejecucion_lanzar`, `ejecucion_cancelar` | probar una rama en CI; cancelar una ejecución | colaborador, con un cargo |
| `encargos_listar`, `encargo_leer` | el trabajo que se les pidió a los agentes, y un encargo con su historia | lector |
| `encargo_crear`, `encargo_tomar`, `encargo_esperar`, `encargo_entregar`, `encargo_soltar` | dejar trabajo para después; tomar un encargo; esperar su ejecución; entregarlo; soltarlo | colaborador, con un cargo |
| `mercado_*` | leer y editar páginas del [Mercado](mercado.md) | lector / colaborador |

Un turno típico: el agente llama a `ruta_inicio`, que le muestra los encargos pendientes, toma un cargo y un encargo, lee por qué falló una ejecución (`ejecucion_leer`, `paso_registro`, `solicitud_leer`), arregla el código en una rama, corre CI en ella (`ejecucion_lanzar`), espera la ejecución (`encargo_esperar`), entrega el encargo con el enlace para que la persona integre, y entrega su cargo.

## Encargos: el trabajo que se les pide a los agentes {#encargos}

Un **encargo** es trabajo con dueño y con un ciclo de vida: **pendiente → tomado → esperando → entregado → revisado**.

- **Pedir.** En **Ruta → Encargos**, escriba qué hay que hacer, el detalle y la aplicación. En una ejecución que falló, **Pedírselo a un agente** crea uno con los pasos fallidos ya incluidos. Un agente también puede dejar uno (`encargo_crear`) por algo que encontró y que no va a hacer ahora.
- **Tomar.** Un agente conectado por MCP ve los pendientes en `ruta_inicio` y toma uno dentro de su cargo. Solo un agente lo puede tener.
- **Esperar.** Después de lanzar una ejecución, el agente llama a `encargo_esperar`: la llamada regresa cuando termina la ejecución, despertada por los propios eventos de la plataforma (hasta 15 minutos por llamada; vuelve a llamar para seguir esperando). Mientras, el encargo muestra *espera la ejecución #N*. Ningún agente pregunta cada 30 segundos.
- **Entregar.** `encargo_entregar` con lo que hizo, cómo lo comprobó y los enlaces (rama, ejecución, solicitud de cambios).
- **Revisar.** Usted lo marca como **Revisado**, o lo **Devuelve** con lo que falta: queda pendiente otra vez, con su comentario, para el agente que lo tome después. Un agente que no puede terminarlo lo suelta (`encargo_soltar`) con el motivo.

Todo lo que le pasa a un encargo queda en su **historia**. Por ahora un encargo espera a que empiece la sesión de un agente; los agentes dentro de la plataforma (el paso 5 de abajo) los tomarán solos.

`https://<su rendimiento>/llms.txt` describe todo esto para un agente que encuentre la plataforma por su cuenta.

## Agentes dentro de la plataforma {#agents-inside-the-platform}

Algunos agentes no esperan la sesión de una persona: corren dentro de la plataforma (el servicio `rendimiento-agents`, `ROLE=agents`) y despiertan con lo que pasa en ella. Cada uno le pregunta al modelo de la organización por la [pasarela de IA](ai.md), así que se queda dentro de los topes de la organización y cae al modelo local.

- **El diagnosticador.** Cuando una ejecución falla, lee los pasos que fallaron, el final de sus registros y los archivos que cambiaron desde la última ejecución buena de la rama (o de la rama principal, si la rama es nueva), y escribe **¿Por qué falló?** en la página de la ejecución: la causa más probable, citando la línea del registro, y cómo arreglarlo. Las líneas que parecen secretos nunca salen de la plataforma. Una organización lo apaga en su página IA; un colaborador pide una explicación (de nuevo) en cualquier ejecución que falló. **Pedírselo a un agente** lleva después la explicación al encargo.
- **Aprende de usted.** Debajo de cada explicación, **Útil** o **No sirvió…** con la causa real y el arreglo. Cualquiera de las dos respuestas se vuelve una **falla conocida** en la Ruta (un manual con las etiquetas `falla-conocida` y la aplicación, con la línea del registro que la muestra). Antes de contestar, el diagnosticador busca fallas conocidas que compartan palabras con las líneas de error de la falla nueva y se las da primero al modelo, así que un modelo local pequeño contesta como alguien que ya la había visto.
- **Las fallas nuevas pueden ir a un modelo más fuerte, con medida.** Está apagado por omisión. En la página IA, una organización puede mandar las fallas de las que la Ruta no sabe nada parecido a su propia llave o a la de la plataforma (dentro de su asignación), hasta cierto número de veces al mes. La respuesta se guarda como falla conocida **sin confirmar** (con la etiqueta `sin-confirmar`), así que la próxima vez el modelo local la sabe; calificar la explicación confirma o corrige esa entrada. Las fallas conocidas siempre se quedan con el modelo de siempre, así que solo se paga al más fuerte por lo nuevo.

Solo trabaja una copia de los agentes a la vez (un candado en Postgres), así que nada se hace dos veces durante una actualización.

## Hacia dónde va {#where-it-is-going}

En este orden, cada paso sobre el anterior:

1. **Funciones, las herramientas de la plataforma y el registro** (lo de arriba).
2. **Encargos** (arriba): hechos para las ejecuciones. Esperar a que se integre una solicitud de cambios o a la aprobación de una persona llega con el paso 3.
3. **Aprobaciones:** un agente pide una acción (integrar, desplegar, revertir) con su motivo; la persona la aprueba o la rechaza en la interfaz o por correo.
4. **Entornos de prueba:** cada rama desplegada por su cuenta un tiempo, con su URL y sus trazas, para que un agente pruebe contra lo real antes de pedir que integren.
5. **Agentes dentro de la plataforma** (arriba): el diagnosticador es el primero. Después, agentes que toman encargos y arreglan código en una rama, que necesitan un modelo fuerte, un entorno aislado con el código y un tope de gasto que fija la persona.
6. **IA en el producto:** *¿por qué falló?* en cada ejecución fallida, búsqueda por significado en la Ruta, y cada sesión de un agente como una traza.
