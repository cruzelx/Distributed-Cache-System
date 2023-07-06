package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/gorilla/mux"
)

type Auxiliary struct {
	LRU *LRU
}

type KeyVal struct {
	Key   string `json:key`
	Value string `json:value`
}

func (aux *Auxiliary) Put(w http.ResponseWriter, r *http.Request) {
	var kv KeyVal

	if err := json.NewDecoder(r.Body).Decode(&kv); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	aux.LRU.Put(kv.Key, kv.Value)

	w.WriteHeader(http.StatusOK)

}

func (aux *Auxiliary) Get(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	key := vars["key"]

	val, err := aux.LRU.Get(key)

	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(KeyVal{Key: key, Value: val})

}

func (aux *Auxiliary) Mappings(w http.ResponseWriter, r *http.Request) {

	keyvals := map[string]string{}
	curr := aux.LRU.dll.Head

	for curr != nil {
		keyvals[curr.Key] = curr.Value
		curr = curr.Next
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(keyvals)
}

func (aux *Auxiliary) Erase(w http.ResponseWriter, r *http.Request) {
	aux.LRU.EraseCache()
	w.WriteHeader(http.StatusOK)
}

func (aux *Auxiliary) Health(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (aux *Auxiliary) SendMappings() {

	keyvals := map[string]string{}
	curr := aux.LRU.dll.Head

	for curr != nil {
		keyvals[curr.Key] = curr.Value
		curr = curr.Next
	}

	postBody, err := json.Marshal(keyvals)
	if err != nil {
		log.Printf("couldn't parse key-val pairs: %v\n", err)
		return
	}

	log.Println("Sending mappings to master server...")

	client := &http.Client{}
	req, err := http.NewRequest("POST", "http://master:8000/rebalance-dead-aux", bytes.NewBuffer(postBody))
	if err != nil {
		log.Println("couldn't create the request")
		return
	}

	port := os.Getenv("PORT")
	serverId := os.Getenv("ID")

	auxServer := fmt.Sprintf("%s:%s", serverId, port)

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("aux-server", auxServer)

	resp, err := client.Do(req)
	if err != nil {
		log.Println("couldn't send mappings to master server")
		return
	}
	defer resp.Body.Close()

	log.Println("Mappings sent!!!")

}
