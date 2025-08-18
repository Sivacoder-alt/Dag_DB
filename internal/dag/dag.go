package dag

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/sivaram/dag-leveldb/internal/store"
	"github.com/syndtr/goleveldb/leveldb"
)

type DAG struct {
	store                 *store.Store
	logger                *logrus.Logger
	maxParents            int
	defaultWeight         float64
	checkpointInterval    int64   // Interval for creating checkpoints
	confirmationThreshold float64 // Weight threshold for finality
	mu                    sync.RWMutex
}

func New(store *store.Store, logger *logrus.Logger, maxParents int, defaultWeight float64, checkpointInterval int64, confirmationThreshold float64) *DAG {
	if maxParents <= 0 {
		maxParents = 2
	}
	if defaultWeight <= 0 {
		defaultWeight = 1.0
	}
	if checkpointInterval <= 0 {
		checkpointInterval = 300
	}
	if confirmationThreshold <= 0 {
		confirmationThreshold = 10.0
	}
	return &DAG{
		store:                 store,
		logger:                logger,
		maxParents:            maxParents,
		defaultWeight:         defaultWeight,
		checkpointInterval:    checkpointInterval,
		confirmationThreshold: confirmationThreshold,
	}
}

func (d *DAG) AddNode(node *store.Node) error {
	
	start := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()

	d.logger.Infof("Adding node: %s", node.ID)

	// Check existing node
	checkStart := time.Now()
	existingNode, err := d.getNodeInternal(node.ID)
	if err != nil {
		d.logger.Errorf("Error checking for existing node %s: %v", node.ID, err)
		return fmt.Errorf("failed to check existing node: %v", err)
	}
	if existingNode != nil {
		d.logger.Warnf("Node with ID %s already exists", node.ID)
		return fmt.Errorf("node with ID %s already exists", node.ID)
	}
	d.logger.Debugf("Check existing node %s took %v", node.ID, time.Since(checkStart))

	// Select tips if no parents provided
	if node.Parents == nil {
		selectTipsStart := time.Now()
		selectedTips, err := d.selectTipsMCMCInternal(2)
		if err != nil {
			d.logger.Warnf("Failed to select tips via MCMC: %v", err)
			if err.Error() != "no nodes in DAG" {
				return fmt.Errorf("failed to select parents: %v", err)
			}
			node.Parents = []string{}
		} else {
			node.Parents = selectedTips
			d.logger.Infof("Auto-selected parents (MCMC) for %s: %v", node.ID, node.Parents)
		}
		d.logger.Debugf("Select tips for node %s took %v", node.ID, time.Since(selectTipsStart))
	}

	if d.maxParents > 0 && len(node.Parents) > d.maxParents {
		return fmt.Errorf("node %s has too many parents: %d, max allowed: %d", node.ID, len(node.Parents), d.maxParents)
	}

	// Check for cycles
	cycleCheckStart := time.Now()
	if err := d.checkCycle(node.ID, node.Parents); err != nil {
		d.logger.Warnf("Cycle check failed for node %s: %v", node.ID, err)
		return err
	}
	d.logger.Debugf("Cycle check for node %s took %v", node.ID, time.Since(cycleCheckStart))

	// Set weight
	if node.Weight == 0 {
		node.Weight = d.defaultWeight
	}
	node.CumulativeWeight = node.Weight

	// Store node
	storeStart := time.Now()
	if err := d.store.AddNode(node); err != nil {
		d.logger.Errorf("Failed to store node %s: %v", node.ID, err)
		return fmt.Errorf("failed to store node: %v", err)
	}
	d.logger.Debugf("Storing node %s took %v", node.ID, time.Since(storeStart))

	d.logger.Infof("Node %s added with weight %f", node.ID, node.Weight)

	// Update cumulative weights
	updateWeightStart := time.Now()
	if err := d.updateCumulativeWeights(node, node.Weight); err != nil {
		d.logger.Errorf("Failed to update cumulative weights for node %s: %v", node.ID, err)
		return fmt.Errorf("failed to update weights: %v", err)
	}
	d.logger.Debugf("Updating cumulative weights for node %s took %v", node.ID, time.Since(updateWeightStart))

	d.logger.Infof("AddNode for %s completed in %v", node.ID, time.Since(start))
	return nil
}

func (d *DAG) updateCumulativeWeights(node *store.Node, delta float64) error {
	start := time.Now()
	if len(node.Parents) == 0 {
		return nil
	}

	// Collect ancestors
	ancestors := make(map[string]*store.Node)
	queue := make([]string, 0, len(node.Parents))
	for _, p := range node.Parents {
		ancestors[p] = nil
		queue = append(queue, p)
	}

	// Fetch ancestors from cache or database
	fetchStart := time.Now()
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if _, exists := ancestors[current]; !exists {
			continue
		}

		parent, err := d.getNodeInternal(current)
		if err != nil {
			d.logger.Errorf("Failed to fetch ancestor %s: %v", current, err)
			continue
		}
		if parent == nil {
			continue
		}
		ancestors[current] = parent

		for _, gp := range parent.Parents {
			if _, seen := ancestors[gp]; !seen {
				ancestors[gp] = nil
				queue = append(queue, gp)
			}
		}
	}
	d.logger.Debugf("Fetching ancestors took %v", time.Since(fetchStart))

	// Batch update weights
	batch := new(leveldb.Batch)
	updateStart := time.Now()
	for ancID, anc := range ancestors {
		if anc == nil {
			continue
		}
		anc.CumulativeWeight += delta
		if anc.CumulativeWeight < anc.Weight {
			anc.CumulativeWeight = anc.Weight
		}
		data, err := json.Marshal(anc)
		if err != nil {
			d.logger.Errorf("Failed to marshal ancestor %s: %v", ancID, err)
			continue
		}
		batch.Put([]byte(ancID), data)
	}
	d.logger.Debugf("Preparing batch updates took %v", time.Since(updateStart))

	// Write batch
	batchWriteStart := time.Now()
	if err := d.store.WriteBatch(batch); err != nil {
		d.logger.Errorf("Failed to write batch for ancestors: %v", err)
		return fmt.Errorf("failed to update ancestors: %v", err)
	}
	d.logger.Debugf("Batch write for ancestors took %v", time.Since(batchWriteStart))

	d.logger.Debugf("updateCumulativeWeights for node %s took %v", node.ID, time.Since(start))
	return nil
}

func (d *DAG) checkCycle(nodeID string, parents []string) error {
	start := time.Now()
	for _, parentID := range parents {
		if parentID == nodeID {
			return fmt.Errorf("cycle detected: node %s cannot be its own parent", nodeID)
		}
		checkStart := time.Now()
		p, err := d.getNodeInternal(parentID)
		if err != nil {
			d.logger.Errorf("Error checking parent %s: %v", parentID, err)
			return fmt.Errorf("failed to check parent %s: %v", parentID, err)
		}
		d.logger.Debugf("Checking parent %s took %v", parentID, time.Since(checkStart))
		if p == nil {
			return fmt.Errorf("parent %s does not exist", parentID)
		}
	}
	d.logger.Debugf("checkCycle for node %s took %v", nodeID, time.Since(start))
	return nil
}

func (d *DAG) selectTipsMCMCInternal(maxTips int) ([]string, error) {
	start := time.Now()
	if maxTips <= 0 {
		maxTips = d.maxParents
	}
	tips := make(map[string]struct{})
	maxAttempts := 10 * maxTips

	nodeCount := 0
	countStart := time.Now()
	iter := d.store.Iterator()
	for iter.Next() {
		if strings.HasPrefix(string(iter.Key()), "checkpoint:") {
			continue
		}
		nodeCount++
	}
	iter.Release()
	d.logger.Debugf("Counting nodes took %v", time.Since(countStart))
	if nodeCount == 0 {
		return nil, fmt.Errorf("no nodes in DAG")
	}
	maxWalkSteps := max(10, nodeCount/2)

	for len(tips) < maxTips && maxAttempts > 0 {
		randStart := time.Now()
		startNode, err := d.getRandomNode()
		if err != nil {
			return nil, err
		}
		d.logger.Debugf("Getting random node took %v", time.Since(randStart))

		current := startNode
		for steps := 0; steps < maxWalkSteps; steps++ {
			tipCheckStart := time.Now()
			isTip, err := d.isTipInternal(current.ID)
			if err != nil {
				return nil, err
			}
			d.logger.Debugf("Checking if %s is tip took %v", current.ID, time.Since(tipCheckStart))
			if isTip {
				tips[current.ID] = struct{}{}
				break
			}

			childStart := time.Now()
			children, err := d.getChildren(current.ID)
			if err != nil {
				return nil, err
			}
			d.logger.Debugf("Getting children for %s took %v", current.ID, time.Since(childStart))
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
	d.logger.Debugf("selectTipsMCMCInternal took %v", time.Since(start))
	return result, nil
}

func (d *DAG) CreateCheckpoint() (*store.Checkpoint, error) {
	start := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()

	d.logger.Infof("Creating new checkpoint")

	fetchStart := time.Now()
	nodes, _ := d.store.GetFinalizedNodes(d.confirmationThreshold)
	d.logger.Infof("Candidate nodes for checkpoint: %d", len(nodes))
	for _, n := range nodes {
		d.logger.Infof("Node %s has weight %d", n.ID, n.CumulativeWeight)
	}
	d.logger.Debugf("Fetching finalized nodes took %v", time.Since(fetchStart))

	finalizedNodes := []string{}
	for _, node := range nodes {
		finalizedNodes = append(finalizedNodes, node.ID)
	}

	if len(finalizedNodes) == 0 {
		d.logger.Warnf("No nodes meet finality threshold for checkpoint")
		return nil, nil
	}

	checkpoint := &store.Checkpoint{
		ID:        fmt.Sprintf("%d", time.Now().UnixNano()),
		NodeIDs:   finalizedNodes,
		Timestamp: time.Now().Unix(),
	}

	storeStart := time.Now()
	if err := d.store.AddCheckpoint(checkpoint); err != nil {
		d.logger.Errorf("Failed to store checkpoint %s: %v", checkpoint.ID, err)
		return nil, fmt.Errorf("failed to store checkpoint: %v", err)
	}
	d.logger.Debugf("Storing checkpoint %s took %v", checkpoint.ID, time.Since(storeStart))

	d.logger.Infof("Checkpoint %s created with %d finalized nodes in %v", checkpoint.ID, len(finalizedNodes), time.Since(start))
	return checkpoint, nil
}

func (d *DAG) GetAllNodes() ([]store.Node, error) {
	start := time.Now()
	d.mu.RLock()
	defer d.mu.RUnlock()

	nodes := []store.Node{}
	iter := d.store.Iterator()
	defer iter.Release()
	for iter.Next() {
		key := string(iter.Key())
		if strings.HasPrefix(key, "checkpoint:") {
			continue
		}
		var node store.Node
		if err := json.Unmarshal(iter.Value(), &node); err != nil {
			d.logger.Errorf("Failed to unmarshal node: %v", err)
			continue
		}
		nodes = append(nodes, node)
	}
	d.logger.Debugf("GetAllNodes took %v", time.Since(start))
	return nodes, nil
}

func (d *DAG) PruneBeforeCheckpoint() error {
	start := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()

	checkpoint, err := d.store.GetLatestCheckpoint()
	if err != nil {
		d.logger.Errorf("Failed to fetch latest checkpoint: %v", err)
		return fmt.Errorf("failed to fetch latest checkpoint: %v", err)
	}
	if checkpoint == nil {
		d.logger.Warnf("No checkpoint found for pruning")
		return nil
	}

	nodes, err := d.GetAllNodes()
	if err != nil {
		d.logger.Errorf("Failed to fetch nodes: %v", err)
		return fmt.Errorf("failed to fetch nodes: %v", err)
	}

	finalizedSet := make(map[string]struct{})
	for _, id := range checkpoint.NodeIDs {
		finalizedSet[id] = struct{}{}
	}

	for _, node := range nodes {
		if _, finalized := finalizedSet[node.ID]; finalized {
			continue
		}
		isReferenced := false
		for _, n := range nodes {
			if _, fin := finalizedSet[n.ID]; fin {
				continue
			}
			for _, parent := range n.Parents {
				if parent == node.ID {
					isReferenced = true
					break
				}
			}
			if isReferenced {
				break
			}
		}
		if isReferenced {
			continue
		}

		if err := d.store.DeleteNode(node.ID); err != nil {
			d.logger.Errorf("Failed to prune node %s: %v", node.ID, err)
			continue
		}
		d.logger.Infof("Pruned node %s before checkpoint %s", node.ID, checkpoint.ID)
	}

	d.logger.Debugf("PruneBeforeCheckpoint took %v", time.Since(start))
	return nil
}

func (d *DAG) GetLatestCheckpoint() (*store.Checkpoint, error) {
	start := time.Now()
	d.mu.RLock()
	defer d.mu.RUnlock()

	d.logger.Infof("Fetching latest checkpoint")
	checkpoint, err := d.store.GetLatestCheckpoint()
	d.logger.Debugf("GetLatestCheckpoint took %v", time.Since(start))
	return checkpoint, err
}

func (d *DAG) SyncWithPeer(peerAddr string) ([]string, error) {
	start := time.Now()
	d.mu.Lock()
	defer d.mu.Unlock()

	d.logger.Infof("Syncing with peer: %s", peerAddr)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(peerAddr + "/checkpoints/latest")
	if err != nil {
		d.logger.Warnf("Failed to fetch checkpoint from peer %s: %v", peerAddr, err)
	} else {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			var checkpoint store.Checkpoint
			if err := json.NewDecoder(resp.Body).Decode(&checkpoint); err != nil {
				d.logger.Errorf("Failed to decode checkpoint from peer %s: %v", peerAddr, err)
			} else {
				existing, err := d.store.GetCheckpoint(checkpoint.ID)
				if err != nil {
					d.logger.Errorf("Error checking checkpoint %s: %v", checkpoint.ID, err)
				} else if existing == nil {
					if err := d.store.AddCheckpoint(&checkpoint); err != nil {
						d.logger.Errorf("Failed to store checkpoint %s from peer %s: %v", checkpoint.ID, peerAddr, err)
					} else {
						d.logger.Infof("Synced checkpoint %s from peer %s", checkpoint.ID, peerAddr)
					}
				}
			}
		}
	}

	resp, err = client.Get(peerAddr + "/nodes")
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
	latestCheckpoint, err := d.store.GetLatestCheckpoint()
	finalizedSet := make(map[string]struct{})
	if latestCheckpoint != nil {
		for _, id := range latestCheckpoint.NodeIDs {
			finalizedSet[id] = struct{}{}
		}
	}

	for _, node := range nodes {
		if _, finalized := finalizedSet[node.ID]; finalized {
			continue
		}

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
		d.logger.Infof("Merged %d nodes from peer %s: %v in %v", len(mergedNodes), peerAddr, mergedNodes, time.Since(start))
	}
	return mergedNodes, nil
}

func (d *DAG) SelectTipsMCMC(maxTips int) ([]string, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.selectTipsMCMCInternal(maxTips)
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
		if strings.HasPrefix(string(iter.Key()), "checkpoint:") {
			continue
		}
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
		if strings.HasPrefix(string(iter.Key()), "checkpoint:") {
			continue
		}
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
		if strings.HasPrefix(string(iter.Key()), "checkpoint:") {
			continue
		}
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
	start := time.Now()
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
		if strings.HasPrefix(string(iter.Key()), "checkpoint:") {
			continue
		}
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

	latestCheckpoint, err := d.store.GetLatestCheckpoint()
	if err != nil {
		return fmt.Errorf("failed to check checkpoint: %v", err)
	}
	if latestCheckpoint != nil {
		for _, nodeID := range latestCheckpoint.NodeIDs {
			if nodeID == id {
				return fmt.Errorf("cannot delete node %s because it is part of checkpoint %s", id, latestCheckpoint.ID)
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

	d.logger.Debugf("DeleteNode for %s took %v", id, time.Since(start))
	return nil
}

func (d *DAG) Logger() *logrus.Logger {
	return d.logger
}
