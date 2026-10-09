# The Mercado (pages without code)

The **Mercado** makes web pages for people who don't write code. You fill in a form and rendimiento makes the page, publishes it at its own address and keeps it. Every page is a **stall** (a *puesto*), and the Mercado page in the top bar shows all of them under their awnings.

## Opening a stall {#opening-a-stall}

**Mercado → Open a stall** asks, in order:

- **What kind of page**: *Personal* (about you and where to find you), *Business* (what you offer, hours, how to reach you), *Event* (when and where, and its story) or *Portfolio* (your work in photos). The kind names the page's sections and suggests details: hours and address for a business, date, time and place for an event.
- **Its name**, a line under it, and its **address**: one word under one of the DNS zones (`ana.example.com`). The word also names the app and its repository.
- **Colors**: one of five Cotija palettes (cempasúchil, grana, añil, nopal, rosa mexicano).
- **A main photo**, shown round at the top, and up to eight more.
- **Its story** (paragraphs, separated by a blank line), its **details** (label and text) and **where to find you**: Instagram, Facebook or TikTok usernames, a WhatsApp or phone number, an email address, a website.
- **The page's language**: Spanish or English, for the page's own words (its headings and footer). What you write stays as you wrote it.

The preview beside the form is the page itself, rendered by the platform as you type. **Open my stall** then:

1. creates a public repository in the Mercado's GitHub organization (`PAGES_ORG`), named after the address;
2. registers it as an app and commits the page to it: `index.html`, `pagina.json` (the form's answers), the photos in `fotos/`, a `Dockerfile` (nginx) and its `rendimiento.yaml`;
3. invites whoever opened it to the repository (*maintain*), so they can change it in GitHub too.

That commit is a push like any other: rendimiento builds the page and publishes it, with its DNS record and certificate. A page is one small service (10m CPU, 32 MiB of memory).

## Changing a stall {#changing-a-stall}

**Change it** on a stall opens the same form with the page as it is. **Save and publish** writes one commit (the new `index.html` and `pagina.json`, new photos, and without the photos no longer used) and the push publishes it. The release history, rollbacks, uptime and visits work as for any app: **Technical details** opens the app's page.

The form reads the page from `pagina.json` on the default branch, so a change made in GitHub shows in the form. `index.html` is rewritten on every save; change the page through `pagina.json` or the form, not by editing the HTML.

## What is safe {#what-is-safe}

Nothing typed in the form becomes HTML: names, texts and details are escaped, links are rebuilt from a username, a number or an address (only `https`, `mailto:` and `tel:` links come out), and photos must be JPEG, PNG, WebP or GIF by their content, not their name. The form shrinks photos to at most 1600 pixels before sending them. An address already used by an app, or by any ingress of the cluster, is refused, and so is anything outside the DNS zones. The preview is shown in a sandboxed frame, without scripts.

## Setting it up {#setting-it-up}

The Mercado is off until `PAGES_ORG` is set. Once:

1. **Create a GitHub organization** for the pages (free), e.g. `example-paginas`.
2. **Give the GitHub App the Administration permission** (read and write): in GitHub, the App's settings → *Permissions & events* → *Repository permissions* → *Administration*. It is what lets it create repositories and invite people. GitHub asks each installation to accept the new permission.
3. **Install the App on the organization** for **all repositories**, so it reaches the repositories it creates. A private App can only be installed on its owner's account: set `GITHUB_ACCOUNTS` and make it public first ([Installing on an organization](../environment/deploy.md#the-github-app)).
4. Set `PAGES_ORG` to the organization's name and restart rendimiento.

People who open stalls sign in like everyone else: with GitHub, and on the allowed list (`ALLOWED_USERS`).

Deleting the app of a stall (Settings → Delete) takes the page offline and out of the Mercado; its repository stays in the organization, to keep or delete in GitHub.
