# AI: the gateway, providers and caps

Every call the platform makes to a model goes through one **gateway** (`internal/ia`): the [agents inside the platform](agents.md#agents-inside-the-platform), such as the diagnostician that explains failed runs. It decides which model answers for each organization, keeps everyone under their monthly caps, and records every call.

## Who answers {#who-answers}

Each organization chooses, on its **AI** page (administrators):

| Provider | Who pays | Notes |
|---|---|---|
| **The local model** | nobody: it runs on the platform's own hardware (`AI_LOCAL_URL`, an Ollama) | the default, and where every call falls back |
| **Your own key** | the organization, to its provider | Anthropic, or any OpenAI-compatible service (its https address and a model). The key is kept in a Kubernetes Secret, never in the database, and the page shows only its last four characters |
| **The platform's** | the platform's operators | only up to the **allowance** the operators give that organization each month, and never past the platform's total (`AI_PLATFORM_CAP`) |

## Caps {#caps}

- The organization sets its **monthly cap** in dollars. With its own key, the gateway stops calling the provider once the month's spending reaches it.
- With the platform's key, the limit is the smaller of the organization's cap and the operators' **allowance**, and all organizations together stay under `AI_PLATFORM_CAP` (5 dollars by default).
- When a cap is reached, a key is missing, or the provider fails, **the local model answers instead**, and the call says why. Nothing stops working; it just gets simpler.
- A single answer is at most 4,096 tokens, so a call can pass a cap only by one answer's worth.

Spending is measured from the tokens each provider reports and the model's list price (`ia.DefaultPrices`; a model not listed counts as free, so give it a price before relying on its cap). Caps reset on the first of each month (UTC).

## Escalating new failures {#escalating-new-failures}

The cheapest setup is the local model plus the Ruta's known failures, which the diagnostician reads first. For failures the Ruta knows nothing like, an organization may escalate to a stronger model (its own key, or the platform's within its allowance), at most a number of times a month (10 by default); it is off unless turned on. What the stronger model answers becomes an unconfirmed known failure, so the next time the local model knows it. See [Agents inside the platform](agents.md#agents-inside-the-platform).

## The AI page {#the-ai-page}

- **Who answers**, the model, the key and the monthly cap (administrators change them).
- **This month:** what was spent with the organization's key, and of the platform's.
- **Try it:** ask a question and see who answered, the tokens, the cost and, if it fell back, why (collaborators).
- **Latest calls:** every call, who asked (a feature or an agent), the model, tokens, cost, and any error.
- **Operators** also see every organization's allowance of the platform's AI, what each spent of it, and the platform's bill; they change allowances there.

## Setting it up {#setting-it-up}

1. A local model: an Ollama reachable from the platform (for example the `ai` app's `ollama` service), in `AI_LOCAL_URL`, with `AI_LOCAL_MODEL`.
2. Optionally, the platform's key: an Anthropic key in the `rendimiento-ia` Secret as `AI_PLATFORM_KEY`. Set the same monthly limit in Anthropic's console as a second safety net.
3. Give allowances to the organizations that may use it, on the AI page.

See [Settings](../reference/config.md#ai).
