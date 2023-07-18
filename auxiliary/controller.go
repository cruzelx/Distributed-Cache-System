package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
)

type Auxiliary struct {
	LRU          *LRU
	requests     *prometheus.CounterVec
	responseTime *prometheus.HistogramVec
}

type KeyVal struct {
	Key   string `json:key`
	Value string `json:value`
}

func NewAuxiliary(bucketSize int, filepath string) *Auxiliary {
	requests := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "auxiliary_request_total",
			Help: "Total number of requests to the auxiliary node",
		}, []string{"method"},
	)

	responseTime := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "auxiliary_response_time_seconds",
			Help:    "Distribution of the response time processed by the auxiliary server",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 16),
		},
		[]string{"method"},
	)

	prometheus.MustRegister(requests, responseTime)

	return &Auxiliary{
		LRU:          NewLRU(bucketSize, filepath),
		requests:     requests,
		responseTime: responseTime,
	}

}

func (aux *Auxiliary) Put(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	var kv KeyVal

	if err := json.NewDecoder(r.Body).Decode(&kv); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	aux.LRU.Put(kv.Key, kv.Value)

	elapsedTime := time.Since(startTime).Seconds()
	aux.requests.WithLabelValues(r.Method).Inc()
	aux.responseTime.WithLabelValues(r.Method).Observe(elapsedTime)
	w.WriteHeader(http.StatusOK)

}

func (aux *Auxiliary) Get(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

	vars := mux.Vars(r)
	key := vars["key"]

	val, err := aux.LRU.Get(key)

	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	elapsedTime := time.Since(startTime).Seconds()
	aux.requests.WithLabelValues(r.Method).Inc()
	aux.responseTime.WithLabelValues(r.Method).Observe(elapsedTime)
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
		log.Printf("failed to parse key-val pairs: %v\n", err)
		return
	}

	log.Println("sending mappings to master server...")

	client := &http.Client{}
	masterServer := os.Getenv("MASTER_SERVER")
	req, err := http.NewRequest("POST", fmt.Sprintf("http://%s/rebalance-dead-aux", masterServer), bytes.NewBuffer(postBody))
	if err != nil {
		log.Println("failed to create the request")
		return
	}

	port := os.Getenv("PORT")
	serverId := os.Getenv("ID")

	auxServer := fmt.Sprintf("%s:%s", serverId, port)

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("aux-server", auxServer)

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("failed to send mappings to master server: %s", err.Error())
		return
	}
	defer resp.Body.Close()

	log.Println("mappings sent!!!")

}
