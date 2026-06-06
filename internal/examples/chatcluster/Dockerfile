# Build context is the REPO ROOT (see docker-compose.yml `context: ../../..`),
# because this example is its own module whose go.mod `replace`s point at the
# sibling `via` and `vianats` modules in the repo — both must be in the context.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY . .
WORKDIR /src/internal/examples/chatcluster
RUN CGO_ENABLED=0 go build -o /chatcluster .

FROM alpine:3.20
COPY --from=build /chatcluster /chatcluster
EXPOSE 3000
ENTRYPOINT ["/chatcluster"]
