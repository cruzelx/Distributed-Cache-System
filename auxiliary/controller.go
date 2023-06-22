package main

import (
	"encoding/json"
	"net/http"

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
	key, ok := vars["key"]

	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	val, err := aux.LRU.Get(key)

	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(val))

}
