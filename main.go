package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"raices/app"
	"raices/database"
	"raices/env"
	"raices/mail"
	"raices/payment"
)

func main() {
	if os.Getenv("ENVIRONMENT") == "development" {
		if err := godotenv.Load(".env.dev"); err != nil {
			logger.Warning("env file: %v", err)
		}
	}

	if err := env.Sync(); err != nil {
		logger.Error("env: %v", err)
		os.Exit(1)
	}

	db, err := database.Open(env.SqlitePath)
	if err != nil {
		logger.Error("database: %v", err)
		os.Exit(1)
	}
	defer db.Close()

	mp, err := payment.NewMPClient()
	if err != nil {
		logger.Error("mp client: %v", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	appDeps := app.Dependencies{
		Mux:     mux,
		MP:      mp,
		DB:      db,
		Logger:  logger,
		Charger: payment.NewCharger(mp),
		Sender:  mail.New(db, env.ResendAPIKey),
	}
	app.Routes(appDeps)

	mux.Handle("GET /public/", http.StripPrefix("/public/", public()))

	server := &http.Server{
		Addr:              ":" + env.Port,
		Handler:           SecurityMiddleware(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.ListenAndServe()
	}()

	logger.Info("Starting server...")
	select {
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			logger.Error("server stopped: %v", err)
			os.Exit(1)
		}
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdownCtx); err != nil {
			logger.Error("server shutdown: %v", err)
			os.Exit(1)
		}
	}
}
