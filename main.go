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

	"github.com/google/uuid"

	"helix/internal/config"
	"helix/internal/db"
	"helix/internal/match"
	"helix/internal/matchmaker"
	"helix/internal/notify"
	"helix/internal/party"
	"helix/internal/router"
	"helix/internal/server"
	"helix/internal/session"
	"helix/internal/tracker"
)

// Startup order is the same: CLI -> config -> components -> server -> graceful shutdown.
func main() {
	// 1. CLI commands
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

	// 2. Config: YAML + flags
	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// 2b. Migrate command
	if len(os.Args) > 1 && os.Args[1] == "migrate" {
		sub := "up"
		if len(os.Args) > 2 {
			sub = os.Args[2]
		}
		conn, err := db.Connect(context.Background(), cfg.Database)
		if err != nil {
			log.Fatalf("database: %v", err)
		}
		defer conn.Close()
		switch sub {
		case "up":
			err = db.Up(context.Background(), conn)
		case "status":
			err = db.Status(context.Background(), conn)
		default:
			log.Fatalf("unknown migrate command: %s", sub)
		}
		if err != nil {
			log.Fatalf("migrate: %v", err)
		}
		log.Printf("migrate %s done", sub)
		return
	}

	log.Printf("Helix starting")
	log.Printf("Node name=%s verbose=%v", cfg.Name, cfg.Verbose)
	log.Printf("Data directory path=%s", cfg.Datadir)
	log.Printf("Database dsn=%s", cfg.Database)

	// 2c. Connect to the database and verify the schema
	conn, err := db.Connect(context.Background(), cfg.Database)
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer conn.Close()
	if err := db.Check(context.Background(), conn); err != nil {
		log.Fatalf("schema: %v", err)
	}
	log.Printf("Database connected")

	// 3. Components
	registry := session.NewRegistry()
	tr := tracker.New()
	parties := party.NewRegistry()
	notifier := notify.New(conn, registry)
	hooks := registerGameHooks(notifier)
	rt := router.New(hooks)
	// Authoritative matches; when a race ends, the winner's leaderboard
	// score is incremented server-side (clients cannot write scores).
	matches := match.NewRegistry(func(m *match.Match) {
		rh, ok := m.Handler().(*match.RaceHandler)
		if !ok {
			return
		}
		_, _ = conn.ExecContext(context.Background(),
			`INSERT INTO leaderboards (id, sort_order, operator) VALUES ('race_wins', 1, 2)
			 ON CONFLICT (id) DO NOTHING`)
		for _, res := range rh.Results() {
			if res.Rank != 1 {
				continue
			}
			_, err := conn.ExecContext(context.Background(),
				`INSERT INTO leaderboard_records (leaderboard_id, owner_id, username, score, update_time)
				 VALUES ('race_wins', $1, $2, 1, now())
				 ON CONFLICT (leaderboard_id, owner_id)
				 DO UPDATE SET score = leaderboard_records.score + 1, username = $2,
				               update_time = now(), num_score = leaderboard_records.num_score + 1`,
				res.UserID, res.Username)
			if err != nil {
				log.Printf("leaderboard write for match %s failed: %v", m.ID, err)
				continue
			}
			log.Printf("match %s: +1 race_wins for %s", m.ID, res.Username)
			// Persistent victory notification: survives reconnect
			if uid, err := uuid.Parse(res.UserID); err == nil {
				_ = notifier.Send(context.Background(), uid,
					"Race won!", 1, map[string]any{"match_id": m.ID, "rank": 1}, true, nil)
			}
		}
	})
	mm := matchmaker.New(router.NewMatchCallback(tr, matches))
	rt.RegisterBuiltinOps(registry)
	rt.RegisterChatOps(tr, conn)
	rt.RegisterStorageOps(conn)
	rt.RegisterLeaderboardOps(conn)
	rt.RegisterMatchmakerOps(mm)
	rt.RegisterMatchOps(matches)
	rt.RegisterNotifyOps(notifier)
	rt.RegisterFriendOps(conn, registry, notifier)
	rt.RegisterGroupOps(conn)
	rt.RegisterPartyOps(parties)
	rt.RegisterWalletOps(conn)
	rt.RegisterTournamentOps(conn)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mux.HandleFunc("/ws", server.NewWsHandler(registry, rt, tr, mm, matches, cfg.Session.EncryptionKey))
	mux.HandleFunc("/auth/device", server.NewAuthDeviceHandler(
		conn, cfg.Session.EncryptionKey, cfg.Session.RefreshEncryptionKey,
		time.Duration(cfg.Session.TokenExpirySec)*time.Second,
		time.Duration(cfg.Session.RefreshExpirySec)*time.Second))
	mux.HandleFunc("/auth/refresh", server.NewAuthRefreshHandler(
		cfg.Session.EncryptionKey, cfg.Session.RefreshEncryptionKey,
		time.Duration(cfg.Session.TokenExpirySec)*time.Second))
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
