package http

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"github.com/sirupsen/logrus"
	"github.com/sivaram/dag-leveldb/internal/dag"
	"github.com/sivaram/dag-leveldb/internal/model"
	"github.com/sivaram/dag-leveldb/internal/store"
)

func setupTest(t *testing.T) (*Handler, *store.Store, func()) {
	// Sanitize t.Name() to remove slashes for Windows compatibility
	safeTestName := strings.ReplaceAll(t.Name(), "/", "_")
	tmpDir, err := os.MkdirTemp("", "leveldb-test-"+safeTestName)
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	// Initialize store
	st, err := store.New(tmpDir)
	if err != nil {
		t.Fatalf("Failed to initialize store: %v", err)
	}

	// Initialize logger
	logger := logrus.New()
	logger.SetLevel(logrus.InfoLevel)
	logger.SetOutput(os.Stdout)

	// Initialize DAG and handler with maxParents=5
	dagManager := dag.New(st, logger, 5)
	handler := NewHandler(dagManager)

	// Cleanup function
	cleanup := func() {
		st.Close()
		if err := os.RemoveAll(tmpDir); err != nil {
			t.Logf("Failed to clean up temp dir %s: %v", tmpDir, err)
		}
	}

	return handler, st, cleanup
}

func TestAddNode(t *testing.T) {
	t.Run("Add valid node without parents", func(t *testing.T) {
		handler, _, cleanup := setupTest(t)
		defer cleanup()

		node := store.Node{
			ID:     "node1",
			Data:   "test data",
			Weight: 1.0,
		}
		body, _ := json.Marshal(node)
		req := httptest.NewRequest("POST", "/nodes", bytes.NewReader(body))
		w := httptest.NewRecorder()

		handler.AddNode(w, req)

		if w.Code != http.StatusCreated {
			t.Errorf("Expected status %d, got %d", http.StatusCreated, w.Code)
		}
		var resp map[string]string
		json.NewDecoder(w.Body).Decode(&resp)
		if resp["message"] != "Node added successfully" {
			t.Errorf("Expected message 'Node added successfully', got %s", resp["message"])
		}
	})

	t.Run("Add node with invalid JSON", func(t *testing.T) {
		handler, _, cleanup := setupTest(t)
		defer cleanup()

		req := httptest.NewRequest("POST", "/nodes", bytes.NewReader([]byte("invalid json")))
		w := httptest.NewRecorder()

		handler.AddNode(w, req)

		if w.Code != http.StatusBadRequest {
			t.Errorf("Expected status %d, got %d", http.StatusBadRequest, w.Code)
		}
		if w.Body.String() != "Invalid request payload\n" {
			t.Errorf("Expected error message 'Invalid request payload', got %s", w.Body.String())
		}
	})

	t.Run("Add duplicate node", func(t *testing.T) {
		handler, _, cleanup := setupTest(t)
		defer cleanup()

		node := store.Node{
			ID:     "node2",
			Data:   "test data",
			Weight: 1.0,
		}
		body, _ := json.Marshal(node)
		req := httptest.NewRequest("POST", "/nodes", bytes.NewReader(body))
		w := httptest.NewRecorder()
		handler.AddNode(w, req)

		req = httptest.NewRequest("POST", "/nodes", bytes.NewReader(body))
		w = httptest.NewRecorder()
		handler.AddNode(w, req)

		if w.Code != http.StatusConflict {
			t.Errorf("Expected status %d, got %d", http.StatusConflict, w.Code)
		}
		if w.Body.String() != "node with ID node2 already exists\n" {
			t.Errorf("Expected error message for duplicate node, got %s", w.Body.String())
		}
	})

	t.Run("Add node with parents", func(t *testing.T) {
		handler, _, cleanup := setupTest(t)
		defer cleanup()

		parent := store.Node{ID: "parent1", Data: "parent data", Weight: 1.0}
		body, _ := json.Marshal(parent)
		req := httptest.NewRequest("POST", "/nodes", bytes.NewReader(body))
		w := httptest.NewRecorder()
		handler.AddNode(w, req)

		node := store.Node{
			ID:      "node3",
			Data:    "child data",
			Parents: []string{"parent1"},
			Weight:  2.0,
		}
		body, _ = json.Marshal(node)
		req = httptest.NewRequest("POST", "/nodes", bytes.NewReader(body))
		w = httptest.NewRecorder()
		handler.AddNode(w, req)

		if w.Code != http.StatusCreated {
			t.Errorf("Expected status %d, got %d", http.StatusCreated, w.Code)
		}

		nodez, err := handler.dag.GetNode("parent1")
		if err != nil {
			t.Fatalf("Failed to get parent: %v", err)
		}
		if nodez.CumulativeWeight != 3.0 {
			t.Errorf("Expected parent cumulative weight 3.0, got %f", node.CumulativeWeight)
		}
	})

}

func TestGetNode(t *testing.T) {
	t.Run("Get existing node", func(t *testing.T) {
		handler, st, cleanup := setupTest(t)
		defer cleanup()

		node := store.Node{
			ID:               "node1",
			Data:             "test data",
			Weight:           1.0,
			CumulativeWeight: 1.0,
		}
		st.AddNode(&node)

		req := httptest.NewRequest("GET", "/nodes/node1", nil)
		req = mux.SetURLVars(req, map[string]string{"id": "node1"})
		w := httptest.NewRecorder()

		handler.GetNode(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
		}
		var resp model.GetNodeResponse
		json.NewDecoder(w.Body).Decode(&resp)
		if resp.ID != "node1" || resp.Data != "test data" || !resp.Istip {
			t.Errorf("Expected node1 with data 'test data' and is_tip true, got %+v", resp)
		}
	})

	t.Run("Get non-existent node", func(t *testing.T) {
		handler, _, cleanup := setupTest(t)
		defer cleanup()

		req := httptest.NewRequest("GET", "/nodes/nonexistent", nil)
		req = mux.SetURLVars(req, map[string]string{"id": "nonexistent"})
		w := httptest.NewRecorder()

		handler.GetNode(w, req)

		if w.Code != http.StatusNotFound {
			t.Errorf("Expected status %d, got %d", http.StatusNotFound, w.Code)
		}
		if w.Body.String() != "Node not found\n" {
			t.Errorf("Expected error message 'Node not found', got %s", w.Body.String())
		}
	})
}

func TestDeleteNode(t *testing.T) {
	t.Run("Delete existing node without children", func(t *testing.T) {
		handler, st, cleanup := setupTest(t)
		defer cleanup()

		node := store.Node{ID: "node1", Data: "test data", Weight: 1.0}
		st.AddNode(&node)

		req := httptest.NewRequest("DELETE", "/nodes/node1", nil)
		req = mux.SetURLVars(req, map[string]string{"id": "node1"})
		w := httptest.NewRecorder()

		handler.DeleteNode(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status %d, got %d", http.StatusOK, w.Code)
		}
		var resp map[string]string
		json.NewDecoder(w.Body).Decode(&resp)
		if resp["message"] != "Node deleted successfully" {
			t.Errorf("Expected message 'Node deleted successfully', got %s", resp["message"])
		}

		n, err := st.GetNode("node1")
		if err != nil || n != nil {
			t.Errorf("Expected node to be deleted, got %+v, err: %v", n, err)
		}
	})

	t.Run("Delete node with children", func(t *testing.T) {
		handler, st, cleanup := setupTest(t)
		defer cleanup()

		parent := store.Node{ID: "parent1", Data: "parent data", Weight: 1.0}
		child := store.Node{ID: "child1", Data: "child data", Parents: []string{"parent1"}, Weight: 1.0}
		st.AddNode(&parent)
		st.AddNode(&child)

		req := httptest.NewRequest("DELETE", "/nodes/parent1", nil)
		req = mux.SetURLVars(req, map[string]string{"id": "parent1"})
		w := httptest.NewRecorder()

		handler.DeleteNode(w, req)

		if w.Code != http.StatusConflict {
			t.Errorf("Expected status %d, got %d", http.StatusConflict, w.Code)
		}
		if !bytes.Contains([]byte(w.Body.String()), []byte("has children")) {
			t.Errorf("Expected error message about children, got %s", w.Body.String())
		}
	})

	t.Run("Delete non-existent node", func(t *testing.T) {
		handler, _, cleanup := setupTest(t)
		defer cleanup()

		req := httptest.NewRequest("DELETE", "/nodes/nonexistent", nil)
		req = mux.SetURLVars(req, map[string]string{"id": "nonexistent"})
		w := httptest.NewRecorder()

		handler.DeleteNode(w, req)

		if w.Code != http.StatusInternalServerError {
			t.Errorf("Expected status %d, got %d", http.StatusInternalServerError, w.Code)
		}
		if !bytes.Contains([]byte(w.Body.String()), []byte("node with ID nonexistent not found")) {
			t.Errorf("Expected error message for non-existent node, got %s", w.Body.String())
		}
	})
}

func TestMCMCTipSelection(t *testing.T) {
	t.Run("MCMC with single node", func(t *testing.T) {
		handler, st, cleanup := setupTest(t)
		defer cleanup()

		node := store.Node{ID: "node1", Data: "test data", Weight: 1.0}
		st.AddNode(&node)

		tips, err := handler.dag.SelectTipsMCMC(2)
		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if len(tips) != 1 || tips[0] != "node1" {
			t.Errorf("Expected tip [node1], got %v", tips)
		}
	})

	t.Run("MCMC with empty DAG", func(t *testing.T) {
		handler, _, cleanup := setupTest(t)
		defer cleanup()

		tips, err := handler.dag.SelectTipsMCMC(2)
		if err == nil || err.Error() != "no nodes in DAG" {
			t.Errorf("Expected error 'no nodes in DAG', got %v", err)
		}
		if tips != nil {
			t.Errorf("Expected nil tips, got %v", tips)
		}
	})

	t.Run("MCMC with multiple nodes", func(t *testing.T) {
		handler, st, cleanup := setupTest(t)
		defer cleanup()

		nodes := []store.Node{
			{ID: "n1", Weight: 1.0},
			{ID: "n2", Parents: []string{"n1"}, Weight: 2.0},
			{ID: "n3", Parents: []string{"n1"}, Weight: 1.5},
		}
		for _, n := range nodes {
			st.AddNode(&n)
		}

		tips, err := handler.dag.SelectTipsMCMC(2)
		if err != nil {
			t.Errorf("Expected no error, got %v", err)
		}
		if len(tips) > 2 {
			t.Errorf("Expected at most 2 tips, got %d", len(tips))
		}
		for _, tip := range tips {
			if tip != "n2" && tip != "n3" {
				t.Errorf("Expected tips n2 or n3, got %s", tip)
			}
		}
	})
}

func TestWeightPropagation(t *testing.T) {
	t.Run("Cumulative weight update", func(t *testing.T) {
		handler, st, cleanup := setupTest(t)
		defer cleanup()

		nodes := []store.Node{
			{ID: "n1", Weight: 1.0},
			{ID: "n2", Parents: []string{"n1"}, Weight: 2.0},
			{ID: "n3", Parents: []string{"n2"}, Weight: 3.0},
		}
		for _, n := range nodes {
			err := handler.dag.AddNode(&n)
			if err != nil {
				t.Fatalf("Failed to add node %s: %v", n.ID, err)
			}
		}

		n1, _ := st.GetNode("n1")
		n2, _ := st.GetNode("n2")
		if n1.CumulativeWeight != 6.0 {
			t.Errorf("Expected n1 cumulative weight 6.0, got %f", n1.CumulativeWeight)
		}
		if n2.CumulativeWeight != 5.0 {
			t.Errorf("Expected n2 cumulative weight 5.0, got %f", n2.CumulativeWeight)
		}
	})

	t.Run("Weight decrement on delete", func(t *testing.T) {
		handler, st, cleanup := setupTest(t)
		defer cleanup()

		nodes := []store.Node{
			{ID: "n1", Weight: 1.0},
			{ID: "n2", Parents: []string{"n1"}, Weight: 2.0},
			{ID: "n3", Parents: []string{"n2"}, Weight: 3.0},
		}
		for _, n := range nodes {
			err := handler.dag.AddNode(&n)
			if err != nil {
				t.Fatalf("Failed to add node %s: %v", n.ID, err)
			}
		}

		err := handler.dag.DeleteNode("n3")
		if err != nil {
			t.Errorf("Expected no error deleting n3, got %v", err)
		}

		n2, _ := st.GetNode("n2")
		if n2.CumulativeWeight != 2.0 {
			t.Errorf("Expected n2 cumulative weight 2.0 after deleting n3, got %f", n2.CumulativeWeight)
		}

		err = handler.dag.DeleteNode("n2")
		if err != nil {
			t.Errorf("Expected no error deleting n2, got %v", err)
		}

		n1, _ := st.GetNode("n1")
		if n1.CumulativeWeight != 1.0 {
			t.Errorf("Expected n1 cumulative weight 1.0 after deleting n2, got %f", n1.CumulativeWeight)
		}
	})
}
