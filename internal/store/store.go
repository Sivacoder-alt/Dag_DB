package store

import (
	"encoding/json"
	"errors"
	"path/filepath"

	"github.com/syndtr/goleveldb/leveldb"
	"github.com/syndtr/goleveldb/leveldb/iterator"
)

type Store struct {
	db *leveldb.DB
}

type Node struct {
	ID               string   `json:"id"`
	Data             string   `json:"data"`
	Parents          []string `json:"parents"`
	Weight           float64  `json:"weight"`
	CumulativeWeight float64  `json:"cumulative_weight"`
}

func New(path string) (*Store, error) {
	db, err := leveldb.OpenFile(filepath.Clean(path), nil)
	if err != nil {
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) AddNode(node *Node) error {
	data, err := json.Marshal(node)
	if err != nil {
		return err
	}
	return s.db.Put([]byte(node.ID), data, nil)
}

func (s *Store) GetNode(id string) (*Node, error) {
	data, err := s.db.Get([]byte(id), nil)
	if err != nil {
		if errors.Is(err, leveldb.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	var node Node
	if err := json.Unmarshal(data, &node); err != nil {
		return nil, err
	}
	return &node, nil
}

func (s *Store) Iterator() iterator.Iterator {
	return s.db.NewIterator(nil, nil)
}


func (s *Store) DeleteNode(id string) error {
	return s.db.Delete([]byte(id), nil)
}
