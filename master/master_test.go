package main

import (
	"fmt"
	"testing"
	"time"
)

func TestHashRing_AddNode(t *testing.T) {
	hr := NewHashRing(3)

	hr.AddNode("aux1")
	hr.AddNode("aux2")
	hr.AddNode("aux3")

	expectedHashLength := hr.replica * 3

	if expectedHashLength != len(hr.sortedHash) {
		t.Errorf("Unexpected number of hashes: got %v wanted %v", len(hr.sortedHash), expectedHashLength)
	}

	keys := []string{"key1", "key2", "key3"}

	for _, k := range keys {
		node, err := hr.GetNode(k)
		if err != nil {
			t.Errorf("Failed to get node for key %s", k)
		}
		if node != "aux1" && node != "aux2" && node != "aux3" {
			t.Errorf("Unexpected node mapping for key %s : got %s", k, node)
		}
	}
}

func TestHashRing_GetNode(t *testing.T) {
	hr := NewHashRing(3)

	hr.AddNode("aux1:3001")
	hr.AddNode("aux2:3002")
	hr.AddNode("aux3:3003")

	testKey := "some-key"
	_, err := hr.GetNode(testKey)
	if err != nil {
		t.Errorf("Failed to get map for the key %s : err %v", testKey, err)
	}

	testKeyNode := map[string]string{
		"water":    "aux1:3001",
		"Interest": "aux2:3002",
		"Escape":   "aux3:3003",
	}

	for k, n := range testKeyNode {
		node, err := hr.GetNode(k)
		if err != nil {
			t.Errorf("Failed to get map for the key %s : err %v", k, err)
		}
		if node != n {
			t.Errorf("Unexpected node mapping for key %s : got %s wanted %s", k, node, n)
		}
	}

}

func TestHashRing_Performance(t *testing.T) {
	hr := NewHashRing(10)

	hr.AddNode("aux1:3001")
	hr.AddNode("aux2:3002")
	hr.AddNode("aux3:3003")

	count := map[string]int{
		"aux1:3001": 0,
		"aux2:3002": 0,
		"aux3:3003": 0,
	}

	numOfKeys := 10_00_00_00
	keys := make([]string, numOfKeys)

	for i := 0; i < numOfKeys; i++ {
		keys[i] = fmt.Sprintf("key%d", i)
	}

	for _, key := range keys {
		node, err := hr.GetNode(key)
		if err != nil {
			t.Errorf("Failed to get map for the key %s : err %v", key, err)
		}
		count[node] += 1
	}

	startTime := time.Now()
	for _, key := range keys {
		_, err := hr.GetNode(key)
		if err != nil {
			t.Errorf("Failed to get map for the key %s : err %v", key, err)
		}

	}
	elapsedTime := time.Since(startTime)

	t.Logf("Performance test: GetNode for %d keys", numOfKeys)
	t.Logf("Elapsed time: %s", elapsedTime)
	t.Logf("Average time per key: %s", elapsedTime/time.Duration(numOfKeys))
	t.Logf("Distribution: aux1:3001 => %.2f%% aux2:3002 => %.2f%% aux3:3003 => %.2f%% ",
		float64(count["aux1:3001"])*100.0/float64(numOfKeys),
		float64(count["aux2:3002"])*100.0/float64(numOfKeys),
		float64(count["aux3:3003"])*100.0/float64(numOfKeys))
}

func TestMain(m *testing.M) {
	m.Run()
}
