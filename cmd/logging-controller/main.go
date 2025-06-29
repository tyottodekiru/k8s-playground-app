// cmd/logging-controller/main.go
package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tyottodekiru/k8s-playground/internal/controllers"
	"github.com/tyottodekiru/k8s-playground/pkg/queue"
)

func main() {
	logDir := getEnv("LOG_DIR", "/var/log/k8s-playground")
	redisURL := getEnv("REDIS_URL", "redis://localhost:6379")
	apiPort := getEnv("API_PORT", "8081")

	// Initialize Redis connection
	redisQueue, err := queue.NewRedisQueue(redisURL)
	if err != nil {
		log.Fatalf("Failed to initialize Redis queue: %v", err)
	}
	defer redisQueue.Close()

	// Initialize logging controller with Redis support
	loggingController := controllers.NewLoggingControllerWithRedis(logDir, redisQueue.Client)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the logging controller
	go func() {
		log.Println("Starting logging controller...")
		if err := loggingController.Start(ctx); err != nil {
			log.Printf("Logging controller error: %v", err)
		}
	}()

	// Setup HTTP API for admin access
	mux := http.NewServeMux()
	mux.HandleFunc("/admin/auth", loggingController.HandleAdminAuth)
	mux.HandleFunc("/admin/logs", loggingController.HandleAdminLogs)
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	server := &http.Server{
		Addr:    ":" + apiPort,
		Handler: mux,
	}

	// Start HTTP server
	go func() {
		log.Printf("Starting admin API server on port %s", apiPort)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP server error: %v", err)
		}
	}()

	// Wait for interrupt signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down logging controller...")

	// Shutdown HTTP server
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("HTTP server shutdown error: %v", err)
	}

	// Cancel context and wait for cleanup
	cancel()
	time.Sleep(2 * time.Second)

	log.Println("Logging controller exited")
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}