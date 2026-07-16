// Command strumentrec is the dev-only fixture recorder of
// fixture-harness-spec.md §1: an HTTP reverse proxy that sits between a
// client (Python aider, or Strument itself) and an OpenAI-compatible
// upstream, logging both directions verbatim while re-emitting the upstream
// SSE unchanged.
//
//	strumentrec -out capture.jsonl -upstream https://openrouter.ai -listen 127.0.0.1:8484
//	python -m aider --openai-api-base http://127.0.0.1:8484/api/v1 ...
//
// The upstream is scheme+host only; the request path is forwarded as-is.
// Captured rows are raw wire artifacts ("raw_request"/"raw_response"); they
// are distilled into scenario fixtures separately. Secret headers are
// stripped from the written rows (never from the forwarded request).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// stripHeaders never appear in captured rows (fixture-harness §1).
var stripHeaders = []string{"Authorization", "Api-Key", "X-Api-Key", "Cookie", "Set-Cookie", "Proxy-Authorization"}

type recorder struct {
	mu       sync.Mutex
	out      *os.File
	upstream *url.URL
	client   *http.Client
}

type rawRow struct {
	V       int               `json:"v"`
	Kind    string            `json:"kind"`
	Time    string            `json:"time"`
	Method  string            `json:"method,omitempty"`
	Path    string            `json:"path,omitempty"`
	Status  int               `json:"status,omitempty"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

func cleanHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, vs := range h {
		out[k] = strings.Join(vs, ", ")
	}
	for _, k := range stripHeaders {
		delete(out, http.CanonicalHeaderKey(k))
	}
	return out
}

func (rec *recorder) write(row rawRow) {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	enc := json.NewEncoder(rec.out)
	if err := enc.Encode(row); err != nil {
		log.Printf("write row: %v", err)
	}
}

func (rec *recorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	rec.write(rawRow{
		V: 1, Kind: "raw_request", Time: time.Now().UTC().Format(time.RFC3339),
		Method: r.Method, Path: r.URL.RequestURI(),
		Headers: cleanHeaders(r.Header), Body: string(body),
	})

	up := *rec.upstream
	up.Path = r.URL.Path
	up.RawQuery = r.URL.RawQuery
	req, err := http.NewRequestWithContext(r.Context(), r.Method, up.String(), strings.NewReader(string(body)))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	req.Header = r.Header.Clone()
	req.Header.Del("Accept-Encoding") // keep the recorded stream readable
	req.Host = up.Host

	resp, err := rec.client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		rec.write(rawRow{V: 1, Kind: "raw_response", Time: time.Now().UTC().Format(time.RFC3339),
			Status: 0, Headers: nil, Body: "PROXY ERROR: " + err.Error()})
		return
	}
	defer resp.Body.Close()

	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)

	// Tee the response to the client chunk by chunk so SSE streams live.
	var captured strings.Builder
	buf := make([]byte, 32*1024)
	flusher, _ := w.(http.Flusher)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			captured.Write(buf[:n])
			if _, werr := w.Write(buf[:n]); werr != nil {
				break
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if rerr != nil {
			break
		}
	}
	rec.write(rawRow{
		V: 1, Kind: "raw_response", Time: time.Now().UTC().Format(time.RFC3339),
		Status: resp.StatusCode, Headers: cleanHeaders(resp.Header), Body: captured.String(),
	})
	log.Printf("%s %s -> %d (%d bytes)", r.Method, r.URL.Path, resp.StatusCode, captured.Len())
}

func main() {
	out := flag.String("out", "", "output JSONL file (appended)")
	upstream := flag.String("upstream", "https://openrouter.ai", "upstream scheme+host")
	listen := flag.String("listen", "127.0.0.1:8484", "listen address")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "strumentrec: -out is required")
		os.Exit(2)
	}
	u, err := url.Parse(*upstream)
	if err != nil || u.Scheme == "" || u.Host == "" {
		log.Fatalf("bad -upstream %q", *upstream)
	}
	f, err := os.OpenFile(*out, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	rec := &recorder{out: f, upstream: u, client: &http.Client{Timeout: 10 * time.Minute}}
	log.Printf("recording %s -> %s into %s", *listen, u, *out)
	log.Fatal(http.ListenAndServe(*listen, rec))
}
