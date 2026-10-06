# Languages

rendimiento speaks **English** and **Mexican Spanish** (es-MX).

## In the browser

The top bar has a language switch (**ES** / **EN**). The first time, the language follows the browser's; after that, the choice is remembered in that browser. Dates and numbers follow the language too.

The Spanish is formal (*usted*) and translates technical terms: *despliegue*, *construcción* (build), *versión*, *reversión*, *complemento*, *confirmación* (commit), *envío* (push), *solicitud de incorporación* (pull request), *cubeta* (bucket), *balanceador de carga*, *entrada* (ingress), *demonio* (daemon).

## In this book

The book is in both languages too: the switch is in its header. A page's Spanish sits next to it (`page.es.md` beside `page.md`, with [mkdocs-static-i18n](https://github.com/ultrabug/mkdocs-static-i18n)); a page not translated yet shows in English in the Spanish book. Translated headings keep their English anchor (`## Título {#english-title}`), so links between pages work in both books.

## In emails

`NOTIFY_LANG` sets the language of [notification emails](notifications.md): `en` (the default) or `es`.

## Messages from the platform

What the platform writes for people (environment checks, run and release results, problems, validation errors, emails) is written in English with `i18n.M`, which is `fmt.Sprintf` and also marks the format for translation. It is translated **when it is shown**, not when it is made:

- the API sees `Accept-Language: es` and translates the JSON it answers with: sentences (`message`, `summary`, `details`, `error`…) through the catalog's patterns; short labels (`name`, `title`) word for word, or through a pattern only when it has enough fixed text of its own, so an app's own name never changes;
- a finished English message is matched against the catalog's formats and the Spanish one is formatted with the same values, which may move (`%[2]s`) and are translated themselves; lists (`a; b`) translate part by part.

So messages stored long ago translate too, and code that makes messages needs no language.

## Adding text

- **In the UI**, write the phrase in English inside `t("…")` (or `tn(n, "one", "many")` for a count) and add its Spanish to `web/src/i18n/es.ts`. `npm run typecheck` fails if a phrase has no Spanish or its `{placeholders}` differ.
- **In Go**, make the message with `M("format %s", value)` and add the format's Spanish to `internal/i18n/es.go`. `TestCatalog` fails if a format has no Spanish or a different number of values.
