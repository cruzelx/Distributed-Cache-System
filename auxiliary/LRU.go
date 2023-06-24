package main

import (
	"encoding/gob"
	"fmt"
	"os"
)

type LRU struct {
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

func (lru *LRU) saveToDisk() (bool, error) {

	file, err := os.OpenFile(lru.filepath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0777)
	if err != nil {
		return false, err
	}
	defer file.Close()

	if len(lru.bucket) == 0 {
		return true, nil
	}

	curr := lru.dll.Head

	temp := make(map[string]string)

	for curr != nil {
		temp[curr.Key] = curr.Value
		curr = curr.Next
	}

	encode := gob.NewEncoder(file)
	if err := encode.Encode(temp); err != nil {
		return false, err
	}
	fmt.Println("saved at: ", lru.filepath)
	return true, nil
}

func (lru *LRU) loadFromDisk() (bool, error) {
	file, err := os.Open(lru.filepath)
	defer file.Close()
	if err != nil {
		return os.IsNotExist(err), err
	}

	decode := gob.NewDecoder(file)

	temp := make(map[string]string)

	if err := decode.Decode(&temp); err != nil {
		return false, err
	}

	lru.dll = NewDLL()

	for k, v := range temp {
		node := &Node{Key: k, Value: v}
		lru.dll.Append(node)
		lru.bucket[k] = node
	}
	return true, err

}
