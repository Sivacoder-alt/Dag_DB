package dag

import (
	"encoding/json"
	"fmt"

	"github.com/sirupsen/logrus"
	"github.com/sivaram/dag-leveldb/internal/store"
)

type DAG struct {
	store  *store.Store
	logger *logrus.Logger
}

func New(store *store.Store, logger *logrus.Logger) *DAG {
	return &DAG{store: store, logger: logger}
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



func (d *DAG) GetNode(id string) (*store.Node, error) {
	d.logger.Infof("Fetching node: %s", id)
	return d.store.GetNode(id)
}

func (d *DAG) IsTip(id string) (bool, error) {
    // Iterate through all nodes to check if any node has the given ID as a parent
    iter := d.store.Iterator()
    defer iter.Release()

    for iter.Next() {
        var node store.Node
        if err := json.Unmarshal(iter.Value(), &node); err != nil {
            return false, err
        }
        for _, parent := range node.Parents {
            if parent == id {
                // If the node is found as a parent, it is not a tip
                return false, nil
            }
        }
    }

    // If no node has the given ID as a parent, it is a tip
    return true, nil
}


func (d *DAG) DeleteNode(id string) error {
	d.logger.Infof("Deleting node: %s", id)

	// Check if node exists
	node, err := d.store.GetNode(id)
	if err != nil {
		return err
	}
	if node == nil {
		return fmt.Errorf("node with ID %s not found", id)
	}

	// Check if node has children (other nodes with this as parent)
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

	// Delete node from store
	if err := d.store.DeleteNode(id); err != nil {
		return err
	}

	// Subtract this node’s weight from parents’ cumulative weight recursively
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
