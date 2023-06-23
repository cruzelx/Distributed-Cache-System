package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/gorilla/mux"
)

type Master struct {
	hashring *HashRing
}

type KeyVal struct {
	Key   string `json:key`
	Value string `json:value`
}

func (m *Master) Put(w http.ResponseWriter, r *http.Request) {
	var kv KeyVal

	if err := json.NewDecoder(r.Body).Decode(&kv); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	node, err := m.hashring.GetNode(kv.Key)
	fmt.Println("Node: ", node)

	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	postBody, err := json.Marshal(kv)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	resp, err := http.Post(fmt.Sprintf("http://%s/data", node), "application/json", bytes.NewBuffer(postBody))
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	w.WriteHeader(resp.StatusCode)
}

func (m *Master) Get(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	key, ok := vars["key"]

	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	node, err := m.hashring.GetNode(key)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	resp, err := http.Get(fmt.Sprintf("http://%s/data/%s", node, key))
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	defer resp.Body.Close()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)

	_, err = io.Copy(w, resp.Body)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

}
