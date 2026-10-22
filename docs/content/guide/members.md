# Members and roles

People sign in to rendimiento with GitHub, and only **members** get in. Each member has a **role** that says what they may do; every route of the API checks it, so the UI and the API agree.

| Role | May |
|---|---|
| **Guest** | see everything: apps, runs and their logs, releases, the [Universe](universe.md), the [Ruta](ruta.md), encargos |
| **Collaborator** | also: run CI, cancel runs, write the Ruta, [ask agents](agents.md#encargos) for things and review what they hand over, edit [Mercado](mercado.md) pages, turn Renovate on for an app |
| **Administrator** | also: deploy and roll back, read and change secrets, onboard and remove apps, manage add-ons, agent keys, members, and update the platform |

## Adding someone {#adding-someone}

On **Members** (your name in the top bar), an administrator writes the person's **GitHub username** and their role. They can sign in from then on, at the same address. Changing a role or removing someone takes effect on their next click; a removed person is signed out at once. An organization always keeps at least one administrator.

## The first administrators {#the-first-administrators}

A new platform has no members, so the `ALLOWED_USERS` setting names the first administrators. They are added once, while there are no members; after that, people are managed on the Members page and the setting no longer counts.

## Organizations {#organizations}

Today everything (apps, the Ruta, encargos, agent keys) belongs to one organization. The data already records which organization each thing is in, so a later version can hold several: each with its own people, apps, agents and AI budget, on a shared cluster, a dedicated one, or the organization's own.
