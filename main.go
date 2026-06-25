// main.go
// DeciScope API サーバーのエントリーポイント。
// app.NewServerRuntime() により HTTP サーバーを構築し、指定ポートで起動する。
// このファイル自体はルーティングやロジックを持たず、サーバー起動のみを担当する。
package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"deciscope-core-api/internal/app"
)

func main() {
	app.LoadEnvironmentFiles()

	runtime, err := app.NewServerRuntime()
	if err != nil {
		log.Fatalf("build server: %v", err)
	}

	address := app.ListenAddressFromEnv()
	server := &http.Server{Addr: address, Handler: runtime.Handler}
	log.Printf("DeciScope backend listening on %s", address)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown server: %v", err)
		}
		if err := runtime.Close(); err != nil {
			log.Printf("close resources: %v", err)
		}
		if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	case err := <-errCh:
		if closeErr := runtime.Close(); closeErr != nil {
			log.Printf("close resources: %v", closeErr)
		}
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}
}
