package main

import (
	"fmt"
	"hash/crc32"
	"sort"
	"sync"
)

type HashRing struct {
	mutex      sync.Mutex
	sortedHash []uint32
	hashmap    map[uint32]string
	replica    int
}

func NewHashRing(replica int) *HashRing {
	return &HashRing{
		sortedHash: []uint32{},
		hashmap:    make(map[uint32]string),
		replica:    replica,
		mutex:      sync.Mutex{},
	}
}

func (hr *HashRing) AddNode(node string) {
	hr.mutex.Lock()
	defer hr.mutex.Unlock()

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
	hr.mutex.Lock()
	defer hr.mutex.Unlock()

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

	hr.mutex.Lock()
	defer hr.mutex.Unlock()

	hash := crc32.ChecksumIEEE([]byte(key))
	index := sort.Search(len(hr.sortedHash), func(i int) bool {
		return hr.sortedHash[i] >= hash
	})
	if index == len(hr.sortedHash) {
		index = 0
	}

	if _, ok := hr.hashmap[hr.sortedHash[index]]; ok {
		return hr.hashmap[hr.sortedHash[index]], nil
	} else {
		return "", fmt.Errorf("node not found for the key %s", key)
	}
}
