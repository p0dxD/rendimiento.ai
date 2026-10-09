FROM node:20-alpine AS ui
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY web/ ./
# Types and translations are checked here, so every image build checks the UI.
RUN npm run typecheck && npm run build

FROM golang:1.27 AS build
WORKDIR /src
# The module and build caches stay on the BuildKit daemon between builds
# (cache mounts, not layers), so a change recompiles only what it touched
# and the image's cache export stays small.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
COPY --from=ui /web/dist ./web/dist
ARG GIT_SHA=""
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/rendimiento ./cmd/rendimiento

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/rendimiento /rendimiento
USER nonroot:nonroot
EXPOSE 8080 9090
ENTRYPOINT ["/rendimiento"]
