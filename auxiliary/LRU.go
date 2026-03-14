package main

import (
	"encoding/gob"
	"fmt"
	"log"
	"os"
	"sync"
)

type LRU struct {
	mu       sync.RWMutex
	capacity int
	bucket   map[string]*Node
	dll      *DLL
	filepath string
}

func NewLRU(capacity int, filepath string) *LRU {
	return &LRU{
		capacity: capacity,
		bucket:   make(map[string]*Node, capacity),
		dll:      NewDLL(),
		filepath: filepath,
	}
}

func (lru *LRU) Get(key string) (string, error) {
	lru.mu.Lock()
	defer lru.mu.Unlock()
	if node, ok := lru.bucket[key]; ok {
		lru.dll.Remove(node)
		lru.dll.Prepend(node)
		return node.Value, nil
	}
	return "", fmt.Errorf("value for the key %s not found", key)
}
func (lru *LRU) Put(key string, value string) {
	lru.mu.Lock()
	defer lru.mu.Unlock()
	if node, ok := lru.bucket[key]; ok {
		node.Value = value
		lru.dll.Remove(node)
		lru.dll.Prepend(node)
	} else {
		if len(lru.bucket) >= lru.capacity {
			delete(lru.bucket, lru.dll.Tail.Key)
			lru.dll.Remove(lru.dll.Tail)
		}
		newNode := &Node{Key: key, Value: value}
		lru.bucket[key] = newNode
		lru.dll.Prepend(newNode)
	}

}

func (lru *LRU) EraseCache() {
	lru.mu.Lock()
	defer lru.mu.Unlock()
	lru.dll = NewDLL()
	lru.bucket = make(map[string]*Node, lru.capacity)
}

func (lru *LRU) GetAll() map[string]string {
	lru.mu.RLock()
	defer lru.mu.RUnlock()
	result := make(map[string]string, len(lru.bucket))
	curr := lru.dll.Head
	for curr != nil {
		result[curr.Key] = curr.Value
		curr = curr.Next
	}
	return result
}

func (lru *LRU) saveToDisk() (bool, error) {
	lru.mu.RLock()
	temp := make(map[string]string, len(lru.bucket))
	curr := lru.dll.Head
	for curr != nil {
		temp[curr.Key] = curr.Value
		curr = curr.Next
	}
	lru.mu.RUnlock()

	if len(temp) == 0 {
		return true, nil
	}

	file, err := os.OpenFile(lru.filepath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0777)
	if err != nil {
		return false, err
	}
	defer file.Close()

	encode := gob.NewEncoder(file)
	if err := encode.Encode(temp); err != nil {
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

	temp := make(map[string]string)
	if err := gob.NewDecoder(file).Decode(&temp); err != nil {
		return false, err
	}

	lru.mu.Lock()
	defer lru.mu.Unlock()
	lru.dll = NewDLL()
	lru.bucket = make(map[string]*Node, lru.capacity)
	for k, v := range temp {
		node := &Node{Key: k, Value: v}
		lru.dll.Append(node)
		lru.bucket[k] = node
	}
	return true, nil
}
