// Command showcase is the Signal flagship: a live audience platform wired to
// run as a 3-pod cluster over a NATS-JetStream backplane with Postgres-backed
// auth, profiles, avatars, and durable vote persistence. This file is WIRING
// ONLY — config, db open+migrate, plugins, deps, mount, serve.
package main

import (
	"context"
	"errors"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/plugins/echarts"
	"github.com/go-via/via/plugins/maplibre"
	"github.com/go-via/via/plugins/picocss"
	"github.com/go-via/vianats"
	"github.com/nats-io/nats.go"

	"github.com/go-via/viashowcase/internal/assets"
	"github.com/go-via/viashowcase/internal/auth"
	"github.com/go-via/viashowcase/internal/store"
	"github.com/go-via/viashowcase/internal/ui"
)

// maxUpload caps the avatar multipart body. 4 MiB rejected typical phone
// photos (often 3–8 MiB) with a bare 413 — "upload didn't work". 10 MiB
// comfortably fits a full-res photo; the form hint states the limit.
const maxUpload = 10 << 20 // 10 MiB avatars

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	port := env("PORT", "3000")
	node := env("NODE_NAME", "node")

	db, err := openDB(env("DATABASE_URL", ""))
	if err != nil {
		return err
	}
	defer db.Close()

	bp, err := backplane(env("NATS_URL", ""))
	if err != nil {
		return err
	}

	chart := echarts.NewChart(
		echarts.WithDimensions("100%", "360px"),
		echarts.WithPalette("#ffbf00"),
		// The app defaults to dark mode; the dark chart theme uses light axis
		// text so labels are readable on the dark result panel (light theme's
		// #222/#666 text was near-invisible there).
		echarts.WithThemeOverride(echarts.ThemeDark),
		echarts.WithInitialOption(map[string]any{
			"xAxis": map[string]any{"type": "category", "data": []string{}},
			"yAxis": map[string]any{"type": "value"},
		}),
	)
	gmap := maplibre.NewMap(
		maplibre.WithDimensions("100%", "360px"),
		// A dark basemap matches the dark UI (the default demotiles style is a
		// bright blue that clashes); CARTO's dark-matter style is free + key-less.
		// The amber pins pop against it.
		maplibre.WithStyle("https://basemaps.cartocdn.com/gl/dark-matter-gl-style/style.json"),
		maplibre.WithZoom(1),
		maplibre.WithGeoJSONSource("pins", maplibre.FeatureCollection()),
		maplibre.WithLayer(maplibre.CircleLayer("pins", "pins",
			maplibre.Paint("circle-color", "#ffbf00"), maplibre.Paint("circle-radius", 6))),
	)

	app := via.New(
		via.WithTitle("Signal"),
		via.WithAddr(":"+port),
		via.WithPlugins(
			picocss.Plugin(
				// Serve ALL themes so the 19-colour picker actually works — the
				// plugin only serves the themes it is given (default: just one).
				picocss.WithThemes(picocss.AllPicoThemes),
				picocss.WithDefaultTheme(picocss.PicoThemeAmber),
				picocss.WithDarkMode(),
			),
			echarts.Plugin(),
			maplibre.Plugin(),
		),
		via.WithBackplane(bp),
		via.WithInsecureCookies(), // demo runs over plain http behind the LB
		via.WithMaxUploadSize(maxUpload),
		via.WithLogger(via.SlogLogger(slog.Default())),
		via.WithMetrics(logMetrics{}),
	)

	app.AppendToHead(assets.Head()...)
	// GET-scoped (not HandleStatic's all-methods "/assets/") so it doesn't
	// collide with the "GET /" home route under net/http's ServeMux rules.
	app.HandleFunc("GET /assets/", http.StripPrefix("/assets/",
		http.FileServer(http.FS(assets.FS))).ServeHTTP)
	app.HandleFunc("GET /avatar/{id}", avatarHandler(db))
	app.HandleFunc("GET /healthz", healthzHandler(db, node))

	ui.Deps.DB = db
	ui.Deps.App = app
	ui.Deps.Chart = chart
	ui.Deps.Map = gmap

	// Public routes.
	via.Mount[ui.Home](app, "/")
	via.Mount[ui.Login](app, "/login")
	via.Mount[ui.Signup](app, "/signup")
	via.Mount[ui.Logout](app, "/logout")
	via.Mount[ui.Join](app, "/r/{code}")

	// Headless: binds the app-global Votes log and registers the durable
	// OnEvent consumer that persists each vote to Postgres (idempotent by offset).
	via.Mount[ui.Persistence](app, "/_persist")

	// Guarded groups: redirect to /login when there is no host session.
	appGrp := app.Group("/app")
	appGrp.Use(auth.Require())
	via.Mount[ui.Profile](appGrp, "/profile")

	hostGrp := app.Group("/host")
	hostGrp.Use(auth.Require())
	via.Mount[ui.Host](hostGrp, "/{code}")

	// Arm the durable vote consumer: the headless /_persist composition registers
	// the OnEvent handler in its OnInit, so hit it once at boot (idempotent per
	// name+key) — durable side-effects must run whether or not anyone is watching.
	go arm("http://127.0.0.1:" + port + "/_persist")
	log.Printf("%s listening on :%s", node, port)
	// Start serves and gracefully shuts down on SIGTERM/SIGINT — draining SSE
	// streams, running OnDispose, and closing the backplane cleanly on deploy.
	app.Start()
	return nil
}

// arm GETs url until it succeeds, so a lifecycle-registered consumer is wired
// without depending on a user navigating there.
func arm(url string) {
	for i := 0; i < 30; i++ {
		if resp, err := http.Get(url); err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(time.Second)
	}
}

// openDB opens the store and migrates, retrying briefly so it tolerates
// Postgres still booting in a freshly-started compose stack.
func openDB(dsn string) (*store.Store, error) {
	var last error
	for i := 0; i < 30; i++ {
		s, err := store.Open(dsn)
		if err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			err = s.Migrate(ctx)
			cancel()
			if err == nil {
				return s, nil
			}
			s.Close()
		}
		last = err
		time.Sleep(time.Second)
	}
	return nil, errors.New("store: not ready: " + errStr(last))
}

// backplane returns a durable JetStream backplane when NATS_URL is set, else the
// in-process InMemory one so a single node runs with no infrastructure. It
// retries so it tolerates NATS/JetStream still warming up in a fresh stack —
// the connection must be live before JetStream() creates the KV bucket, so a
// blocking Connect (not RetryOnFailedConnect) is used and the whole bring-up is
// retried.
func backplane(url string) (via.Backplane, error) {
	if strings.TrimSpace(url) == "" {
		return via.InMemory(), nil
	}
	var last error
	for i := 0; i < 30; i++ {
		nc, err := nats.Connect(url, nats.Timeout(3*time.Second), nats.MaxReconnects(-1))
		if err == nil {
			bp, jerr := vianats.JetStream(nc)
			if jerr == nil {
				return bp, nil
			}
			nc.Close()
			last = jerr
		} else {
			last = err
		}
		time.Sleep(time.Second)
	}
	return nil, errors.New("backplane: not ready: " + errStr(last))
}

// healthzHandler is a lightweight readiness probe for the load balancer and
// orchestrator: 200 only when Postgres is reachable, 503 otherwise — so a pod
// whose DB link has died is routed around instead of serving broken requests.
func healthzHandler(db *store.Store, node string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := db.Ping(ctx); err != nil {
			http.Error(w, "db unavailable", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok " + node))
	}
}

// avatarHandler streams an avatar's bytes from Postgres, 404 when absent.
func avatarHandler(db *store.Store) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		ct, data, err := db.Avatar(r.Context(), r.PathValue("id"))
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", ct)
		// Defense in depth: never let the browser sniff a stored avatar into an
		// executable type. Upload also restricts stored types to raster images.
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(data)
	}
}

// logMetrics is a minimal observability sink: it logs counters (the events that
// matter operationally — fold halts, consumer errors) and ignores the high-volume
// gauges/histograms. A real deployment would forward to Prometheus/OTel.
type logMetrics struct{}

func (logMetrics) Counter(name string, labels ...string) {
	slog.Info("metric", "counter", name, "labels", strings.Join(labels, ","))
}
func (logMetrics) Gauge(string, float64, ...string)     {}
func (logMetrics) Histogram(string, float64, ...string) {}

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func errStr(err error) string {
	if err == nil {
		return "unknown"
	}
	return err.Error()
}
