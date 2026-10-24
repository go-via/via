package via

import (
	"compress/gzip"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// Encoder is one Content-Encoding via may answer with: Token is its
// Accept-Encoding name, NewWriter wraps the response body in it. via ships
// [Gzip]; any other coding plugs in as an adapter. Brotli, for one:
//
//	br := via.Encoder{Token: "br", NewWriter: func(w io.Writer) via.Compressor {
//		return brotli.NewWriterOptions(w, brotli.WriterOptions{Quality: 5, LGWin: 18})
//	}}
//	via.NewRouter(via.WithPageCompression(br, via.Gzip(6)))
type Encoder struct {
	Token     string
	NewWriter func(io.Writer) Compressor
}

// Compressor is the writer an [Encoder] returns. Flush must emit everything
// written so far as decodable output: via flushes after every SSE frame.
type Compressor interface {
	io.Writer
	Flush() error
	Close() error
}

// Gzip encodes with compress/gzip at level, from gzip.HuffmanOnly to
// gzip.BestCompression. It panics on a level outside that range.
func Gzip(level int) Encoder {
	if _, err := gzip.NewWriterLevel(io.Discard, level); err != nil {
		panic("via: Gzip level " + strconv.Itoa(level) + " out of range")
	}
	// A gzip writer allocates its window up front, so a page response would
	// otherwise pay for one per request.
	g := gzipPool{level: level, pool: &sync.Pool{}}
	return Encoder{Token: "gzip", NewWriter: g.newWriter}
}

type gzipPool struct {
	level int
	pool  *sync.Pool
}

func (g gzipPool) newWriter(w io.Writer) Compressor {
	zw, _ := g.pool.Get().(*gzip.Writer)
	if zw == nil {
		zw, _ = gzip.NewWriterLevel(w, g.level)
	} else {
		zw.Reset(w)
	}
	return pooledGzip{zw, g.pool}
}

type pooledGzip struct {
	*gzip.Writer
	pool *sync.Pool
}

func (p pooledGzip) Close() error {
	err := p.Writer.Close()
	p.pool.Put(p.Writer)
	return err
}

// setEncoders validates an option's encoders and stores them in dst. A set dst
// means the option was passed twice. No encoders means gzip at its default
// level.
func setEncoders(dst *[]Encoder, option string, encs []Encoder) {
	if *dst != nil {
		panic("via: conflicting " + option + " options; pass one")
	}
	if len(encs) == 0 {
		*dst = []Encoder{Gzip(gzip.DefaultCompression)}
		return
	}
	seen := map[string]bool{}
	for _, e := range encs {
		if e.Token == "" || e.NewWriter == nil {
			panic("via: " + option + ": an Encoder needs a Token and a NewWriter")
		}
		tok := strings.ToLower(e.Token)
		if seen[tok] {
			panic("via: " + option + ": duplicate encoder " + strconv.Quote(e.Token))
		}
		seen[tok] = true
	}
	*dst = slices.Clone(encs)
}

// negotiate picks the first of encs the Accept-Encoding header allows. The
// server's order wins over the client's q-values; q=0 refuses a coding, and *
// stands for every coding the header does not name.
func negotiate(accept string, encs []Encoder) (Encoder, bool) {
	if accept == "" || len(encs) == 0 {
		return Encoder{}, false
	}
	allowed := map[string]bool{}
	for part := range strings.SplitSeq(accept, ",") {
		tok, params, _ := strings.Cut(part, ";")
		allowed[strings.ToLower(strings.TrimSpace(tok))] = !zeroQ(params)
	}
	for _, e := range encs {
		ok, named := allowed[strings.ToLower(e.Token)]
		if !named {
			ok = allowed["*"]
		}
		if ok {
			return e, true
		}
	}
	return Encoder{}, false
}

func zeroQ(params string) bool {
	for p := range strings.SplitSeq(params, ";") {
		k, v, _ := strings.Cut(strings.TrimSpace(p), "=")
		if strings.EqualFold(k, "q") {
			q, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			return err == nil && q == 0
		}
	}
	return false
}

// compressWriter encodes a response with the first encoder the request
// accepts, from the SSE list for the connect stream and the page list for the
// rest. It decides once, as the header goes out, so Content-Encoding and the
// body always agree.
type compressWriter struct {
	http.ResponseWriter
	req       *http.Request
	page, sse []Encoder
	enc       Compressor
	decided   bool
}

func (r *Router) wrapForCompression(w http.ResponseWriter, req *http.Request) *compressWriter {
	if r.cfg.pageEnc == nil && r.cfg.sseEnc == nil {
		return nil
	}
	w.Header().Add("Vary", "Accept-Encoding")
	return &compressWriter{ResponseWriter: w, req: req, page: r.cfg.pageEnc, sse: r.cfg.sseEnc}
}

func (c *compressWriter) decide(code int) {
	c.decided = true
	hdr := c.Header()
	if c.req.Method == http.MethodHead || hdr.Get("Content-Encoding") != "" ||
		code == http.StatusNoContent || code == http.StatusNotModified || code == http.StatusPartialContent {
		return
	}
	encs := c.page
	// A plain action's answer is text/event-stream too, but it is a finished
	// response, so it compresses like a page.
	if strings.HasPrefix(hdr.Get("Content-Type"), "text/event-stream") && isStreamRequest(c.req) {
		encs = c.sse
	}
	e, ok := negotiate(c.req.Header.Get("Accept-Encoding"), encs)
	if !ok {
		return
	}
	hdr.Set("Content-Encoding", e.Token)
	hdr.Del("Content-Length")
	c.enc = e.NewWriter(c.ResponseWriter)
}

func (c *compressWriter) WriteHeader(code int) {
	// A 1xx goes out ahead of the real header and decides nothing.
	if !c.decided && code >= 200 {
		c.decide(code)
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *compressWriter) Write(p []byte) (int, error) {
	if !c.decided {
		// net/http would sniff the type from the compressed bytes.
		if _, set := c.Header()["Content-Type"]; !set {
			c.Header().Set("Content-Type", http.DetectContentType(p))
		}
		c.decide(http.StatusOK)
	}
	if c.enc != nil {
		return c.enc.Write(p)
	}
	return c.ResponseWriter.Write(p)
}

// FlushError pushes the encoder's pending output out before flushing the
// connection; a flush that skipped it would hold back every SSE frame.
func (c *compressWriter) FlushError() error {
	if !c.decided {
		c.decide(http.StatusOK)
	}
	if c.enc != nil {
		if err := c.enc.Flush(); err != nil {
			return err
		}
	}
	return http.NewResponseController(c.ResponseWriter).Flush()
}

// flushable answers canFlush for the writer underneath: FlushError alone
// would claim a stream the real writer cannot carry.
func (c *compressWriter) flushable() bool { return canFlush(c.ResponseWriter) }

func (c *compressWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }

func (c *compressWriter) close() {
	if c.enc != nil {
		c.enc.Close()
	}
}
