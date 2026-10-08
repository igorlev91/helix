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
)


func main() {

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


	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	log.Printf("Helix starting")
	log.Printf("Node name=%s verbose=%v", cfg.Name, cfg.Verbose)
	log.Printf("Data directory path=%s", cfg.Datadir)
	log.Printf("Database dsn=%s", cfg.Database)


	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})


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
