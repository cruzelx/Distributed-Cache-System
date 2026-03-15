package main

import (
	"fmt"
	"hash/crc32"
	"sort"
	"sync"
)

type HashRing struct {
	mu         sync.RWMutex
	sortedHash []uint32
	hashmap    map[uint32]string
	replica    int
}

func NewHashRing(replica int) *HashRing {
	return &HashRing{
		sortedHash: []uint32{},
		hashmap:    make(map[uint32]string),
		replica:    replica,
	}
}

func (hr *HashRing) AddNode(node string) {
	hr.mu.Lock()
	defer hr.mu.Unlock()

	for i := 0; i < hr.replica; i++ {
		replicaKey := fmt.Sprintf("%s:%d", node, i)
		hash := crc32.ChecksumIEEE([]byte(replicaKey))
		hr.hashmap[hash] = node
		hr.sortedHash = append(hr.sortedHash, hash)
	}

	sort.Slice(hr.sortedHash, func(i, j int) bool {
		return hr.sortedHash[i] < hr.sortedHash[j]
	})
}

func (hr *HashRing) RemoveNode(node string) {
	hr.mu.Lock()
	defer hr.mu.Unlock()

	var modifiedSortedHash []uint32

	for _, hash := range hr.sortedHash {
		if hr.hashmap[hash] != node {
			modifiedSortedHash = append(modifiedSortedHash, hash)
		} else {
			delete(hr.hashmap, hash)
		}
	}
	hr.sortedHash = modifiedSortedHash
}

func (hr *HashRing) GetNode(key string) (string, error) {
	nodes, err := hr.GetNodes(key, 1)
	if err != nil {
		return "", err
	}
	return nodes[0], nil
}

// GetNodes returns up to n distinct physical nodes for key, starting from
// its position on the ring and walking clockwise. If fewer than n distinct
// nodes exist, all available nodes are returned.
func (hr *HashRing) GetNodes(key string, n int) ([]string, error) {
	hr.mu.RLock()
	defer hr.mu.RUnlock()

	if len(hr.sortedHash) == 0 {
		return nil, fmt.Errorf("hash ring is empty")
	}

	hash := crc32.ChecksumIEEE([]byte(key))
	start := sort.Search(len(hr.sortedHash), func(i int) bool {
		return hr.sortedHash[i] >= hash
	})
	if start == len(hr.sortedHash) {
		start = 0
	}

	seen := make(map[string]bool)
	nodes := make([]string, 0, n)
	for i := 0; i < len(hr.sortedHash) && len(nodes) < n; i++ {
		idx := (start + i) % len(hr.sortedHash)
		node := hr.hashmap[hr.sortedHash[idx]]
		if !seen[node] {
			seen[node] = true
			nodes = append(nodes, node)
		}
	}
	return nodes, nil
}
