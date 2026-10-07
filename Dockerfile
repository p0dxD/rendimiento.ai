FROM node:20-alpine AS ui
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
# Types and translations are checked here, so every image build checks the UI.
RUN npm run typecheck && npm run build

FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=ui /web/dist ./web/dist
ARG GIT_SHA=""
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/rendimiento ./cmd/rendimiento

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/rendimiento /rendimiento
USER nonroot:nonroot
EXPOSE 8080 9090
ENTRYPOINT ["/rendimiento"]
