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
		if strings.Contains(err.Error(), "cycle detected") || strings.Contains(err.Error(), "parent does not exist") {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		http.Error(w, "Failed to add node", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"message": "Node added successfully"})
}

func (h *Handler) GetNodes(w http.ResponseWriter, r *http.Request) {
	nodes, err := h.dag.GetAllNodes()
	if err != nil {
		http.Error(w, "Failed to fetch nodes", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(nodes); err != nil {
		http.Error(w, "Failed to encode nodes", http.StatusInternalServerError)
		return
	}
}

func (h *Handler) SyncNodes(w http.ResponseWriter, r *http.Request) {
	var nodes []store.Node
	if err := json.NewDecoder(r.Body).Decode(&nodes); err != nil {
		http.Error(w, "Invalid request payload", http.StatusBadRequest)
		return
	}

	for _, node := range nodes {
		if err := h.dag.AddNode(&node); err != nil {
			continue
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "Nodes synced successfully"})
}

func (h *Handler) GetNode(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	node, err := h.dag.GetNode(id)
	if err != nil {
		http.Error(w, "Failed to fetch node", http.StatusInternalServerError)
		return
	}
	if node == nil {
		http.Error(w, "Node not found", http.StatusNotFound)
		return
	}

	isTip, err := h.dag.IsTip(id)
	if err != nil {
		http.Error(w, "Failed to check if node is tip", http.StatusInternalServerError)
		return
	}

	resp := model.GetNodeResponse{
		ID:               node.ID,
		Data:             node.Data,
		Parents:          node.Parents,
		Weight:           node.Weight,
		CumulativeWeight: node.CumulativeWeight,
		Istip:            isTip,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return
	}
}

func (h *Handler) DeleteNode(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	id := vars["id"]

	if err := h.dag.DeleteNode(id); err != nil {
		if strings.Contains(err.Error(), "not found") {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		if strings.Contains(err.Error(), "has children") {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		http.Error(w, "Failed to delete node", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"message": "Node deleted successfully"})
}
