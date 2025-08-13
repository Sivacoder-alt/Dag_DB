package main

import (
	"flag"
	"log"
	server "net/http"

	"github.com/gorilla/mux"
	"github.com/sivaram/dag-leveldb/api/http"
	"github.com/sivaram/dag-leveldb/internal/config"
	"github.com/sivaram/dag-leveldb/internal/dag"
	"github.com/sivaram/dag-leveldb/internal/logger"
	"github.com/sivaram/dag-leveldb/internal/store"
	"github.com/sivaram/dag-leveldb/routes"
)

func main() {
	configPath := flag.String("config", "config/config.yaml", "Path to configuration file")
	flag.Parse()

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	logr, err := logger.NewLogger(cfg)
	if err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}

	st, err := store.New(cfg.LevelDB.Path)
	if err != nil {
		log.Fatalf("Failed to initialize store: %v", err)
	}
	defer st.Close()

	dagManager := dag.New(st, logr,cfg.DAG.MaxParents)
	handler := http.NewHandler(dagManager)

	r := mux.NewRouter()
	routes.RegisterRoutes(r, handler) 
	log.Printf("Server listening on %s", cfg.Server.ListenAddr)
	if err := server.ListenAndServe(cfg.Server.ListenAddr, r); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
