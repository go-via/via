## Shake.db

This DB was constructed following this excellent talk: https://www.youtube.com/watch?v=RqubKSF3wig

## Running

It's important to pass the fts5 build tag, or you'll get `no such module: fts5`.

Run with: `go run -tags fts5 main.go`