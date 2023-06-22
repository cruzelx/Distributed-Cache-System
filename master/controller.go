package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
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

	err := json.NewDecoder(r.Body).Decode(&kv)

	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	node, err := m.hashring.GetNode(kv.Key)

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	resp, err := http.Post(fmt.Sprintf("http://%s/data", node), "applicaiton/json", r.Body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
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
	fmt.Println("Node: ", node)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	resp, err := http.Get(fmt.Sprintf("http://%s/data/%s", node, key))
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	defer resp.Body.Close()
	w.WriteHeader(resp.StatusCode)

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if _, err := w.Write(body); err != nil {
		fmt.Println("Failed to write reponse:  ", err)
	}

}
