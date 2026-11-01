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
- **Desde su propio agente.** Su propio Claude Code o Codex también deja encargos, una vez conectado con una llave de colaborador (Ruta → Llaves muestra el comando de cada uno). Con el atajo **/encargo** de esa misma pestaña, `/encargo revisa por qué la tienda está lenta` lo deja bien llenado, y dice quién lo pidió y desde qué agente (*p0dxD · Claude in main*); otro agente lo toma de la Ruta.
- **Estimado, y para quién.** El estimador (un [agente dentro de la plataforma](#agents-inside-the-platform)) califica cada encargo nuevo del 1 al 10 y le pone precio: lo que gastaría la IA de la plataforma en hacerlo (con Sonnet, aprox. $0.44 por un 2, $2.75 por un 5). Hasta el monto de la organización (en la página IA, $0.50 si no se cambia) es **para la IA de la plataforma**; arriba, **para su propio agente**, que usted paga al mes y no por llamada. El encargo lo muestra, con la razón, y **Dejarlo a mi agente** o **Hacerlo con IA de todos modos** lo cambia; los agentes lo leen en `encargo_leer` (`dificultad`, `estimado`, `para`). Cualquier agente puede seguir tomando cualquier encargo; el [agente de código](#the-coding-agent) toma los que le mandan. **Estimar otra vez** pide una calificación nueva (si cambió el detalle o falló la estimación) y conserva la ruta si usted la eligió; los encargos de antes del estimador también reciben la suya.
- **Lo que costaría todo.** Arriba de la lista, el costo de los encargos con la IA de la plataforma: hacer todos los abiertos, hacer solo los fáciles (debajo de su monto), lo que valían los ya hechos y lo que de verdad gastó el agente de código. Una barra muestra los abiertos por dificultad, de verde (barato) a rojo (caro), del mismo color que la dificultad de cada encargo.
- **Tomar.** Un agente conectado por MCP ve los pendientes en `ruta_inicio` y toma uno dentro de su cargo. Solo un agente lo puede tener.
- **Esperar.** Después de lanzar una ejecución, el agente llama a `encargo_esperar`: la llamada regresa cuando termina la ejecución, despertada por los propios eventos de la plataforma (hasta 50 segundos por llamada, menos del minuto que los clientes de MCP esperan a una herramienta; vuelve a llamar para seguir esperando). Mientras, el encargo muestra *espera la ejecución #N*. Ningún agente pregunta cada 30 segundos.
- **Entregar.** `encargo_entregar` con lo que hizo, cómo lo comprobó y los enlaces (rama, ejecución, solicitud de cambios).
- **Revisar.** Usted lo marca como **Revisado**, o lo **Devuelve** con lo que falta: queda pendiente otra vez, con su comentario, para el agente que lo tome después. Un agente que no puede terminarlo lo suelta (`encargo_soltar`) con el motivo.

Todo lo que le pasa a un encargo queda en su **historia**. Por ahora un encargo espera a que empiece la sesión de un agente; los agentes dentro de la plataforma (el paso 5 de abajo) los tomarán solos.

`https://<su rendimiento>/llms.txt` describe todo esto para un agente que encuentre la plataforma por su cuenta.

## Agentes dentro de la plataforma {#agents-inside-the-platform}

Algunos agentes no esperan la sesión de una persona: corren dentro de la plataforma (el servicio `rendimiento-agents`, `ROLE=agents`) y despiertan con lo que pasa en ella. Cada uno le pregunta al modelo de la organización por la [pasarela de IA](ai.md), así que se queda dentro de los topes de la organización y cae al modelo local.

- **El diagnosticador.** Cuando una ejecución falla, lee los pasos que fallaron, el final de sus registros y los archivos que cambiaron desde la última ejecución buena de la rama (o de la rama principal, si la rama es nueva), y escribe **¿Por qué falló?** en la página de la ejecución: la causa más probable, citando la línea del registro, y cómo arreglarlo. Las líneas que parecen secretos nunca salen de la plataforma. Una organización lo apaga en su página IA; un colaborador pide una explicación (de nuevo) en cualquier ejecución que falló. **Pedírselo a un agente** lleva después la explicación al encargo.
- **En el idioma de quien lee.** La explicación se escribe una vez, en `NOTIFY_LANG`. Quien lee la plataforma en el otro idioma la recibe traducida por la IA de su organización la primera vez, marcada *Traducido por la IA*, y guardada, así que el siguiente la ve al instante; diagnosticarla de nuevo descarta las traducciones viejas.
- **Aprende de usted.** Debajo de cada explicación, **Útil** o **No sirvió…** con la causa real y el arreglo. Cualquiera de las dos respuestas se vuelve una **falla conocida** en la Ruta (un manual con las etiquetas `falla-conocida` y la aplicación, con la línea del registro que la muestra). Antes de contestar, el diagnosticador busca fallas conocidas que compartan palabras con las líneas de error de la falla nueva y se las da primero al modelo, así que un modelo local pequeño contesta como alguien que ya la había visto.
- **El estimador.** Califica cada encargo nuevo (qué tan difícil es, cuánto gastaría la IA de la plataforma) con el modelo de la organización, para que el encargo vaya a la IA de la plataforma o a su propio agente; vea [Encargos](#encargos).
- **Las fallas nuevas pueden ir a un modelo más fuerte, con medida.** Está apagado por omisión. En la página IA, una organización puede mandar las fallas de las que la Ruta no sabe nada parecido a su propia llave o a la de la plataforma (dentro de su asignación), hasta cierto número de veces al mes. La respuesta se guarda como falla conocida **sin confirmar** (con la etiqueta `sin-confirmar`), así que la próxima vez el modelo local la sabe; calificar la explicación confirma o corrige esa entrada. Las fallas conocidas siempre se quedan con el modelo de siempre, así que solo se paga al más fuerte por lo nuevo.

### El agente de código {#the-coding-agent}

La IA de la plataforma puede hacer ella misma los encargos, como un agente propio (la llave *agente de código* en Ruta → Llaves, que crea la plataforma).

- **Apagado hasta que usted lo encienda.** En la página IA (5. El agente de código): apagado; *solo cuando yo se lo mande*; o *también los fáciles, por su cuenta* (los que quedan debajo del monto por encargo). Necesita un modelo de Anthropic: su propia llave o la de la plataforma.
- **Mandarle trabajo.** En Ruta → Encargos: **Hacer los fáciles**, **Hacer los difíciles**, o marque encargos y **Hacer los elegidos**; cada botón dice cuántos son y cuánto costarían. Esperan *en la fila del agente de código*, de uno en uno.
- **Cómo trabaja.** Toma el encargo dentro de un cargo y trabaja en una rama `encargo-<n>` del repositorio de la aplicación, a través de GitHub: lee y busca en los archivos, sigue el `AGENTS.md` o `CLAUDE.md` del repositorio, cambia lo necesario y hace commit. Nunca ejecuta código él mismo: cada commit lo prueba la CI de la plataforma, y lee por qué falló una ejecución (los registros de los pasos que fallaron y la explicación del diagnosticador) hasta que una pase. Entonces entrega el encargo con el enlace para abrir la solicitud de cambios y la ejecución, para que usted lo revise y lo junte. Nunca toca la rama principal.
- **Lo que gasta.** Cada llamada pasa por la [puerta de IA](ai.md), dentro del tope mensual de la organización, y se suma al encargo (*El agente de código gastó…*). Al doble de lo estimado se detiene: hace commit de lo que tiene en su rama y le devuelve el encargo a su propio agente, con lo que hizo y lo que falta, para que su Claude Code o Codex siga desde ahí. Hace lo mismo si no puede terminar, y si la plataforma se reinicia a medio trabajo.

Solo trabaja una copia de los agentes a la vez (un candado en Postgres), así que nada se hace dos veces durante una actualización.

## Hacia dónde va {#where-it-is-going}

En este orden, cada paso sobre el anterior:

1. **Funciones, las herramientas de la plataforma y el registro** (lo de arriba).
2. **Encargos** (arriba): hechos para las ejecuciones. Esperar a que se integre una solicitud de cambios o a la aprobación de una persona llega con el paso 3.
3. **Aprobaciones:** un agente pide una acción (integrar, desplegar, revertir) con su motivo; la persona la aprueba o la rechaza en la interfaz o por correo.
4. **Entornos de prueba:** cada rama desplegada por su cuenta un tiempo, con su URL y sus trazas, para que un agente pruebe contra lo real antes de pedir que integren.
5. **Agentes dentro de la plataforma** (arriba): el diagnosticador, el estimador y el agente de código, que hace encargos en una rama con la CI de la plataforma como entorno aislado, dentro de un tope de gasto. Después, medir qué tanto aciertan las estimaciones, para ajustarlas.
6. **IA en el producto:** *¿por qué falló?* en cada ejecución fallida, búsqueda por significado en la Ruta, y cada sesión de un agente como una traza.
