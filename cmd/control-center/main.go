package main

import (
	"context"
	"crypto/tls"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/dockette/viewdoc"
	"github.com/dockette/viewdoc/internal/api"
	"github.com/dockette/viewdoc/internal/proxy"
	"github.com/dockette/viewdoc/internal/slots"
	"github.com/dockette/viewdoc/internal/tlsself"
)

const (
	defaultSlots      = "viewer-1,viewer-2,viewer-3"
	defaultListenAddr = ":8080"
)

func main() {
	addr := envOr("LISTEN_ADDR", defaultListenAddr)
	tlsAddr := envOr("TLS_ADDR", "")

	pool, err := slots.FromCSV(envOr("VIEWER_SLOTS", defaultSlots))
	if err != nil {
		log.Fatalf("VIEWER_SLOTS: %v", err)
	}

	for _, s := range pool.All() {
		log.Printf("slot %d %s -> %s", s.Index, s.PathPrefix(), s.VNCURL())
	}

	var tlsCfg *tls.Config
	if tlsAddr != "" {
		var err error
		tlsCfg, err = tlsself.Load(viewdoc.ServerCert, viewdoc.ServerKey)
		if err != nil {
			log.Fatalf("tls cert: %v", err)
		}
		log.Print("TLS enabled with embedded self-signed cert (certs/server.{crt,key})")
	}

	mux := http.NewServeMux()
	h := api.New(pool)
	mux.HandleFunc("GET /healthz", h.Healthz)
	mux.HandleFunc("GET /api/slots", h.Slots)
	mux.HandleFunc("POST /api/slot/{idx}/params", h.Params)
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	for _, s := range pool.All() {
		ph := proxy.ForSlot(s)
		// Both patterns are needed: the bare prefix to catch /slot/{i} (we
		// redirect to /slot/{i}/) and the subtree pattern for everything below.
		mux.Handle(s.PathPrefix(), ph)
		mux.Handle(s.PathPrefix()+"/", ph)
	}
	sub, err := fs.Sub(viewdoc.WebFS, "web")
	if err != nil {
		log.Fatalf("embed: %v", err)
	}
	mux.Handle("/", http.FileServer(http.FS(sub)))

	var servers []*http.Server
	servers = append(servers, listen(addr, logMiddleware(mux), nil))
	if tlsAddr != "" {
		servers = append(servers, listen(tlsAddr, logMiddleware(mux), tlsCfg))
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop
	log.Println("shutting down")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for _, srv := range servers {
		wg.Add(1)
		go func(srv *http.Server) {
			defer wg.Done()
			_ = srv.Shutdown(ctx)
		}(srv)
	}
	wg.Wait()
}

func listen(addr string, h http.Handler, cfg *tls.Config) *http.Server {
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		TLSConfig:         cfg,
	}
	go func() {
		var err error
		if cfg != nil {
			err = srv.ListenAndServeTLS("", "")
		} else {
			err = srv.ListenAndServe()
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen %s: %v", addr, err)
		}
	}()
	scheme := "http"
	if cfg != nil {
		scheme = "https"
	}
	log.Printf("listening on %s://0.0.0.0%s", scheme, addr)
	return srv
}

func envOr(k, def string) string {
	if v, ok := os.LookupEnv(k); ok && v != "" {
		return v
	}
	return def
}

func logMiddleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		h.ServeHTTP(w, r)
		log.Printf("%s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}

