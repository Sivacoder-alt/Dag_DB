package http

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/sivaram/dag-leveldb/internal/dag"
	"github.com/sivaram/dag-leveldb/internal/model"
	"github.com/sivaram/dag-leveldb/internal/store"
)

type Handler struct {
	dag *dag.DAG
}

func NewHandler(dag *dag.DAG) *Handler {
	return &Handler{dag: dag}
}


func (h *Handler) AddNode(w http.ResponseWriter, r *http.Request) {
	var node store.Node
	if err := json.NewDecoder(r.Body).Decode(&node); err != nil {
		http.Error(w, "Invalid request payload", http.StatusBadRequest)
		return
	}

	if err := h.dag.AddNode(&node); err != nil {
		if strings.Contains(err.Error(), "already exists") {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, "Failed to add node", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"message": "Node added successfully"})
}

func (h *Handler) GetNode(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]
	node, err := h.dag.GetNode(id)
	if err != nil {
		http.Error(w, "Failed to fetch node", http.StatusInternalServerError)
		return
	}
	if node == nil {
		http.Error(w, "Node not found", http.StatusNotFound)
		return
	}

	// Check if the node is a tip (no children)
	isTip, err := h.dag.IsTip(id)
	if err != nil {
		http.Error(w, "Failed to determine if node is a tip", http.StatusInternalServerError)
		return
	}

	response := model.GetNodeResponse{
		ID:               node.ID,
		Data:             node.Data,
		Parents:          node.Parents,
		Weight:           node.Weight,
		CumulativeWeight: node.CumulativeWeight,
		Istip:            isTip,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (h *Handler) DeleteNode(w http.ResponseWriter, r *http.Request) {
	id := mux.Vars(r)["id"]

	err := h.dag.DeleteNode(id)
	if err != nil {
		if strings.Contains(err.Error(), "has children") {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"message": "Node deleted successfully"})
}
