package web

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"time"
)

//go:embed static
var staticFiles embed.FS

// keepaliveInterval hält SSE-Verbindungen über Proxys hinweg offen.
var keepaliveInterval = 30 * time.Second

// NewHandler liefert den HTTP-Handler mit Seite, JSON-API und Server-Sent Events.
func NewHandler(store *Store, logger *slog.Logger) http.Handler {
	static, err := fs.Sub(staticFiles, "static")
	if err != nil {
		panic(err) // nur bei Programmierfehler möglich
	}

	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(static))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, _ *http.Request) {
		data, err := store.Snapshot()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(data)
	})
	mux.HandleFunc("GET /api/events", func(w http.ResponseWriter, r *http.Request) {
		serveEvents(w, r, store, logger)
	})
	return mux
}

func serveEvents(w http.ResponseWriter, r *http.Request, store *Store, logger *slog.Logger) {
	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")

	updates, unsubscribe := store.Subscribe()
	defer unsubscribe()

	send := func() error {
		data, err := store.Snapshot()
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "event: state\ndata: %s\n\n", data); err != nil {
			return err
		}
		return rc.Flush()
	}

	if err := send(); err != nil {
		logger.Debug("sse send failed", "error", err)
		return
	}

	keepalive := time.NewTicker(keepaliveInterval)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-updates:
			if err := send(); err != nil {
				logger.Debug("sse send failed", "error", err)
				return
			}
		case <-keepalive.C:
			if _, err := fmt.Fprint(w, ": keepalive\n\n"); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
		}
	}
}

// Serve startet den Webserver und läuft, bis ctx beendet wird.
func Serve(ctx context.Context, addr string, handler http.Handler, logger *slog.Logger) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		// Laufende SSE-Verbindungen beim Herunterfahren mit abbrechen.
		BaseContext: func(_ net.Listener) context.Context { return ctx },
	}

	errCh := make(chan error, 1)
	go func() {
		logger.Info("web server started", "address", addr)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		return fmt.Errorf("web server: %w", err)
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("web server shutdown: %w", err)
		}
		return nil
	}
}
