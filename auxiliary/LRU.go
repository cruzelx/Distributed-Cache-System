package main

import (
	"fmt"
)

type LRU struct {
	capacity int
	bucket   map[string]*Node
	dll      *DLL
}

func NewLRU(capacity int) *LRU {
	return &LRU{
		capacity: capacity,
		bucket:   make(map[string]*Node, capacity),
		dll:      NewDLL(),
	}
}

func (lru *LRU) Get(key string) (string, error) {
	if node, ok := lru.bucket[key]; ok {
		lru.dll.Remove(node)
		lru.dll.Prepend(node)
		return node.Value, nil
	}
	return "", fmt.Errorf("value for the key %s not found", key)
}
func (lru *LRU) Put(key string, value string) {
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
