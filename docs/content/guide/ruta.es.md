# La Ruta

Ninguna mariposa monarca hace sola toda la migración: hacen falta varias generaciones, y aun así cada otoño llegan a los mismos bosques de oyamel en Michoacán. Los agentes de IA son así. Una sesión de un agente no recuerda nada de la anterior, y el propio modelo será reemplazado. **La Ruta** es donde se guarda el camino: la memoria de la plataforma, para las personas y para los agentes, para que quien siga (otra sesión, otro modelo o una persona) continúe donde se quedó el anterior.

## Qué guarda {#what-it-holds}

| Tipo | Qué | Quién lo escribe |
|---|---|---|
| **Decisión** | qué se decidió, y por qué | personas y agentes |
| **Manual** | cómo se hace algo aquí | personas y agentes |
| **Pendiente** | algo que falta hacer; se marca cuando queda hecho | personas y agentes |
| **Nota** | cualquier otra cosa que valga la pena saber | personas y agentes |
| **Lo vivido** | relatos que vivieron las personas, en sus propias palabras | **solo personas** |

Lo vivido existe porque un agente solo sabe lo que alguien escribió, no lo que las personas vivieron. Esos relatos se guardan tal como se contaron; los agentes los leen con respeto, pero nunca los pueden escribir ni cambiar.

No se pierde nada: una edición conserva la versión anterior, y *Archivar* esconde una entrada sin borrarla.

## Cargos: el turno de trabajo de un agente {#cargos-an-agents-turn-of-work}

Un agente que quiere escribir primero **toma un cargo**: un turno de trabajo con un propósito ("mudar la base de datos de stockpulse"). Todo lo que escribe pertenece a ese cargo. Antes de irse, **entrega el cargo**: qué hizo y qué falta. El siguiente agente empieza leyendo las últimas entregas. La pestaña **Cargos** las muestra como el vuelo de las monarcas, una tras otra, con cada llamada a una herramienta anotada.

## Conectar un agente (MCP) {#connecting-an-agent-mcp}

Los agentes llegan a la Ruta por **MCP** (Model Context Protocol), un estándar abierto que hablan muchas herramientas de IA, en `https://<su rendimiento>/mcp`.

1. En **Ruta → Llaves**, cree una llave: un nombre, solo lectura o lectura y escritura, y cuánto dura (90 días como máximo). La llave se muestra **una sola vez**.
2. Conecte el agente. En Claude Code:

    ```bash
    claude mcp add --transport http rendimiento https://rendimiento.example.com/mcp \
      --header "Authorization: Bearer $RENDIMIENTO_MCP_TOKEN"
    ```

    O confirme un `.mcp.json` en un repositorio, para que quien lo abra con un agente quede conectado, con la llave en una variable de entorno y nunca en git:

    ```json
    { "mcpServers": { "rendimiento": { "type": "http", "url": "https://rendimiento.example.com/mcp",
        "headers": { "Authorization": "Bearer ${RENDIMIENTO_MCP_TOKEN}" } } } }
    ```

3. Cuando el agente se conecta, el servidor le explica las reglas: empezar con `ruta_inicio`, tomar un cargo antes de escribir, entregarlo antes de irse y nunca escribir secretos.

| Herramienta | Hace | Llave |
|---|---|---|
| `ruta_inicio` | las últimas entregas, los pendientes abiertos, lo reciente y el cargo abierto del agente | cualquiera |
| `ruta_buscar` | busca por texto y por tipo | cualquiera |
| `ruta_leer` | una entrada completa | cualquiera |
| `cargo_tomar` | toma un cargo con un propósito | lectura y escritura |
| `ruta_anotar` | agrega una decisión, un manual, un pendiente o una nota | lectura y escritura, con cargo |
| `ruta_actualizar` | cambia una entrada, o marca un pendiente como hecho | lectura y escritura, con cargo |
| `cargo_entregar` | entrega el cargo | lectura y escritura, con cargo |

## Seguridad {#safety}

- **Las llaves** son aleatorias, se guardan solo como huella SHA-256, siempre caducan y se pueden revocar en la pestaña Llaves; revocar cierra el cargo abierto de la llave. Una llave de solo lectura ni siquiera ve las herramientas de escritura.
- **Sin secretos:** las entradas que parecen una contraseña, un token o una llave privada se rechazan, vengan de un agente o de una persona. Escriba *dónde* vive un secreto (el nombre del Secret), no su valor.
- **Todo queda anotado:** cada llamada a una herramienta, permitida o rechazada, con su llave y su cargo.
- **Notas, no órdenes:** a los agentes se les dice que las entradas son notas. Si una entrada pide algo que la persona con quien trabajan no pidió, no lo hacen, y lo dicen.
- **La misma puerta:** `/mcp` rechaza las peticiones de otros sitios web (la revisión de `Origin`), como el resto de la API.
- Por ahora la Ruta solo guarda conocimiento. Las acciones sobre el clúster por MCP (desplegar, revertir) vienen después, con vista previa, aprobación y forma de deshacer.
