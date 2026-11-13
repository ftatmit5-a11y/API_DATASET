package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"yoloapi/internal/api"
)

func main() {
	addr := env("ADDR", ":8080")
	dataDir := env("DATA_DIR", "data")
	maxMB, _ := strconv.Atoi(env("MAX_UPLOAD_MB", "512"))
	if maxMB <= 0 {
		maxMB = 512
	}

	srv, err := api.New(dataDir, int64(maxMB)<<20)
	if err != nil {
		log.Fatal(err)
	}
	defer srv.Close()

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       5 * time.Minute,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		log.Printf("YOLO dataset API listening on %s", addr)
		log.Printf("data directory: %s", dataDir)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(ctx); err != nil {
		log.Printf("shutdown: %v", err)
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
