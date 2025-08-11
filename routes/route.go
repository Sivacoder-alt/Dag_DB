package routes

import (
	"github.com/gorilla/mux"
	"github.com/sivaram/dag-leveldb/api/http"
)

// RegisterRoutes registers all routes with the given router and handler
func RegisterRoutes(r *mux.Router, handler *http.Handler) {
	r.HandleFunc("/nodes", handler.AddNode).Methods("POST")
	r.HandleFunc("/nodes/{id}", handler.GetNode).Methods("GET")
	r.HandleFunc("/nodes/{id}", handler.DeleteNode).Methods("DELETE")
}
