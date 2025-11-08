# Live Reload with Air

Hot-reloads your Go code and web page.

## Setup

If you don't have Air yet:
```bash
go install github.com/air-verse/air@latest
```

## Run
```bash
air
```

Then open `http://localhost:3000` in your browser.

## How It Works

Air watches your Go files and rebuilds when you make changes.

LiveReloadPlugin handles browser refresh through a SSE connection at `/dev/reload`. When Air restarts the server, the connection drops, triggering an automatic page reload after 100ms. This only runs on localhost.

## Files

- `.air.toml` - Air config
- `livereload.go` - Via plugin for browser auto-reload
