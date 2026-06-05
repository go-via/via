# Build context is the REPO ROOT (see docker-compose.yml `context: ../..`),
# because viashowcase is its own module whose go.mod `replace`s point at the
# sibling `via` and `vianats` modules in the repo — both must be in the context.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY . .
WORKDIR /src/viashowcase
RUN CGO_ENABLED=0 go build -o /showcase ./cmd/showcase

FROM alpine:3.20
COPY --from=build /showcase /showcase
EXPOSE 3000
ENTRYPOINT ["/showcase"]
