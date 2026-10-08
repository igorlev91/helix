package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"helix/internal/config"
	"helix/internal/router"
	"helix/internal/server"
	"helix/internal/session"
	"helix/internal/tracker"
)

// Startup order is the same: CLI -> config -> components -> server -> graceful shutdown.
func main() {
	// 1. CLI commands (Nakama: migrate / check / healthcheck / --version)
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "--version":
			fmt.Println("helix dev")
			return
		case "healthcheck":
			resp, err := http.Get("http://localhost:7350/health")
			if err != nil || resp.StatusCode != http.StatusOK {
				log.Fatal("healthcheck failed")
			}
			fmt.Println("healthcheck ok")
			return
		}
	}

	// 2. Config: YAML + flags (analog of server.ParseArgs)
	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	log.Printf("Helix starting")
	log.Printf("Node name=%s verbose=%v", cfg.Name, cfg.Verbose)
	log.Printf("Data directory path=%s", cfg.Datadir)
	log.Printf("Database dsn=%s", cfg.Database)

	// 3. Components (Nakama builds ~25 objects here; we add them step by step)
	registry := session.NewRegistry()
	tr := tracker.New()
	rt := router.New()
	rt.RegisterBuiltinOps(registry)
	rt.RegisterChatOps(tr)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/ws", server.NewWsHandler(registry, rt, tr))
	mux.HandleFunc("/sessions", server.NewSessionsHandler(registry))

	// 4. HTTP server on the client port 
	srv := &http.Server{Addr: fmt.Sprintf(":%d", cfg.Port), Handler: mux}
	go func() {
		log.Printf("Client port=%d", cfg.Port)
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	log.Printf("Startup done")


	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	log.Printf("Shutting down")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(ctx)
	log.Printf("Shutdown complete")
}
