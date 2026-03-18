package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"

	appcfg "github.com/yourorg/whatsapp-s3-uploader/config"
	"github.com/yourorg/whatsapp-s3-uploader/handlers"
	"github.com/yourorg/whatsapp-s3-uploader/services"
)

func main() {
	// Load .env if present (ignored in production where env vars are set directly).
	if err := godotenv.Load(); err != nil {
		log.Println("no .env file found, reading from environment")
	}

	cfg, err := appcfg.Load()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	// Build services.
	s3Svc, err := services.NewS3Service(cfg)
	if err != nil {
		log.Fatalf("s3 service error: %v", err)
	}

	waSvc := services.NewWhatsAppService(cfg)

	// Build handlers.
	webhookHandler := handlers.NewWebhookHandler(cfg, s3Svc, waSvc)

	// Register routes.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handlers.HealthCheck)

	// WhatsApp webhook — Meta calls GET to verify and POST to deliver messages.
	mux.HandleFunc("GET /webhook", webhookHandler.Verify)
	mux.HandleFunc("POST /webhook", webhookHandler.Receive)

	// Direct file upload endpoint (for non-WhatsApp clients).
	mux.HandleFunc("POST /upload", webhookHandler.DirectUpload)

	server := &http.Server{
		Addr:         ":" + cfg.Port,
		Handler:      loggingMiddleware(mux),
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Start in background.
	go func() {
		log.Printf("server listening on :%s", cfg.Port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	// Graceful shutdown on SIGINT / SIGTERM.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("forced shutdown: %v", err)
	}
	log.Println("server stopped")
}

// loggingMiddleware logs method, path, and latency for every request.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rw, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, rw.code, time.Since(start))
	})
}

type responseWriter struct {
	http.ResponseWriter
	code int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.code = code
	rw.ResponseWriter.WriteHeader(code)
}
