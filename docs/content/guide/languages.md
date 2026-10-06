# Languages

rendimiento speaks **English** and **Mexican Spanish** (es-MX).

## In the browser

The top bar has a language switch (**ES** / **EN**). The first time, the language follows the browser's; after that, the choice is remembered in that browser. Dates and numbers follow the language too.

The Spanish is formal (*usted*) and translates technical terms: *despliegue*, *compilación*, *versión*, *reversión*, *complemento*, *confirmación* (commit), *envío* (push), *solicitud de incorporación* (pull request).

## In emails

`NOTIFY_LANG` sets the language of [notification emails](notifications.md): `en` (the default) or `es`.

## Messages from the platform

What the platform writes for people (environment checks, run and release results, problems, validation errors, emails) is written in English with `i18n.M`, which is `fmt.Sprintf` and also marks the format for translation. It is translated **when it is shown**, not when it is made:

- the API sees `Accept-Language: es` and translates the JSON it answers with: sentences (`message`, `summary`, `details`, `error`…) through the catalog's patterns, short labels (`name`, `title`) only on an exact match, so an app's own name never changes;
- a finished English message is matched against the catalog's formats and the Spanish one is formatted with the same values, which may move (`%[2]s`) and are translated themselves; lists (`a; b`) translate part by part.

So messages stored long ago translate too, and code that makes messages needs no language.

## Adding text

- **In the UI**, write the phrase in English inside `t("…")` (or `tn(n, "one", "many")` for a count) and add its Spanish to `web/src/i18n/es.ts`. `npm run typecheck` fails if a phrase has no Spanish or its `{placeholders}` differ.
- **In Go**, make the message with `M("format %s", value)` and add the format's Spanish to `internal/i18n/es.go`. `TestCatalog` fails if a format has no Spanish or a different number of values.
