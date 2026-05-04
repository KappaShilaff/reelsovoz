package health

import (
	"context"
	"log/slog"
	"net/http"
)

func Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", ok)
	mux.HandleFunc("GET /readyz", ok)
	return mux
}

func Start(ctx context.Context, addr string, logger *slog.Logger) *http.Server {
	server := &http.Server{
		Addr:    addr,
		Handler: Handler(),
	}

	go func() {
		<-ctx.Done()
		if err := server.Shutdown(context.Background()); err != nil {
			logger.Error("health server shutdown failed", "error", err)
		}
	}()

	go func() {
		logger.Info("health server started", "addr", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("health server failed", "error", err)
		}
	}()

	return server
}

func ok(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK\n"))
}
