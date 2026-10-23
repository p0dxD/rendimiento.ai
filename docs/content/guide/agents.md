# Built for agents

rendimiento is meant to be run **by people and AI agents together**. An agent is not a chat window beside the platform: it is a user of it, with its own key, its own rights, a turn of work it answers for, and a record of everything it did. This page is how that works today and where it is going.

## The ideas {#the-ideas}

- **Agents are users.** An agent connects with a [key](ruta.md#connecting-an-agent-mcp) a person made for it, which says what it may do (its **role**) and on which apps. Everything a person can see in the UI, an agent can read over **MCP**.
- **Memory outside the session.** An agent forgets everything when its session ends, and the model will be replaced. What is learned is kept in [la Ruta](ruta.md): decisions with their reasons, manuals, to-dos, and each agent's handover.
- **Turns of work.** An agent writes or acts only inside a **cargo**: a turn with a purpose, handed over with what was done and what is left. Whoever comes next starts from the last handovers.
- **People decide what reaches production.** Agents test; a person merges and deploys. An agent can run CI on any branch except the default one, because running the default branch deploys.
- **Everything is recorded.** Every tool call, allowed or refused, is kept with its key and cargo (**Ruta → Cargos**).
- **Notes, not orders.** What an agent reads in the Ruta or a log is information. If it asks for something the person did not ask for, the agent does not do it, and says so.

## Roles {#roles}

| Role | May |
|---|---|
| **Reader** | read the Ruta, the apps, runs, step logs, releases, problems and kept requests |
| **Collaborator** | also, with a cargo: write the Ruta, edit Mercado pages, run CI on a branch, cancel a run |

A key can also be limited to **some apps**: the platform's tools then show and act on those only. The role *administrator* is reserved for actions that will need a person's approval (rolling back, deploying), which are not open to agents yet.

## What an agent can do today {#what-an-agent-can-do-today}

All of it over one MCP server, at `https://<your rendimiento>/mcp`:

| Tool | Does | Role |
|---|---|---|
| `ruta_inicio`, `ruta_buscar`, `ruta_leer` | start from the last handovers and to-dos; search and read the Ruta | reader |
| `apps_listar`, `app_estado` | the apps, with their last run, release (and its verification) and open problems | reader |
| `ejecucion_leer`, `paso_registro` | a run with its steps, and the end of a step's log: why something failed | reader |
| `solicitudes_listar`, `solicitud_leer` | kept requests and their [waterfall](universe.md#following-one-request): where a slow or failed request spent its time | reader |
| `cargo_tomar`, `cargo_entregar` | take and hand over a turn of work | collaborator |
| `ruta_anotar`, `ruta_actualizar` | write decisions, manuals, to-dos and notes | collaborator, with a cargo |
| `ejecucion_lanzar`, `ejecucion_cancelar` | test a branch in CI; cancel a run | collaborator, with a cargo |
| `encargos_listar`, `encargo_leer` | the work asked of agents, and one encargo with its history | reader |
| `encargo_crear`, `encargo_tomar`, `encargo_esperar`, `encargo_entregar`, `encargo_soltar` | leave work for later; take an encargo; wait on its run; hand it over; let it go | collaborator, with a cargo |
| `mercado_*` | read and edit [Mercado](mercado.md) pages | reader / collaborator |

A typical turn: the agent calls `ruta_inicio`, which shows the pending encargos, takes a cargo and an encargo, reads why a run failed (`ejecucion_leer`, `paso_registro`, `solicitud_leer`), fixes the code on a branch, runs CI on it (`ejecucion_lanzar`), waits for the run (`encargo_esperar`), hands the encargo over with the link for the person to merge, and hands its cargo over.

## Encargos: work asked of agents {#encargos}

An **encargo** is work with an owner and a life cycle: **pending → taken → waiting → delivered → reviewed**.

- **Asking.** On **Ruta → Encargos**, write what to do, the detail and the app. On a failed run, **Ask an agent** makes one with the failed steps already in it. An agent can also leave one (`encargo_crear`) for something it found and will not do now.
- **Taking.** An agent connected over MCP sees the pending ones in `ruta_inicio` and takes one inside its cargo. Only one agent can have it.
- **Waiting.** After starting a run, the agent calls `encargo_esperar`: the call returns when the run ends, woken by the platform's own events (up to 15 minutes a call; it calls again to keep waiting). The encargo shows *waiting on run #N* meanwhile. No agent asks every 30 seconds.
- **Handing over.** `encargo_entregar` with what was done, how it was checked, and the links (branch, run, pull request).
- **Review.** You mark it **Reviewed**, or **Send back** with what is missing: it is pending again, with your comment, for whichever agent takes it next. An agent that cannot finish lets it go (`encargo_soltar`) with the reason.

Everything that happens to an encargo is in its **history**. For now an encargo waits until an agent session starts; agents inside the platform (step 5 below) will take them on their own.

`https://<your rendimiento>/llms.txt` describes all this for an agent that finds the platform on its own.

## Agents inside the platform {#agents-inside-the-platform}

Some agents don't wait for a person's session: they run inside the platform (the `rendimiento-agents` service, `ROLE=agents`) and wake on what happens in it. Each asks the organization's model through the [AI gateway](ai.md), so it stays within the organization's caps, and falls back to the local model.

- **The diagnostician.** When a run fails, it reads the failed steps, the end of their logs and the files changed since the branch's last good run, and writes **Why did it fail?** on the run's page: the most likely cause, quoting the log line, and how to fix it. Lines that look like secrets never leave the platform. An organization turns it off on its AI page; a collaborator asks for an explanation (again) on any failed run. **Ask an agent** then carries the explanation into the encargo.

Only one copy of the agents works at a time (a lock in Postgres), so nothing is done twice during an update.

## Where it is going {#where-it-is-going}

In this order, each built on the one before:

1. **Roles, the platform's tools and the record** (above).
2. **Encargos** (above): done for runs. Waiting on a merged pull request or a person's approval comes with step 3.
3. **Approvals:** an agent asks for an action (merge, deploy, roll back) with its reason; the person approves or refuses it in the UI or by email.
4. **Preview environments:** each branch deployed on its own for a while, with its URL and traces, so an agent tests against the real thing before asking to merge.
5. **Agents inside the platform** (above): the diagnostician is the first. Next, agents that take encargos and fix code on a branch, which need a strong model, a sandbox with the code, and a spending cap set by the person.
6. **AI in the product:** *why did this fail?* on every failed run, search by meaning in the Ruta, and each agent session as a trace.
