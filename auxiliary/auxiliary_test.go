package main

import (
	"os"
	"testing"
)

func TestDLL_Prepend(t *testing.T) {
	dll := NewDLL()

	node1 := &Node{Key: "alex", Value: "bhattarai"}
	node2 := &Node{Key: "ramesh", Value: "pokharel"}

	dll.Prepend(node1)
	if dll.Head != node1 || dll.Tail != node1 {
		t.Error("Prepend: Unexpected head and tail node")
	}

	dll.Prepend(node2)
	if dll.Head != node2 || dll.Tail != node1 {
		t.Error("Prepend: Unexpected head and tail node")
	}

}

func TestDLL_Append(t *testing.T) {
	dll := NewDLL()

	node1 := &Node{Key: "alex", Value: "bhattarai"}
	node2 := &Node{Key: "ramesh", Value: "pokharel"}

	dll.Append(node1)
	if dll.Head != node1 || dll.Tail != node1 {
		t.Error("Append: Unexpected head and tail node")
	}

	dll.Append(node2)
	if dll.Head != node1 || dll.Tail != node2 {
		t.Error("Append: Unexpected head and tail node")
	}
}

func TestDLL_Remove(t *testing.T) {
	dll := NewDLL()

	node1 := &Node{Key: "alex", Value: "bhattarai"}
	node2 := &Node{Key: "ramesh", Value: "pokharel"}

	dll.Append(node1)
	dll.Append(node2)

	dll.Remove(node1)
	if dll.Head != node2 || dll.Tail != node2 {
		t.Error("Remove: Unexpected head and tail node")
	}

	dll.Remove(node2)
	if dll.Head != nil || dll.Tail != nil {
		t.Error("Remove: Unexpected head and tail node")
	}

}

func TestLRU_Get(t *testing.T) {
	lru := NewLRU(3, "")

	lru.Put("Name", "Alex", 0)
	lru.Put("Age", "25", 0)
	lru.Put("Country", "NP", 0)

	val, err := lru.Get("Age")
	if err != nil {
		t.Errorf("Failed to get value for existing key %s: %v", "Age", err)
	}

	if val != "25" {
		t.Errorf("Unexpected value for key %s: got %s wanted %s", "Age", val, "25")
	}

	_, err = lru.Get("Town")
	if err == nil {
		t.Errorf("Expected error for non existent key %s", "Town")
	}

	expectedError := "value for the key Town not found"
	if err.Error() != expectedError {
		t.Errorf("Unexpected error message: wanted %s, got %s", expectedError, err.Error())
	}

}

func TestLRU_Put(t *testing.T) {
	lru := NewLRU(3, "")

	lru.Put("Name", "Alex", 0)
	lru.Put("Age", "25", 0)
	lru.Put("Country", "NP", 0)

	val1, err1 := lru.Get("Name")
	val2, err2 := lru.Get("Age")
	val3, err3 := lru.Get("Country")

	if err1 != nil || err2 != nil || err3 != nil {
		t.Error("Failed to get values for one or more keys")
	}
	if val1 != "Alex" || val2 != "25" || val3 != "NP" {
		t.Errorf("Unexpected values for the keys: wanted %s,%s,%s; got %s,%s,%s", "Alex", "25", "NP", val1, val2, val3)
	}

	// Eviction test: keys k15, k59, k60, k73 all hash to the same shard.
	// Fill the shard to capacity (3), then add a 4th key and verify the LRU
	// entry (k15) is evicted.
	lru2 := NewLRU(numShards*3, "") // each shard gets capacity 3
	lru2.Put("k15", "v1", 0)
	lru2.Put("k59", "v2", 0)
	lru2.Put("k60", "v3", 0)
	lru2.Put("k73", "v4", 0) // evicts k15 (LRU in this shard)

	if _, err := lru2.Get("k15"); err == nil {
		t.Error("Expected k15 to be evicted but it was still present")
	}
	if val, err := lru2.Get("k73"); err != nil || val != "v4" {
		t.Errorf("Expected k73=v4 but got err=%v val=%s", err, val)
	}
}

func TestLRU_SaveAndLoadFromDisk(t *testing.T) {
	filepath := "test.dat"

	lru := NewLRU(3, filepath)

	lru.Put("Name", "Alex", 0)
	lru.Put("Age", "25", 0)
	lru.Put("Country", "NP", 0)

	if ok, err := lru.saveToDisk(); !ok {
		t.Errorf("Failed to save to disk: err %v", err)
	}

	newlru := NewLRU(3, filepath)
	if ok, err := newlru.loadFromDisk(); !ok {
		t.Errorf("Failed to read from disk: err %v", err)
	}

	value, err := newlru.Get("Name")
	if err != nil {
		t.Errorf("Failed to get value for key %s: err %v", "Name", err)
	}

	if value != "Alex" {
		t.Errorf("Unexpected value for the key %s : wanted %s, got %s", "Name", "Alex", value)
	}

	err = os.Remove(filepath)
	if err != nil {
		t.Errorf("Failed to remove test file: err %v", err)
	}

}

func Benchmark_LRUPut(b *testing.B) {
	lru := NewLRU(3, "")

	for i := 0; i < b.N; i++ {
		lru.Put("Alex", "Name", 0)
	}
}

func Benchmark_LRUGet(b *testing.B) {
	lru := NewLRU(3, "")
	lru.Put("Alex", "Name", 0)

	for i := 0; i < b.N; i++ {
		lru.Get("Alex")
	}
}

func TestMain(m *testing.M) {
	m.Run()
}
