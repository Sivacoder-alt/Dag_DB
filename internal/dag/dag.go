package dag

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/sivaram/dag-leveldb/internal/store"
)

type DAG struct {
	store         *store.Store
	logger        *logrus.Logger
	maxParents    int
	defaultWeight float64
	mu            sync.RWMutex
}

func New(store *store.Store, logger *logrus.Logger, maxParents int, defaultWeight float64) *DAG {
	if maxParents <= 0 {
		maxParents = 2
	}
	if defaultWeight <= 0 {
		defaultWeight = 1.0
	}
	return &DAG{store: store, logger: logger, maxParents: maxParents, defaultWeight: defaultWeight}
}

func (d *DAG) AddNode(node *store.Node) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.logger.Infof("Adding node: %s", node.ID)

	existingNode, err := d.getNodeInternal(node.ID)
	if err != nil {
		d.logger.Errorf("Error checking for existing node %s: %v", node.ID, err)
		return fmt.Errorf("failed to check existing node: %v", err)
	}
	if existingNode != nil {
		d.logger.Warnf("Node with ID %s already exists", node.ID)
		return fmt.Errorf("node with ID %s already exists", node.ID)
	}

	// Only select tips if parents is not explicitly provided (i.e., null in JSON)
	// If parents: [] is sent, keep it as empty
	if node.Parents == nil {
		selectedTips, err := d.selectTipsMCMCInternal(2)
		if err != nil {
			d.logger.Warnf("Failed to select tips via MCMC: %v", err)
			if err.Error() != "no nodes in DAG" {
				return fmt.Errorf("failed to select parents: %v", err)
			}
		} else {
			node.Parents = selectedTips
			d.logger.Infof("Auto-selected parents (MCMC) for %s: %v", node.ID, node.Parents)
		}
	}

	if d.maxParents > 0 && len(node.Parents) > d.maxParents {
		return fmt.Errorf("node %s has too many parents: %d, max allowed: %d", node.ID, len(node.Parents), d.maxParents)
	}

	if err := d.checkCycle(node.ID, node.Parents); err != nil {
		d.logger.Warnf("Cycle check failed for node %s: %v", node.ID, err)
		return err
	}

	if node.Weight == 0 {
		node.Weight = d.defaultWeight
	}
	node.CumulativeWeight = node.Weight

	if err := d.store.AddNode(node); err != nil {
		d.logger.Errorf("Failed to store node %s: %v", node.ID, err)
		return fmt.Errorf("failed to store node: %v", err)
	}

	d.logger.Infof("Node %s added with weight %f", node.ID, node.Weight)

	if err := d.updateCumulativeWeights(node, node.Weight); err != nil {
		d.logger.Errorf("Failed to update cumulative weights for node %s: %v", node.ID, err)
		return fmt.Errorf("failed to update weights: %v", err)
	}

	return nil
}

func (d *DAG) GetAllNodes() ([]store.Node, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	nodes := []store.Node{}
	iter := d.store.Iterator()
	defer iter.Release()
	for iter.Next() {
		var node store.Node
		if err := json.Unmarshal(iter.Value(), &node); err != nil {
			d.logger.Errorf("Failed to unmarshal node: %v", err)
			continue
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

func (d *DAG) checkCycle(nodeID string, parents []string) error {
	for _, parentID := range parents {
		if parentID == nodeID {
			return fmt.Errorf("cycle detected: node %s cannot be its own parent", nodeID)
		}
		p, err := d.getNodeInternal(parentID)
		if err != nil {
			d.logger.Errorf("Error checking parent %s: %v", parentID, err)
			return fmt.Errorf("failed to check parent %s: %v", parentID, err)
		}
		if p == nil {
			return fmt.Errorf("parent %s does not exist", parentID)
		}
	}
	return nil
}

func (d *DAG) updateCumulativeWeights(node *store.Node, delta float64) error {
	if len(node.Parents) == 0 {
		return nil
	}

	ancestors := make(map[string]struct{})
	queue := make([]string, 0, len(node.Parents))
	for _, p := range node.Parents {
		if _, seen := ancestors[p]; !seen {
			ancestors[p] = struct{}{}
			queue = append(queue, p)
		}
	}

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		parent, err := d.getNodeInternal(current)
		if err != nil {
			d.logger.Errorf("Error fetching parent %s: %v", current, err)
			return fmt.Errorf("failed to fetch parent %s: %v", current, err)
		}
		if parent == nil {
			continue
		}

		for _, gp := range parent.Parents {
			if _, seen := ancestors[gp]; !seen {
				ancestors[gp] = struct{}{}
				queue = append(queue, gp)
			}
		}
	}

	for ancID := range ancestors {
		anc, err := d.getNodeInternal(ancID)
		if err != nil {
			d.logger.Errorf("Error fetching ancestor %s: %v", ancID, err)
			return fmt.Errorf("failed to fetch ancestor %s: %v", ancID, err)
		}
		if anc == nil {
			continue
		}

		anc.CumulativeWeight += delta
		if anc.CumulativeWeight < anc.Weight {
			anc.CumulativeWeight = anc.Weight
		}

		if err := d.store.AddNode(anc); err != nil {
			d.logger.Errorf("Failed to update ancestor %s: %v", ancID, err)
			return fmt.Errorf("failed to update ancestor %s: %v", ancID, err)
		}
	}

	return nil
}

func (d *DAG) SyncWithPeer(peerAddr string) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.logger.Infof("Syncing with peer: %s", peerAddr)

	client := &http.Client{
		Timeout: 5 * time.Second,
	}
	resp, err := client.Get(peerAddr + "/nodes")
	if err != nil {
		d.logger.Errorf("Failed to fetch nodes from peer %s: %v", peerAddr, err)
		return nil, fmt.Errorf("failed to fetch nodes from peer %s: %v", peerAddr, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		d.logger.Errorf("Peer %s returned status %d", peerAddr, resp.StatusCode)
		return nil, fmt.Errorf("peer %s returned status %d", peerAddr, resp.StatusCode)
	}

	var nodes []store.Node
	if err := json.NewDecoder(resp.Body).Decode(&nodes); err != nil {
		d.logger.Errorf("Failed to decode nodes from peer %s: %v", peerAddr, err)
		return nil, fmt.Errorf("failed to decode nodes: %v", err)
	}

	mergedNodes := []string{}
	for _, node := range nodes {
		existing, err := d.getNodeInternal(node.ID)
		if err != nil {
			d.logger.Errorf("Error checking node %s: %v", node.ID, err)
			continue
		}
		if existing != nil {
			d.logger.Debugf("Node %s already exists, skipping", node.ID)
			continue
		}

		if err := d.checkCycle(node.ID, node.Parents); err != nil {
			d.logger.Warnf("Cycle check failed for node %s from peer %s: %v", node.ID, peerAddr, err)
			continue
		}

		if d.maxParents > 0 && len(node.Parents) > d.maxParents {
			d.logger.Warnf("Node %s has too many parents: %d, max allowed: %d", node.ID, len(node.Parents), d.maxParents)
			continue
		}

		if node.Weight == 0 {
			node.Weight = d.defaultWeight
		}
		node.CumulativeWeight = node.Weight

		if err := d.store.AddNode(&node); err != nil {
			d.logger.Errorf("Failed to add node %s from peer %s: %v", node.ID, peerAddr, err)
			continue
		}
		d.logger.Infof("Node %s merged from peer %s with weight %f", node.ID, peerAddr, node.Weight)
		mergedNodes = append(mergedNodes, node.ID)

		if err := d.updateCumulativeWeights(&node, node.Weight); err != nil {
			d.logger.Errorf("Failed to update weights for node %s: %v", node.ID, err)
		}
	}

	if len(mergedNodes) == 0 {
		d.logger.Warnf("No new nodes merged from peer %s", peerAddr)
	} else {
		d.logger.Infof("Merged %d nodes from peer %s: %v", len(mergedNodes), peerAddr, mergedNodes)
	}
	return mergedNodes, nil
}

func (d *DAG) SelectTipsMCMC(maxTips int) ([]string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.selectTipsMCMCInternal(maxTips)
}

func (d *DAG) selectTipsMCMCInternal(maxTips int) ([]string, error) {
	if maxTips <= 0 {
		maxTips = d.maxParents
	}
	tips := make(map[string]struct{})
	maxAttempts := 10 * maxTips

	nodeCount := 0
	iter := d.store.Iterator()
	for iter.Next() {
		nodeCount++
	}
	iter.Release()
	if nodeCount == 0 {
		return nil, fmt.Errorf("no nodes in DAG")
	}
	maxWalkSteps := max(10, nodeCount/2)

	for len(tips) < maxTips && maxAttempts > 0 {
		startNode, err := d.getRandomNode()
		if err != nil {
			return nil, err
		}

		current := startNode
		for steps := 0; steps < maxWalkSteps; steps++ {
			isTip, err := d.isTipInternal(current.ID)
			if err != nil {
				return nil, err
			}
			if isTip {
				tips[current.ID] = struct{}{}
				break
			}

			children, err := d.getChildren(current.ID)
			if err != nil {
				return nil, err
			}
			if len(children) == 0 {
				tips[current.ID] = struct{}{}
				break
			}

			current = weightedRandomChoice(children)
		}
		maxAttempts--
	}

	if len(tips) == 0 {
		d.logger.Warnf("No tips found after %d attempts", maxAttempts)
		return nil, fmt.Errorf("no tips available")
	}

	result := make([]string, 0, len(tips))
	for id := range tips {
		result = append(result, id)
	}
	return result, nil
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (d *DAG) getRandomNode() (*store.Node, error) {
	iter := d.store.Iterator()
	defer iter.Release()

	count := 0
	keys := []string{}
	for iter.Next() {
		var node store.Node
		if err := json.Unmarshal(iter.Value(), &node); err != nil {
			continue
		}
		keys = append(keys, node.ID)
		count++
	}
	if count == 0 {
		return nil, fmt.Errorf("no nodes in DAG")
	}

	target := rand.Intn(count)
	return d.getNodeInternal(keys[target])
}

func (d *DAG) getChildren(parentID string) ([]*store.Node, error) {
	iter := d.store.Iterator()
	defer iter.Release()

	children := []*store.Node{}
	for iter.Next() {
		var node store.Node
		if err := json.Unmarshal(iter.Value(), &node); err != nil {
			return nil, err
		}
		for _, p := range node.Parents {
			if p == parentID {
				children = append(children, &node)
				break
			}
		}
	}
	return children, nil
}

func weightedRandomChoice(nodes []*store.Node) *store.Node {
	totalWeight := 0.0
	for _, n := range nodes {
		totalWeight += math.Max(n.CumulativeWeight, 0.0001)
	}

	r := rand.Float64() * totalWeight
	cumSum := 0.0
	for _, n := range nodes {
		cumSum += math.Max(n.CumulativeWeight, 0.0001)
		if r <= cumSum {
			return n
		}
	}

	return nodes[len(nodes)-1]
}

func (d *DAG) GetNode(id string) (*store.Node, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	d.logger.Infof("Fetching node: %s", id)
	return d.getNodeInternal(id)
}

func (d *DAG) getNodeInternal(id string) (*store.Node, error) {
	return d.store.GetNode(id)
}

func (d *DAG) IsTip(id string) (bool, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	return d.isTipInternal(id)
}

func (d *DAG) isTipInternal(id string) (bool, error) {
	iter := d.store.Iterator()
	defer iter.Release()

	for iter.Next() {
		var node store.Node
		if err := json.Unmarshal(iter.Value(), &node); err != nil {
			return false, err
		}
		for _, parent := range node.Parents {
			if parent == id {
				return false, nil
			}
		}
	}

	return true, nil
}

func (d *DAG) DeleteNode(id string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.logger.Infof("Deleting node: %s", id)

	node, err := d.getNodeInternal(id)
	if err != nil {
		return err
	}
	if node == nil {
		return fmt.Errorf("node with ID %s not found", id)
	}

	iter := d.store.Iterator()
	defer iter.Release()
	for iter.Next() {
		var n store.Node
		if err := json.Unmarshal(iter.Value(), &n); err != nil {
			return err
		}
		for _, parentID := range n.Parents {
			if parentID == id {
				return fmt.Errorf("cannot delete node %s because it has children", id)
			}
		}
	}

	if err := d.updateCumulativeWeights(node, -node.Weight); err != nil {
		d.logger.Errorf("Failed to update cumulative weights during delete: %v", err)
		return fmt.Errorf("failed to update weights: %v", err)
	}

	if err := d.store.DeleteNode(id); err != nil {
		return fmt.Errorf("failed to delete node: %v", err)
	}

	return nil
}

func (d *DAG) Logger() *logrus.Logger {
	return d.logger
}
