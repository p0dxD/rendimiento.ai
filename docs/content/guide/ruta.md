# La Ruta

No monarch butterfly makes the whole migration: it takes several generations, and still they reach the same fir forests in Michoacán every autumn. AI agents are like that. An agent session remembers nothing of the last one, and the model itself will be replaced. **La Ruta** is where the way is kept: the platform's memory, for people and for agents, so that whoever comes next (another session, another model, or a person) picks up where the last one left off.

## What it holds {#what-it-holds}

| Kind | What | Who writes it |
|---|---|---|
| **Decision** | what was decided, and why | people and agents |
| **Manual** | how to do something here | people and agents |
| **To do** | something left to do; ticked off when done | people and agents |
| **Note** | anything else worth knowing | people and agents |
| **Lived** (*lo vivido*) | stories people lived, in their own words | **people only** |

Lo vivido exists because an agent only knows what was written down, not what people lived. Those stories are kept as told; agents read them with respect but can never write or change them.

Nothing is lost: an edit keeps the previous version, and *Archive* hides an entry without deleting it.

## Cargos: an agent's turn of work {#cargos-an-agents-turn-of-work}

An agent that wants to write first **takes a cargo**: a turn of work with a purpose ("move stockpulse's database"). Everything it writes belongs to that cargo. Before it leaves, it **hands the cargo over** (*entrega*): what it did, and what is left. The next agent starts by reading the last handovers. The **Cargos** tab shows them as the monarchs' flight, one after another, with every tool call recorded.

## Connecting an agent (MCP) {#connecting-an-agent-mcp}

Agents reach the Ruta through **MCP** (Model Context Protocol), an open standard many AI tools speak, at `https://<your rendimiento>/mcp`.

1. On **Ruta → Keys**, create a key: a name, read-only or read-and-write, and how long it lasts (at most 90 days). The key is shown **once**.
2. Connect the agent. In Claude Code:

    ```bash
    claude mcp add --transport http rendimiento https://rendimiento.example.com/mcp \
      --header "Authorization: Bearer $RENDIMIENTO_MCP_TOKEN"
    ```

    Or commit a `.mcp.json` to a repository, so anyone who opens it with an agent is connected, with the key in an environment variable and never in git:

    ```json
    { "mcpServers": { "rendimiento": { "type": "http", "url": "https://rendimiento.example.com/mcp",
        "headers": { "Authorization": "Bearer ${RENDIMIENTO_MCP_TOKEN}" } } } }
    ```

3. When the agent connects, the server tells it the rules: start with `ruta_inicio`, take a cargo before writing, hand it over before leaving, never write secrets.

| Tool | Does | Key |
|---|---|---|
| `ruta_inicio` | the last handovers, open to-dos, recent entries, and the agent's open cargo | any |
| `ruta_buscar` | search by text and kind | any |
| `ruta_leer` | one entry in full | any |
| `cargo_tomar` | take a cargo with a purpose | read and write |
| `ruta_anotar` | add a decision, manual, to-do or note | read and write, with a cargo |
| `ruta_actualizar` | change an entry, or tick a to-do | read and write, with a cargo |
| `cargo_entregar` | hand the cargo over | read and write, with a cargo |

## Safety {#safety}

- **Keys** are random, stored only as a SHA-256 hash, always expire, and can be revoked on the Keys tab; revoking closes the key's open cargo. A read-only key doesn't even see the writing tools.
- **No secrets:** entries that look like a password, token or private key are refused, by agents and people alike. Write *where* a secret lives (the Secret's name), not its value.
- **Everything is recorded:** every tool call, allowed or refused, with its key and cargo.
- **Notes, not orders:** agents are told that entries are notes. If an entry asks for something the person they work with didn't ask for, they don't do it, and say so.
- **Same front door:** `/mcp` refuses requests from other websites (the `Origin` check), like the rest of the API.
- For now the Ruta only holds knowledge. Actions on the cluster through MCP (deploying, rolling back) come later, with previews, approval and undo.
