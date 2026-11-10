// Package httpserver corre un http.Server con timeouts y apagado ordenado,
// igual para el hub y para el agent.
package httpserver

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Run sirve h en addr hasta que ctx se cancela y después le da unos
// segundos para terminar las peticiones en curso.
func Run(ctx context.Context, addr string, h http.Handler, logger *slog.Logger) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}

	// ListenAndServe bloquea, así que corre en su propia goroutine y avisa
	// por el canal cuando termina. El select de abajo espera lo que pase
	// primero: que el servidor falle o que llegue la señal de apagado.
	errc := make(chan error, 1)
	go func() {
		logger.Info("escuchando", "addr", addr)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	logger.Info("apagando")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
