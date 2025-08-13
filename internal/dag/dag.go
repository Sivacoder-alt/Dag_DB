package dag

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"

	"github.com/sirupsen/logrus"
	"github.com/sivaram/dag-leveldb/internal/store"
)

type DAG struct {
	store  *store.Store
	logger *logrus.Logger
	maxParents int
}

func New(store *store.Store, logger *logrus.Logger, maxParents int) *DAG {
	if maxParents <= 0 {
		maxParents = 2
	}
	return &DAG{store: store, logger: logger,maxParents: maxParents}
}

func (d *DAG) AddNode(node *store.Node) error {
	d.logger.Infof("Adding node: %s", node.ID)

	// Check if node exists
	existingNode, err := d.store.GetNode(node.ID)
	if err != nil {
		d.logger.Errorf("Error checking for existing node: %v", err)
		return err
	}
	if existingNode != nil {
		d.logger.Warnf("Node with ID %s already exists", node.ID)
		return fmt.Errorf("node with ID %s already exists", node.ID)
	}

	// Auto-select parents via MCMC if not provided
	if len(node.Parents) == 0 {
		selectedTips, err := d.SelectTipsMCMC(2)
		if err != nil {
			d.logger.Warnf("Failed to select tips via MCMC: %v", err)
			// Allow adding genesis nodes (no parents) if DAG is empty
			if err.Error() != "no nodes in DAG" {
				return fmt.Errorf("failed to select parents: %v", err)
			}
		} else {
			node.Parents = selectedTips
			d.logger.Infof("Auto-selected parents (MCMC) for %s: %v", node.ID, node.Parents)
		}
	}

	// Default weight if not set
	if node.Weight == 0 {
		node.Weight = 1.0
	}
	node.CumulativeWeight = node.Weight

	// Save the node first
	if err := d.store.AddNode(node); err != nil {
		d.logger.Errorf("Failed to add node %s: %v", node.ID, err)
		return err
	}

	d.logger.Infof("Node %s added with weight %f", node.ID, node.Weight)

	// Propagate cumulative weight to parents
	for _, parentID := range node.Parents {
		d.logger.Infof("Propagating weight to parent %s", parentID)
		if err := d.incrementParentWeight(parentID, node.Weight); err != nil {
			d.logger.Errorf("Failed to update parent weight for %s: %v", parentID, err)
		}
	}

	return nil
}

func (d *DAG) incrementParentWeight(parentID string, weight float64) error {
	parent, err := d.store.GetNode(parentID)
	if err != nil {
		d.logger.Errorf("Failed to get parent node %s: %v", parentID, err)
		return err
	}
	if parent == nil {
		d.logger.Warnf("Parent node %s not found", parentID)
		return nil
	}

	parent.CumulativeWeight += weight
	d.logger.Infof("Incremented cumulative weight of node %s to %f", parent.ID, parent.CumulativeWeight)

	if err := d.store.AddNode(parent); err != nil {
		d.logger.Errorf("Failed to update parent node %s: %v", parent.ID, err)
		return err
	}

	// Recursively update parents of this parent
	for _, grandParentID := range parent.Parents {
		if err := d.incrementParentWeight(grandParentID, weight); err != nil {
			return err
		}
	}

	return nil
}

func (d *DAG) SelectTipsMCMC(maxTips int) ([]string, error) {
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
	maxWalkSteps := max(10, nodeCount*2) 

	for len(tips) < maxTips && maxAttempts > 0 {
		startNode, err := d.getRandomNode()
		if err != nil {
			return nil, err 
		}

		current := startNode
		for steps := 0; steps < maxWalkSteps; steps++ {
			isTip, err := d.IsTip(current.ID)
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
	for iter.Next() {
		count++
	}
	if count == 0 {
		return nil, fmt.Errorf("no nodes in DAG")
	}

	target := rand.Intn(count)
	iter = d.store.Iterator() 
	defer iter.Release()

	for i := 0; i <= target && iter.Next(); i++ {
		if i == target {
			var node store.Node
			if err := json.Unmarshal(iter.Value(), &node); err != nil {
				return nil, err
			}
			return &node, nil
		}
	}
	return nil, fmt.Errorf("failed to select random node")
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
	d.logger.Infof("Fetching node: %s", id)
	return d.store.GetNode(id)
}

func (d *DAG) IsTip(id string) (bool, error) {
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
	d.logger.Infof("Deleting node: %s", id)

	node, err := d.store.GetNode(id)
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

	if err := d.store.DeleteNode(id); err != nil {
		return err
	}

	for _, parentID := range node.Parents {
		if err := d.decrementParentWeight(parentID, node.Weight); err != nil {
			d.logger.Errorf("Failed to decrement weight for parent %s: %v", parentID, err)
		}
	}

	return nil
}

func (d *DAG) decrementParentWeight(parentID string, weight float64) error {
	parent, err := d.store.GetNode(parentID)
	if err != nil {
		return err
	}
	if parent == nil {
		return nil
	}

	parent.CumulativeWeight -= weight
	if parent.CumulativeWeight < 0 {
		parent.CumulativeWeight = 0
	}

	if err := d.store.AddNode(parent); err != nil {
		return err
	}

	// Recursive up the DAG
	for _, grandParentID := range parent.Parents {
		if err := d.decrementParentWeight(grandParentID, weight); err != nil {
			return err
		}
	}

	return nil
}
