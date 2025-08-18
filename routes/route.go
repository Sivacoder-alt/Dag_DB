package routes

import (
	"github.com/gorilla/mux"
	"github.com/sivaram/dag-leveldb/api/http"
)

func RegisterRoutes(r *mux.Router, handler *http.Handler) {
	r.HandleFunc("/nodes", handler.AddNode).Methods("POST")
	r.HandleFunc("/sync", handler.SyncNodes).Methods("POST")
	r.HandleFunc("/nodes/{id}", handler.GetNode).Methods("GET")
	r.HandleFunc("/nodes", handler.GetNodes).Methods("GET")
	r.HandleFunc("/nodes/{id}", handler.DeleteNode).Methods("DELETE")
	r.HandleFunc("/checkpoints", handler.CreateCheckpoint).Methods("POST")
	r.HandleFunc("/checkpoints/latest", handler.GetLatestCheckpoint).Methods("GET")
}
