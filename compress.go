package lplex

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"
)

// gzipWriterPool reuses gzip writers to reduce allocations.
var gzipWriterPool = sync.Pool{
	New: func() any {
		w, _ := gzip.NewWriterLevel(io.Discard, gzip.BestSpeed)
		return w
	},
}

// CompressHandler wraps an http.Handler with gzip compression.
// Responses are compressed when the client sends Accept-Encoding: gzip
// and the response Content-Type is not text/event-stream (SSE streams
// are left uncompressed because per-event gzip flushing adds overhead
// without meaningful compression on small JSON payloads).
func CompressHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}

		cw := &compressResponseWriter{
			ResponseWriter: w,
		}
		defer cw.close()

		next.ServeHTTP(cw, r)
	})
}

// compressResponseWriter defers gzip initialization until the first Write,
// so it can inspect Content-Type and skip compression for SSE streams.
type compressResponseWriter struct {
	http.ResponseWriter
	gz       *gzip.Writer
	decided  bool
	passthru bool // true = no compression, write directly
}

func (cw *compressResponseWriter) init() {
	if cw.decided {
		return
	}
	cw.decided = true

	ct := cw.ResponseWriter.Header().Get("Content-Type")
	if strings.HasPrefix(ct, "text/event-stream") {
		cw.passthru = true
		return
	}

	gz := gzipWriterPool.Get().(*gzip.Writer)
	gz.Reset(cw.ResponseWriter)
	cw.gz = gz
	cw.ResponseWriter.Header().Set("Content-Encoding", "gzip")
	cw.ResponseWriter.Header().Del("Content-Length")
}

func (cw *compressResponseWriter) WriteHeader(code int) {
	cw.init()
	cw.ResponseWriter.WriteHeader(code)
}

func (cw *compressResponseWriter) Write(b []byte) (int, error) {
	cw.init()
	if cw.passthru {
		return cw.ResponseWriter.Write(b)
	}
	return cw.gz.Write(b)
}

// Flush flushes compressed or uncompressed data to the client.
func (cw *compressResponseWriter) Flush() {
	if cw.gz != nil {
		cw.gz.Flush() //nolint:errcheck
	}
	if f, ok := cw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap returns the underlying ResponseWriter for http.ResponseController.
func (cw *compressResponseWriter) Unwrap() http.ResponseWriter {
	return cw.ResponseWriter
}

func (cw *compressResponseWriter) close() {
	if cw.gz != nil {
		cw.gz.Close() //nolint:errcheck
		gzipWriterPool.Put(cw.gz)
	}
}
