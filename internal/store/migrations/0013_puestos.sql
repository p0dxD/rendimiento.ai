-- El Mercado: web pages made from a form, by people who don't write code.
-- Each page (puesto) is an app like any other, in a repository rendimiento
-- created; this row remembers who opened it and what kind of page it is.
-- The page itself lives in its repository (pagina.json).
CREATE TABLE puestos (
    app_id     BIGINT PRIMARY KEY REFERENCES apps(id) ON DELETE CASCADE,
    owner      TEXT NOT NULL,  -- GitHub login of whoever opened it
    tipo       TEXT NOT NULL,  -- personal | negocio | evento | portafolio
    nombre     TEXT NOT NULL,  -- the page's title, as shown in the Mercado
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
