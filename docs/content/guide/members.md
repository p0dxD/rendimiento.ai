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

Everything belongs to an **organization**: its apps (with their runs, releases and requests), its Ruta, encargos, agent keys and members. People of one organization never see another's: lists leave them out, and their pages answer *not found*. A person can belong to several, with a role in each, and switches with the selector at the top; the role that counts is the one in the organization they are working in.

The **platform's organization** (the first one) runs the platform. Its administrators are the **operators**: they alone see the cluster's pages (Environment, Add-ons, Services, Problems, the Mercado), update the platform, and, for now, create organizations (under Members → Organizations) and onboard apps, in whichever organization they are working in. An agent key belongs to the organization it was made in, and its tools see only that organization.

Today all organizations share the platform's cluster. Organizations in a dedicated virtual cluster, or on their own cluster in any cloud, come later.
