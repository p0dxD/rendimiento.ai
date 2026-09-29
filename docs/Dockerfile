# syntax=docker/dockerfile:1
# The book, built in three stages from the whole repository (build context
# = repository root, so the book can include real source files):
#   1. codemap: generate the code reference from the Go sources
#   2. site:    build the static site with MkDocs Material
#   3. runtime: serve it with an unprivileged nginx on port 8080
# Only the last stage becomes the image; the others are thrown away.

FROM golang:1.27-alpine AS codemap
WORKDIR /src
COPY . .
RUN go run ./hack/codemap > /code-map.md

FROM python:3.12-slim AS site
WORKDIR /src
# Dependencies first: this layer is reused until requirements.txt changes.
COPY docs/requirements.txt docs/requirements.txt
RUN pip install --no-cache-dir -r docs/requirements.txt
COPY . .
COPY --from=codemap /code-map.md docs/content/reference/code-map.md
RUN cd docs && mkdocs build --site-dir /site

FROM nginxinc/nginx-unprivileged:1.27-alpine
COPY --from=site /site /usr/share/nginx/html
EXPOSE 8080
