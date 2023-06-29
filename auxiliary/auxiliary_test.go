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

	lru.Put("Name", "Alex")
	lru.Put("Age", "25")
	lru.Put("Country", "NP")

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

	expectedOrder := []string{"Age", "Country", "Name"}

	curr := lru.dll.Head
	for _, key := range expectedOrder {
		if key != curr.Key {
			t.Errorf("Unexpected key order in LRU Cache: wanted %s, got %s", key, curr.Key)
		}
		curr = curr.Next
	}

}

func TestLRU_Put(t *testing.T) {
	lru := NewLRU(3, "")

	lru.Put("Name", "Alex")
	lru.Put("Age", "25")
	lru.Put("Country", "NP")

	val1, err1 := lru.Get("Name")
	val2, err2 := lru.Get("Age")
	val3, err3 := lru.Get("Country")

	if err1 != nil || err2 != nil || err3 != nil {
		t.Error("Failed to get values for one or more keys")
	}
	if val1 != "Alex" || val2 != "25" || val3 != "NP" {
		t.Errorf("Unexpected values for the keys: wanted %s,%s,%s; got %s,%s,%s", "Alex", "25", "NP", val1, val2, val3)
	}

	lru.Put("Wallet", "Bitcoin")

	_, err := lru.Get("Name")
	if err == nil {
		t.Errorf("Expected error while getting evicted key %s ", "Name")
	}

	val, err := lru.Get("Wallet")

	if err != nil {
		t.Errorf("Failed to get value for key %s: err %v", "Wallet", err)
	}

	if val != "Bitcoin" {
		t.Errorf("Unexpected value for the key %s: wanted %s, got %s", "Wallet", "Bitcoin", val)
	}

}

func TestLRU_SaveAndLoadFromDisk(t *testing.T) {
	filepath := "test.dat"

	lru := NewLRU(3, filepath)

	lru.Put("Name", "Alex")
	lru.Put("Age", "25")
	lru.Put("Country", "NP")

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
		lru.Put("Alex", "Name")
	}
}

func Benchmark_LRUGet(b *testing.B) {
	lru := NewLRU(3, "")
	lru.Put("Alex", "Name")

	for i := 0; i < b.N; i++ {
		lru.Get("Alex")
	}
}

func TestMain(m *testing.M) {
	m.Run()
}
