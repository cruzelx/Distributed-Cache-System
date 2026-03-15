package main

import (
	"encoding/gob"
	"fmt"
	"hash/fnv"
	"log"
	"os"
	"sync"
	"time"
)

const numShards = 16

type diskSnapshot struct {
	Data   map[string]string
	Expiry map[string]time.Time
}

// lruShard is one independently-locked segment of the cache.
type lruShard struct {
	mu       sync.Mutex
	capacity int
	bucket   map[string]*Node
	dll      *DLL
	expiry   map[string]time.Time
}

type LRU struct {
	shards   [numShards]lruShard
	filepath string
}

func NewLRU(capacity int, filepath string) *LRU {
	// Ceiling division so total capacity >= requested.
	shardCap := (capacity + numShards - 1) / numShards
	if shardCap < 1 {
		shardCap = 1
	}
	lru := &LRU{filepath: filepath}
	for i := range lru.shards {
		lru.shards[i] = lruShard{
			capacity: shardCap,
			bucket:   make(map[string]*Node, shardCap),
			dll:      NewDLL(),
			expiry:   make(map[string]time.Time),
		}
	}
	return lru
}

func shardIndex(key string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(key))
	return h.Sum32() % numShards
}

func (lru *LRU) shardFor(key string) *lruShard {
	return &lru.shards[shardIndex(key)]
}

func (lru *LRU) Get(key string) (string, error) {
	s := lru.shardFor(key)
	s.mu.Lock()
	defer s.mu.Unlock()

	node, ok := s.bucket[key]
	if !ok {
		return "", fmt.Errorf("value for the key %s not found", key)
	}
	if exp, hasExp := s.expiry[key]; hasExp && time.Now().After(exp) {
		s.dll.Remove(node)
		delete(s.bucket, key)
		delete(s.expiry, key)
		return "", fmt.Errorf("value for the key %s not found", key)
	}
	s.dll.Remove(node)
	s.dll.Prepend(node)
	return node.Value, nil
}

func (lru *LRU) Put(key, value string, ttlSecs int) {
	s := lru.shardFor(key)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.putLocked(key, value, ttlSecs)
}

// putLocked inserts or updates a key. Caller must hold s.mu.
func (s *lruShard) putLocked(key, value string, ttlSecs int) {
	if node, ok := s.bucket[key]; ok {
		node.Value = value
		s.dll.Remove(node)
		s.dll.Prepend(node)
	} else {
		if len(s.bucket) >= s.capacity {
			tail := s.dll.Tail
			delete(s.bucket, tail.Key)
			delete(s.expiry, tail.Key)
			s.dll.Remove(tail)
		}
		newNode := &Node{Key: key, Value: value}
		s.bucket[key] = newNode
		s.dll.Prepend(newNode)
	}
	if ttlSecs > 0 {
		s.expiry[key] = time.Now().Add(time.Duration(ttlSecs) * time.Second)
	} else {
		delete(s.expiry, key)
	}
}

// BulkPut groups entries by shard so each shard lock is acquired once.
func (lru *LRU) BulkPut(entries []KeyVal) {
	type kv struct {
		key, val string
		ttl      int
	}
	var groups [numShards][]kv
	for _, e := range entries {
		idx := shardIndex(e.Key)
		groups[idx] = append(groups[idx], kv{e.Key, e.Value, e.TTL})
	}
	for i := range lru.shards {
		if len(groups[i]) == 0 {
			continue
		}
		s := &lru.shards[i]
		s.mu.Lock()
		for _, e := range groups[i] {
			s.putLocked(e.key, e.val, e.ttl)
		}
		s.mu.Unlock()
	}
}

func (lru *LRU) Delete(key string) bool {
	s := lru.shardFor(key)
	s.mu.Lock()
	defer s.mu.Unlock()
	node, ok := s.bucket[key]
	if !ok {
		return false
	}
	s.dll.Remove(node)
	delete(s.bucket, key)
	delete(s.expiry, key)
	return true
}

func (lru *LRU) EraseCache() {
	for i := range lru.shards {
		s := &lru.shards[i]
		s.mu.Lock()
		s.dll = NewDLL()
		s.bucket = make(map[string]*Node, s.capacity)
		s.expiry = make(map[string]time.Time)
		s.mu.Unlock()
	}
}

func (lru *LRU) GetAll() map[string]string {
	result := make(map[string]string)
	now := time.Now()
	for i := range lru.shards {
		s := &lru.shards[i]
		s.mu.Lock()
		for curr := s.dll.Head; curr != nil; curr = curr.Next {
			if exp, hasExp := s.expiry[curr.Key]; !hasExp || now.Before(exp) {
				result[curr.Key] = curr.Value
			}
		}
		s.mu.Unlock()
	}
	return result
}

// startReaper launches a background goroutine that removes expired keys every interval.
func (lru *LRU) startReaper(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			lru.reapExpired()
		}
	}()
}

func (lru *LRU) reapExpired() {
	now := time.Now()
	for i := range lru.shards {
		s := &lru.shards[i]
		s.mu.Lock()
		for key, exp := range s.expiry {
			if now.After(exp) {
				if node, ok := s.bucket[key]; ok {
					s.dll.Remove(node)
					delete(s.bucket, key)
				}
				delete(s.expiry, key)
			}
		}
		s.mu.Unlock()
	}
}

func (lru *LRU) saveToDisk() (bool, error) {
	snap := diskSnapshot{
		Data:   make(map[string]string),
		Expiry: make(map[string]time.Time),
	}

	for i := range lru.shards {
		s := &lru.shards[i]
		s.mu.Lock()
		for curr := s.dll.Head; curr != nil; curr = curr.Next {
			snap.Data[curr.Key] = curr.Value
		}
		for k, v := range s.expiry {
			snap.Expiry[k] = v
		}
		s.mu.Unlock()
	}

	if len(snap.Data) == 0 {
		return true, nil
	}

	file, err := os.OpenFile(lru.filepath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0777)
	if err != nil {
		return false, err
	}
	defer file.Close()

	if err := gob.NewEncoder(file).Encode(snap); err != nil {
		return false, err
	}
	log.Println("saved at: ", lru.filepath)
	return true, nil
}

func (lru *LRU) loadFromDisk() (bool, error) {
	file, err := os.Open(lru.filepath)
	if err != nil {
		return os.IsNotExist(err), err
	}
	defer file.Close()

	var snap diskSnapshot
	if err := gob.NewDecoder(file).Decode(&snap); err != nil {
		return false, err
	}

	// Group valid entries by shard to acquire each lock once.
	type entry struct{ key, val string }
	var groups [numShards][]entry
	now := time.Now()
	for k, v := range snap.Data {
		if exp, hasExp := snap.Expiry[k]; hasExp && now.After(exp) {
			continue // expired while server was down
		}
		groups[shardIndex(k)] = append(groups[shardIndex(k)], entry{k, v})
	}

	for i := range lru.shards {
		if len(groups[i]) == 0 {
			continue
		}
		s := &lru.shards[i]
		s.mu.Lock()
		for _, e := range groups[i] {
			node := &Node{Key: e.key, Value: e.val}
			s.dll.Append(node)
			s.bucket[e.key] = node
			if exp, ok := snap.Expiry[e.key]; ok {
				s.expiry[e.key] = exp
			}
		}
		s.mu.Unlock()
	}
	return true, nil
}
