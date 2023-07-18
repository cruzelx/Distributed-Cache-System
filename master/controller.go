package main

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	backupFilePath = "/data/backupCache.dat"
)

type Master struct {
	hashring         *HashRing
	client           *http.Client
	requests         *prometheus.CounterVec
	responseTime     *prometheus.HistogramVec
	filepath         string
	auxServers       []string
	activeAuxServers map[string]bool
}

func NewMaster() *Master {
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
	}
	client := &http.Client{Transport: transport}

	requests := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "master_request_total",
			Help: "Total number of requests to the master node",
		}, []string{"method"},
	)

	responseTime := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "master_response_time_seconds",
			Help:    "Distribution of the response time processed by the master server",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 16),
		},
		[]string{"method"},
	)

	prometheus.MustRegister(requests, responseTime)

	return &Master{
		client:           client,
		hashring:         NewHashRing(3),
		requests:         requests,
		responseTime:     responseTime,
		filepath:         backupFilePath,
		auxServers:       getAuxServers(),
		activeAuxServers: make(map[string]bool),
	}
}

type KeyVal struct {
	Key   string `json:key`
	Value string `json:value`
}

func (m *Master) Put(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

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

	resp, err := m.client.Post(fmt.Sprintf("http://%s/data", node), "application/json", bytes.NewBuffer(postBody))
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	w.WriteHeader(resp.StatusCode)
	elapsedTime := time.Since(startTime).Seconds()
	m.requests.WithLabelValues(r.Method).Inc()
	m.responseTime.WithLabelValues(r.Method).Observe(elapsedTime)
}

func (m *Master) Get(w http.ResponseWriter, r *http.Request) {
	startTime := time.Now()

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

	resp, err := m.client.Get(fmt.Sprintf("http://%s/data/%s", node, key))
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

	elapsedTime := time.Since(startTime).Seconds()
	m.requests.WithLabelValues(r.Method).Inc()
	m.responseTime.WithLabelValues(r.Method).Observe(elapsedTime)
}

func (m *Master) rebalance(keyvals map[string]string) {
	startTime := time.Now()

	keyvalChan := make(chan KeyVal)

	var wg sync.WaitGroup
	numOfWorkers := 16

	if len(keyvals) < 16 {
		numOfWorkers = len(keyvals)
	}

	log.Printf("number of workers used for remapping: %d", numOfWorkers)

	for i := 0; i < numOfWorkers; i++ {
		wg.Add(1)

		go func(workID int) {
			defer wg.Done()

			for keyval := range keyvalChan {
				node, err := m.hashring.GetNode(keyval.Key)
				if err != nil {
					log.Printf("failed to remap key %s to server %s", keyval.Key, node)
					continue
				}

				postBody, err := json.Marshal(keyval)
				if err != nil {
					log.Printf("failed to parse key-value pair: %v \n", err)
					continue
				}

				resp, err := m.client.Post(fmt.Sprintf("http://%s/data", node), "application/json", bytes.NewBuffer(postBody))
				if err != nil {
					log.Printf("failed to send key-value pair to aux server %s", node)
					continue
				}
				defer resp.Body.Close()
			}
		}(i)
	}

	for k, v := range keyvals {
		keyvalChan <- KeyVal{Key: k, Value: v}
	}

	defer close(keyvalChan)
	elapsedTime := time.Since(startTime).Seconds()

	log.Printf("mapped keys in %v sec(s)\n", elapsedTime)

}

func (m *Master) backupCacheToDisk(keyvals map[string]string) error {
	_, err := os.Stat(m.filepath)
	if err != nil {
		if os.IsNotExist(err) {
			file, err := os.Create(m.filepath)
			if err != nil {
				return fmt.Errorf("failed to create backup file: %v", err)
			}

			encode := gob.NewEncoder(file)
			if err := encode.Encode(keyvals); err != nil {
				return fmt.Errorf("failed to encode key-val pairs to file %s: %v", m.filepath, err)

			}
			log.Printf("saved mappings to backup file %s", m.filepath)
			return nil
		}
		return fmt.Errorf("error reading stats of file %s: %v", m.filepath, err)
	}

	file, err := os.OpenFile(m.filepath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %v", m.filepath, err)

	}
	defer file.Close()

	encode := gob.NewEncoder(file)
	if err := encode.Encode(&keyvals); err != nil {
		return fmt.Errorf("failed to decode key-val pairs to file %s: %v", m.filepath, err)

	}
	log.Printf("saved mappings to backup file %s", m.filepath)
	return nil
}

func (m *Master) RestoreCacheFromDisk() error {
	file, err := os.Open(m.filepath)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %v", m.filepath, err)
	}

	var mappings map[string]string

	decode := gob.NewDecoder(file)
	if err := decode.Decode(&mappings); err != nil {
		return fmt.Errorf("failed to decode mappings from file %s: %v", m.filepath, err)
	}

	m.rebalance(mappings)

	return nil
}

// Aux sends mappings to this route before dying
func (m *Master) RebalanceDeadAuxServer(w http.ResponseWriter, r *http.Request) {
	var auxMappings map[string]string

	auxServer := r.Header.Get("aux-server")

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	err = json.Unmarshal(body, &auxMappings)
	if err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	log.Printf("Remapping %d keys from server %s", len(auxMappings), auxServer)
	// Broadcast aux node to be removed
	m.hashring.RemoveNode(auxServer)
	// rebalance all key-vals to the live aux servers
	m.rebalance(auxMappings)

	// Save to backup cache file while remapping
	// This helps in spinning up
	go func(filepath string, mappings map[string]string) {
		if err := m.backupCacheToDisk(auxMappings); err != nil {
			log.Println(err)
			return
		}
	}(m.filepath, auxMappings)

}

func (m *Master) checkAuxServerHealth(auxServer string) bool {
	resp, err := m.client.Get(fmt.Sprintf("http://%s/health", auxServer))
	if err != nil {
		log.Printf("failed to connect to aux server %s: %v", auxServer, err)
		return false
	}
	defer resp.Body.Close()

	return true
}

func (m *Master) handleDeadAuxServer(deadAux string) {
	if val, ok := m.activeAuxServers[deadAux]; ok && val {
		m.hashring.RemoveNode(deadAux)
	}
	m.activeAuxServers[deadAux] = false
	log.Printf("heart of %s has stopped beating... ", deadAux)
}

func (m *Master) getDistinctNodesToRebalance(node string) []string {
	distinctNodes := make(map[string]bool)

	for i := 0; i < m.hashring.replica; i++ {
		replicaNode := fmt.Sprintf("%s:%d", node, i)
		mappedNode, err := m.hashring.GetNode(replicaNode)

		if err != nil {
			log.Println(err)
			continue
		}

		distinctNodes[mappedNode] = true
	}

	result := make([]string, 0, len(distinctNodes))
	for k := range distinctNodes {
		result = append(result, k)
	}
	return result

}

func (m *Master) handleAliveAuxServer(aliveAux string) {
	if val, ok := m.activeAuxServers[aliveAux]; ok && !val {

		distinctNodesToRebalance := m.getDistinctNodesToRebalance(aliveAux)

		for _, node := range distinctNodesToRebalance {
			go func(node string) {

				resp, err := m.client.Get(fmt.Sprintf("http://%s/mappings", node))
				if err != nil {
					log.Printf("failed to get mappings from aux server %s: %v", node, err)
					return
				}

				data, err := io.ReadAll(resp.Body)
				if err != nil {
					log.Printf("failed to read mappings from the response: %v", err)
					return
				}

				var mappings map[string]string
				if err := json.Unmarshal(data, &mappings); err != nil {
					log.Printf("failed to parse the response body: %v", err)
					return
				}

				m.hashring.AddNode(aliveAux)

				m.rebalance(mappings)

				defer resp.Body.Close()

			}(node)
		}
	}
	m.activeAuxServers[aliveAux] = true
	log.Printf("heart of %s is beating... ", aliveAux)
}

// Checks the heartbeat of aux server every {duration} seconds
func (m *Master) HealthCheck(duration time.Duration, stop <-chan interface{}) {
	log.Printf("checking health of aux servers... %v", m.auxServers)

	deadAuxChan := make(chan string)
	aliveAuxChan := make(chan string)

	for _, aux := range m.auxServers {

		go func(aux string) {

			for {
				select {
				case <-stop:
					return

				default:
					if !m.checkAuxServerHealth(aux) {
						deadAuxChan <- aux
					} else {
						aliveAuxChan <- aux
					}
					time.Sleep(duration)
				}
			}
		}(aux)
	}

	defer func() {
		log.Printf("Exiting from health check...")
		close(deadAuxChan)
		close(aliveAuxChan)
	}()

	for {

		select {
		case deadAux := <-deadAuxChan:
			m.handleDeadAuxServer(deadAux)

		case aliveAux := <-aliveAuxChan:
			m.handleAliveAuxServer(aliveAux)

		case <-stop:
			log.Println("exiting health check...")
			return
		}
	}

}
