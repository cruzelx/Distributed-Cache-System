package main

import (
	"encoding/gob"
	"fmt"
	"log"
	"os"
	"sync"
	"time"
)

type diskSnapshot struct {
	Data   map[string]string
	Expiry map[string]time.Time
}

type LRU struct {
	mu       sync.RWMutex
	capacity int
	bucket   map[string]*Node
	dll      *DLL
	filepath string
	expiry   map[string]time.Time
}

func NewLRU(capacity int, filepath string) *LRU {
	return &LRU{
		capacity: capacity,
		bucket:   make(map[string]*Node, capacity),
		dll:      NewDLL(),
		filepath: filepath,
		expiry:   make(map[string]time.Time),
	}
}

func (lru *LRU) Get(key string) (string, error) {
	lru.mu.Lock()
	defer lru.mu.Unlock()
	node, ok := lru.bucket[key]
	if !ok {
		return "", fmt.Errorf("value for the key %s not found", key)
	}
	if exp, hasExp := lru.expiry[key]; hasExp && time.Now().After(exp) {
		lru.dll.Remove(node)
		delete(lru.bucket, key)
		delete(lru.expiry, key)
		return "", fmt.Errorf("value for the key %s not found", key)
	}
	lru.dll.Remove(node)
	lru.dll.Prepend(node)
	return node.Value, nil
}

// Put inserts or updates a key. ttlSecs > 0 sets an expiry; 0 means no expiry.
func (lru *LRU) Put(key, value string, ttlSecs int) {
	lru.mu.Lock()
	defer lru.mu.Unlock()
	if node, ok := lru.bucket[key]; ok {
		node.Value = value
		lru.dll.Remove(node)
		lru.dll.Prepend(node)
	} else {
		if len(lru.bucket) >= lru.capacity {
			tail := lru.dll.Tail
			delete(lru.bucket, tail.Key)
			delete(lru.expiry, tail.Key)
			lru.dll.Remove(tail)
		}
		newNode := &Node{Key: key, Value: value}
		lru.bucket[key] = newNode
		lru.dll.Prepend(newNode)
	}
	if ttlSecs > 0 {
		lru.expiry[key] = time.Now().Add(time.Duration(ttlSecs) * time.Second)
	} else {
		delete(lru.expiry, key)
	}
}

func (lru *LRU) Delete(key string) bool {
	lru.mu.Lock()
	defer lru.mu.Unlock()
	node, ok := lru.bucket[key]
	if !ok {
		return false
	}
	lru.dll.Remove(node)
	delete(lru.bucket, key)
	delete(lru.expiry, key)
	return true
}

func (lru *LRU) EraseCache() {
	lru.mu.Lock()
	defer lru.mu.Unlock()
	lru.dll = NewDLL()
	lru.bucket = make(map[string]*Node, lru.capacity)
	lru.expiry = make(map[string]time.Time)
}

func (lru *LRU) GetAll() map[string]string {
	lru.mu.RLock()
	defer lru.mu.RUnlock()
	now := time.Now()
	result := make(map[string]string, len(lru.bucket))
	curr := lru.dll.Head
	for curr != nil {
		if exp, hasExp := lru.expiry[curr.Key]; !hasExp || now.Before(exp) {
			result[curr.Key] = curr.Value
		}
		curr = curr.Next
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
	lru.mu.Lock()
	defer lru.mu.Unlock()
	now := time.Now()
	for key, exp := range lru.expiry {
		if now.After(exp) {
			if node, ok := lru.bucket[key]; ok {
				lru.dll.Remove(node)
				delete(lru.bucket, key)
			}
			delete(lru.expiry, key)
		}
	}
}

func (lru *LRU) saveToDisk() (bool, error) {
	lru.mu.RLock()
	snap := diskSnapshot{
		Data:   make(map[string]string, len(lru.bucket)),
		Expiry: make(map[string]time.Time, len(lru.expiry)),
	}
	curr := lru.dll.Head
	for curr != nil {
		snap.Data[curr.Key] = curr.Value
		curr = curr.Next
	}
	for k, v := range lru.expiry {
		snap.Expiry[k] = v
	}
	lru.mu.RUnlock()

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

	lru.mu.Lock()
	defer lru.mu.Unlock()
	lru.dll = NewDLL()
	lru.bucket = make(map[string]*Node, lru.capacity)
	lru.expiry = make(map[string]time.Time)
	now := time.Now()
	for k, v := range snap.Data {
		// Skip entries that expired while the server was down.
		if exp, hasExp := snap.Expiry[k]; hasExp && now.After(exp) {
			continue
		}
		node := &Node{Key: k, Value: v}
		lru.dll.Append(node)
		lru.bucket[k] = node
	}
	for k, v := range snap.Expiry {
		if _, ok := lru.bucket[k]; ok {
			lru.expiry[k] = v
		}
	}
	return true, nil
}
