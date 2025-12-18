package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	"github.com/srypher/mural-challenge-backend/internal/handlers"
	"github.com/srypher/mural-challenge-backend/internal/models"
	"github.com/srypher/mural-challenge-backend/internal/mural"
	"github.com/srypher/mural-challenge-backend/internal/storage"
)

func main() {
	_ = godotenv.Load()

	ctx := context.Background()
	db, err := storage.NewDB(ctx)
	if err != nil {
		log.Fatalf("db init: %v", err)
	}
	defer db.Pool.Close()

	// For this demo image, start from a clean slate on each container start so
	// repeated $1 test payments are easier to reason about.
	if strings.ToLower(os.Getenv("RESET_ORDERS_ON_START")) == "true" {
		if _, err := db.Pool.Exec(ctx, "TRUNCATE TABLE orders"); err != nil {
			log.Printf("failed to truncate orders table: %v", err)
		} else {
			log.Printf("orders table truncated on startup")
		}
	}

	muralClient, err := mural.NewClient(mural.Config{
		BaseURL:     getEnv("MURAL_BASE_URL", "https://api-staging.muralpay.com"),
		APIKey:      os.Getenv("MURAL_API_KEY"),
		TransferKey: os.Getenv("MURAL_TRANSFER_KEY"),
		// OrganizationID and AccountID will be derived automatically when possible.
	})
	if err != nil {
		log.Fatalf("mural client init: %v", err)
	}

	backendBaseURL := os.Getenv("BACKEND_BASE_URL")
	useWebhooks := strings.ToLower(getEnv("USE_WEBHOOKS", "false")) == "true"
	if backendBaseURL == "" && useWebhooks {
		log.Println("USE_WEBHOOKS is true but BACKEND_BASE_URL is not set; webhooks will not be configured")
	}

	orderStore := models.NewOrderStore(db.Pool)

	app := handlers.NewApp(orderStore, muralClient, backendBaseURL, useWebhooks)
	mux := app.Routes()

	addr := getEnv("PORT", "8080")

	srv := &http.Server{
		Addr:         ":" + addr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	// graceful shutdown
	go func() {
		log.Printf("backend listening on :%s", addr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctxShutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctxShutdown)
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
