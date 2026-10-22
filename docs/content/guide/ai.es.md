# IA: la pasarela, los proveedores y los topes {#ai-the-gateway-providers-and-caps}

Toda llamada que la plataforma le hace a un modelo pasa por una sola **pasarela** (`internal/ia`): la de los [agentes dentro de la plataforma](agents.md#agents-inside-the-platform), como el diagnosticador que explica las ejecuciones que fallan. Decide qué modelo contesta para cada organización, mantiene a todos bajo sus topes mensuales y registra cada llamada.

## Quién contesta {#who-answers}

Cada organización elige en su página **IA** (los administradores):

| Proveedor | Quién paga | Notas |
|---|---|---|
| **El modelo local** | nadie: corre en el equipo de la propia plataforma (`AI_LOCAL_URL`, un Ollama) | el predeterminado, y al que cae toda llamada |
| **Su propia llave** | la organización, a su proveedor | Anthropic, o cualquier servicio compatible con OpenAI (su dirección https y un modelo). La llave se guarda en un Secret de Kubernetes, nunca en la base de datos, y la página solo muestra sus últimos cuatro caracteres |
| **La de la plataforma** | quienes operan la plataforma | solo hasta la **asignación** que los operadores le dan a esa organización cada mes, y nunca más allá del total de la plataforma (`AI_PLATFORM_CAP`) |

## Topes {#caps}

- La organización fija su **tope mensual** en dólares. Con su propia llave, la pasarela deja de llamar al proveedor en cuanto el gasto del mes lo alcanza.
- Con la llave de la plataforma, el límite es el menor entre el tope de la organización y la **asignación** de los operadores, y todas las organizaciones juntas se quedan bajo `AI_PLATFORM_CAP` (5 dólares si no se cambia).
- Cuando se alcanza un tope, falta una llave o el proveedor falla, **contesta el modelo local** y la llamada dice por qué. Nada deja de funcionar; solo se vuelve más sencillo.
- Una respuesta tiene a lo más 4,096 tokens, así que una llamada solo puede pasarse de un tope por una respuesta.

El gasto se mide con los tokens que reporta cada proveedor y el precio de lista del modelo (`ia.DefaultPrices`; un modelo que no está en la lista cuenta como gratis, así que póngale precio antes de confiar en su tope). Los topes vuelven a cero el primero de cada mes (UTC).

## La página IA {#the-ai-page}

- **Quién contesta**, el modelo, la llave y el tope mensual (los cambian los administradores).
- **Este mes:** lo gastado con la llave de la organización y de la de la plataforma.
- **Pruébela:** haga una pregunta y vea quién contestó, los tokens, el costo y, si cayó al modelo local, por qué (los colaboradores).
- **Últimas llamadas:** cada llamada, quién preguntó (una función o un agente), el modelo, los tokens, el costo y cualquier error.
- **Los operadores** ven además la asignación de cada organización de la IA de la plataforma, lo que gastó cada una y la cuenta de la plataforma; ahí cambian las asignaciones.

## Cómo configurarla {#setting-it-up}

1. Un modelo local: un Ollama al que llegue la plataforma (por ejemplo el servicio `ollama` de la aplicación `ai`), en `AI_LOCAL_URL`, con `AI_LOCAL_MODEL`.
2. Si quiere, la llave de la plataforma: una llave de Anthropic en el Secret `rendimiento-ia`, como `AI_PLATFORM_KEY`. Ponga el mismo límite mensual en la consola de Anthropic, como segundo seguro.
3. Dé asignaciones a las organizaciones que puedan usarla, en la página IA.

Vea [Ajustes](../reference/config.md#ai).
