package store

import (
	"bytes"
	"encoding/gob"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/iterator"
	"github.com/syndtr/goleveldb/leveldb/opt"
)

type Store struct {
	db              *leveldb.DB
	nodeCache       map[string]*Node
	checkpointCache map[string]*Checkpoint
	cacheMu         sync.RWMutex
	maxCacheSize    int
	logger          *logrus.Logger
}

type Node struct {
	ID               string   `gob:"id"`
	Data             string   `gob:"data"`
	Parents          []string `gob:"parents"`
	Weight           float64  `gob:"weight"`
	CumulativeWeight float64  `gob:"cumulative_weight"`
}

type Checkpoint struct {
	ID        string   `gob:"id"`
	NodeIDs   []string `gob:"node_ids"`
	Timestamp int64    `gob:"timestamp"`
}

const (
	latestCheckpointKey = "latest_checkpoint"
	finalizedIndexKey   = "finalized_nodes"
	defaultMaxCacheSize = 1000
)

func New(path string, logger *logrus.Logger) (*Store, error) {
	db, err := leveldb.OpenFile(filepath.Clean(path), &opt.Options{
		Compression:        opt.NoCompression,
		BlockCacheCapacity: 8 * opt.MiB,
		WriteBuffer:        4 * opt.MiB,
	})
	if err != nil {
		return nil, err
	}
	return &Store{
		db:              db,
		nodeCache:       make(map[string]*Node),
		checkpointCache: make(map[string]*Checkpoint),
		maxCacheSize:    defaultMaxCacheSize,
		logger:          logger,
	}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) AddNode(node *Node) error {
	start := time.Now()
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(node); err != nil {
		s.logger.Errorf("Failed to encode node %s: %v", node.ID, err)
		return fmt.Errorf("failed to encode node: %v", err)
	}
	if err := s.db.Put([]byte(node.ID), buf.Bytes(), nil); err != nil {
		s.logger.Errorf("Failed to store node %s in LevelDB: %v", node.ID, err)
		return fmt.Errorf("failed to store node: %v", err)
	}
	s.cacheMu.Lock()
	if len(s.nodeCache) >= s.maxCacheSize {
		for k := range s.nodeCache {
			delete(s.nodeCache, k)
			break
		}
	}
	s.nodeCache[node.ID] = node
	s.cacheMu.Unlock()
	s.logger.Debugf("AddNode %s took %v", node.ID, time.Since(start))
	return nil
}

func (s *Store) GetNode(id string) (*Node, error) {
	start := time.Now()
	s.cacheMu.RLock()
	if node, exists := s.nodeCache[id]; exists {
		s.cacheMu.RUnlock()
		s.logger.Debugf("GetNode %s from cache took %v", id, time.Since(start))
		return node, nil
	}
	s.cacheMu.RUnlock()

	data, err := s.db.Get([]byte(id), nil)
	if err != nil {
		if errors.Is(err, leveldb.ErrNotFound) {
			s.logger.Debugf("Node %s not found in LevelDB", id)
			return nil, nil
		}
		s.logger.Errorf("Failed to get node %s from LevelDB: %v", id, err)
		return nil, fmt.Errorf("failed to get node: %v", err)
	}
	var node Node
	dec := gob.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&node); err != nil {
		s.logger.Errorf("Failed to decode node %s: %v", id, err)
		return nil, fmt.Errorf("failed to decode node: %v", err)
	}
	s.cacheMu.Lock()
	if len(s.nodeCache) >= s.maxCacheSize {
		for k := range s.nodeCache {
			delete(s.nodeCache, k)
			break
		}
	}
	s.nodeCache[id] = &node
	s.cacheMu.Unlock()
	s.logger.Debugf("GetNode %s from LevelDB took %v", id, time.Since(start))
	return &node, nil
}

func (s *Store) Iterator() iterator.Iterator {
	return s.db.NewIterator(nil, nil)
}

func (s *Store) DeleteNode(id string) error {
	start := time.Now()
	if err := s.db.Delete([]byte(id), nil); err != nil {
		s.logger.Errorf("Failed to delete node %s: %v", id, err)
		return fmt.Errorf("failed to delete node: %v", err)
	}
	s.cacheMu.Lock()
	delete(s.nodeCache, id)
	s.cacheMu.Unlock()
	s.logger.Debugf("DeleteNode %s took %v", id, time.Since(start))
	return nil
}

func (s *Store) AddCheckpoint(checkpoint *Checkpoint) error {
	start := time.Now()
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(checkpoint); err != nil {
		s.logger.Errorf("Failed to encode checkpoint %s: %v", checkpoint.ID, err)
		return fmt.Errorf("failed to encode checkpoint: %v", err)
	}
	batch := new(leveldb.Batch)
	batch.Put([]byte("checkpoint:"+checkpoint.ID), buf.Bytes())
	batch.Put([]byte(latestCheckpointKey), []byte(checkpoint.ID))
	if err := s.db.Write(batch, nil); err != nil {
		s.logger.Errorf("Failed to store checkpoint %s: %v", checkpoint.ID, err)
		return fmt.Errorf("failed to store checkpoint: %v", err)
	}
	s.cacheMu.Lock()
	if len(s.checkpointCache) >= s.maxCacheSize {
		for k := range s.checkpointCache {
			delete(s.checkpointCache, k)
			break
		}
	}
	s.checkpointCache[checkpoint.ID] = checkpoint
	s.cacheMu.Unlock()
	s.logger.Debugf("AddCheckpoint %s took %v", checkpoint.ID, time.Since(start))
	return nil
}

func (s *Store) GetCheckpoint(id string) (*Checkpoint, error) {
	start := time.Now()
	s.cacheMu.RLock()
	if checkpoint, exists := s.checkpointCache[id]; exists {
		s.cacheMu.RUnlock()
		s.logger.Debugf("GetCheckpoint %s from cache took %v", id, time.Since(start))
		return checkpoint, nil
	}
	s.cacheMu.RUnlock()

	data, err := s.db.Get([]byte("checkpoint:"+id), nil)
	if err != nil {
		if errors.Is(err, leveldb.ErrNotFound) {
			s.logger.Debugf("Checkpoint %s not found in LevelDB", id)
			return nil, nil
		}
		s.logger.Errorf("Failed to get checkpoint %s from LevelDB: %v", id, err)
		return nil, fmt.Errorf("failed to get checkpoint: %v", err)
	}
	var checkpoint Checkpoint
	dec := gob.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&checkpoint); err != nil {
		s.logger.Errorf("Failed to decode checkpoint %s: %v", id, err)
		return nil, fmt.Errorf("failed to decode checkpoint: %v", err)
	}
	s.cacheMu.Lock()
	if len(s.checkpointCache) >= s.maxCacheSize {
		for k := range s.checkpointCache {
			delete(s.checkpointCache, k)
			break
		}
	}
	s.checkpointCache[id] = &checkpoint
	s.cacheMu.Unlock()
	s.logger.Debugf("GetCheckpoint %s from LevelDB took %v", id, time.Since(start))
	return &checkpoint, nil
}

func (s *Store) GetLatestCheckpoint() (*Checkpoint, error) {
	start := time.Now()
	idData, err := s.db.Get([]byte(latestCheckpointKey), nil)
	if err != nil {
		if errors.Is(err, leveldb.ErrNotFound) {
			s.logger.Debugf("No latest checkpoint found")
			return nil, nil
		}
		s.logger.Errorf("Failed to get latest checkpoint key: %v", err)
		return nil, fmt.Errorf("failed to get latest checkpoint key: %v", err)
	}
	checkpoint, err := s.GetCheckpoint(string(idData))
	if err != nil {
		s.logger.Errorf("Failed to get checkpoint %s: %v", string(idData), err)
		return nil, err
	}
	s.logger.Debugf("GetLatestCheckpoint took %v", time.Since(start))
	return checkpoint, nil
}

func (s *Store) WriteBatch(batch *leveldb.Batch) error {
	start := time.Now()
	if err := s.db.Write(batch, nil); err != nil {
		s.logger.Errorf("Failed to write batch: %v", err)
		return fmt.Errorf("failed to write batch: %v", err)
	}
	s.logger.Debugf("WriteBatch took %v", time.Since(start))
	return nil
}

func (s *Store) UpdateFinalizedIndex(node *Node, threshold float64, add bool) error {
	start := time.Now()
	data, err := s.db.Get([]byte(finalizedIndexKey), nil)
	finalized := make(map[string]struct{})
	if err == nil {
		dec := gob.NewDecoder(bytes.NewReader(data))
		if err := dec.Decode(&finalized); err != nil {
			s.logger.Errorf("Failed to decode finalized index: %v", err)
			return fmt.Errorf("failed to decode finalized index: %v", err)
		}
	} else if !errors.Is(err, leveldb.ErrNotFound) {
		s.logger.Errorf("Failed to get finalized index: %v", err)
		return fmt.Errorf("failed to get finalized index: %v", err)
	}

	if add && node.CumulativeWeight >= threshold {
			finalized[node.ID] = struct{}{}
	} else {
		delete(finalized, node.ID)
	}

	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	if err := enc.Encode(finalized); err != nil {
		s.logger.Errorf("Failed to encode finalized index: %v", err)
		return fmt.Errorf("failed to encode finalized index: %v", err)
	}
	if err := s.db.Put([]byte(finalizedIndexKey), buf.Bytes(), nil); err != nil {
		s.logger.Errorf("Failed to store finalized index: %v", err)
		return fmt.Errorf("failed to store finalized index: %v", err)
	}
	s.logger.Debugf("UpdateFinalizedIndex for node %s took %v", node.ID, time.Since(start))
	return nil
}

func (s *Store) GetFinalizedNodes(threshold float64) ([]Node, error) {
	start := time.Now()
	data, err := s.db.Get([]byte(finalizedIndexKey), nil)
	if err != nil {
		if errors.Is(err, leveldb.ErrNotFound) {
			s.logger.Debugf("No finalized nodes found")
			return nil, nil
		}
		s.logger.Errorf("Failed to get finalized index: %v", err)
		return nil, fmt.Errorf("failed to get finalized index: %v", err)
	}
	var finalized map[string]struct{}
	dec := gob.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&finalized); err != nil {
		s.logger.Errorf("Failed to decode finalized index: %v", err)
		return nil, fmt.Errorf("failed to decode finalized index: %v", err)
	}

	nodes := make([]Node, 0, len(finalized))
	for id := range finalized {
		node, err := s.GetNode(id)
		if err != nil {
			s.logger.Errorf("Failed to get node %s: %v", id, err)
			continue
		}
		if node == nil {
			s.logger.Warnf("Node %s in finalized index not found", id)
			continue
		}
		if node.CumulativeWeight >= threshold {
			nodes = append(nodes, *node)
		}
	}
	s.logger.Debugf("GetFinalizedNodes took %v", time.Since(start))
	return nodes, nil
}
