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
	BackupFilePath = "/data/backupCache.dat"
)

var (
	masterRequests     *prometheus.CounterVec
	masterResponseTime *prometheus.HistogramVec
	metricsOnce        sync.Once
)

func initMetrics() {
	metricsOnce.Do(func() {
		masterRequests = prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: "master_request_total",
				Help: "Total number of requests to the master node",
			}, []string{"method"},
		)
		masterResponseTime = prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "master_response_time_seconds",
				Help:    "Distribution of the response time processed by the master server",
				Buckets: prometheus.ExponentialBuckets(0.001, 2, 16),
			},
			[]string{"method"},
		)
		prometheus.MustRegister(masterRequests, masterResponseTime)
	})
}

type Master struct {
	hashring         *HashRing
	client           *http.Client
	requests         *prometheus.CounterVec
	responseTime     *prometheus.HistogramVec
	filepath         string
	auxServers       []string
	auxMu            sync.RWMutex
	activeAuxServers map[string]bool
	role             string // "primary" or "standby"
	standby          string // address of standby server (primary only)
}

func NewMaster(role, standby string) *Master {
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
	}
	client := &http.Client{Transport: transport}

	initMetrics()

	return &Master{
		client:           client,
		hashring:         NewHashRing(3),
		requests:         masterRequests,
		responseTime:     masterResponseTime,
		filepath:         BackupFilePath,
		auxServers:       getAuxServers(),
		activeAuxServers: make(map[string]bool),
		role:             role,
		standby:          standby,
	}
}

type KeyVal struct {
	Key   string `json:key`
	Value string `json:value`
}

type RingUpdate struct {
	Action string `json:"action"` // "add" or "remove"
	Aux    string `json:"aux"`
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
				resp.Body.Close()
			}
		}(i)
	}

	for k, v := range keyvals {
		keyvalChan <- KeyVal{Key: k, Value: v}
	}

	close(keyvalChan)
	wg.Wait()

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
			defer file.Close()

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
	defer file.Close()

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
	m.hashring.RemoveNode(auxServer)
	m.auxMu.Lock()
	m.activeAuxServers[auxServer] = false
	m.auxMu.Unlock()
	go m.pushRingUpdate("remove", auxServer)
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

func (m *Master) HealthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// StateHandler returns the current activeAuxServers map so the standby can mirror it.
func (m *Master) StateHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	m.auxMu.RLock()
	defer m.auxMu.RUnlock()
	json.NewEncoder(w).Encode(m.activeAuxServers)
}

// RingUpdateHandler receives ring change events pushed by the primary.
func (m *Master) RingUpdateHandler(w http.ResponseWriter, r *http.Request) {
	var update RingUpdate
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	m.auxMu.Lock()
	defer m.auxMu.Unlock()
	switch update.Action {
	case "add":
		m.hashring.AddNode(update.Aux)
		m.activeAuxServers[update.Aux] = true
	case "remove":
		m.hashring.RemoveNode(update.Aux)
		m.activeAuxServers[update.Aux] = false
	default:
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	log.Printf("standby ring update applied: %s %s", update.Action, update.Aux)
}

// pushRingUpdate fires a non-blocking ring change notification to the standby.
func (m *Master) pushRingUpdate(action, aux string) {
	if m.standby == "" {
		return
	}
	update := RingUpdate{Action: action, Aux: aux}
	body, err := json.Marshal(update)
	if err != nil {
		log.Printf("failed to marshal ring update: %v", err)
		return
	}
	resp, err := m.client.Post(
		fmt.Sprintf("http://%s/ring-update", m.standby),
		"application/json",
		bytes.NewBuffer(body),
	)
	if err != nil {
		log.Printf("failed to push ring update to standby %s: %v", m.standby, err)
		return
	}
	resp.Body.Close()
}

// initFromPrimary polls the primary's /state endpoint until it responds, then
// builds the local ring from the returned activeAuxServers map.
func (m *Master) initFromPrimary(primaryAddr string) {
	for {
		resp, err := m.client.Get(fmt.Sprintf("http://%s/state", primaryAddr))
		if err != nil {
			log.Printf("standby: waiting for primary at %s...", primaryAddr)
			time.Sleep(2 * time.Second)
			continue
		}
		var state map[string]bool
		if err := json.NewDecoder(resp.Body).Decode(&state); err != nil {
			resp.Body.Close()
			log.Printf("standby: failed to decode primary state: %v", err)
			time.Sleep(2 * time.Second)
			continue
		}
		resp.Body.Close()
		m.auxMu.Lock()
		m.activeAuxServers = state
		m.auxMu.Unlock()
		for aux, active := range state {
			if active {
				m.hashring.AddNode(aux)
			}
		}
		log.Printf("standby: initialized ring from primary (%d servers)", len(state))
		return
	}
}

// monitorPrimary polls the primary's /health endpoint. After 3 consecutive
// failures it signals the promote channel and exits.
func (m *Master) monitorPrimary(primaryAddr string, promote chan<- struct{}, stop <-chan interface{}) {
	failures := 0
	for {
		select {
		case <-stop:
			return
		default:
			resp, err := m.client.Get(fmt.Sprintf("http://%s/health", primaryAddr))
			if err == nil {
				resp.Body.Close()
				failures = 0
			} else {
				failures++
				log.Printf("standby: primary health check failed (%d/3)", failures)
				if failures >= 3 {
					log.Println("standby: primary unreachable, promoting to primary")
					promote <- struct{}{}
					return
				}
			}
			time.Sleep(5 * time.Second)
		}
	}
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
	m.auxMu.Lock()
	defer m.auxMu.Unlock()
	if val, ok := m.activeAuxServers[deadAux]; ok && val {
		m.hashring.RemoveNode(deadAux)
		go m.pushRingUpdate("remove", deadAux)
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
	m.auxMu.Lock()
	defer m.auxMu.Unlock()
	if val, ok := m.activeAuxServers[aliveAux]; ok && !val {

		distinctNodesToRebalance := m.getDistinctNodesToRebalance(aliveAux)

		m.hashring.AddNode(aliveAux)
		go m.pushRingUpdate("add", aliveAux)

		for _, node := range distinctNodesToRebalance {
			go func(node string) {

				resp, err := m.client.Get(fmt.Sprintf("http://%s/mappings", node))
				if err != nil {
					log.Printf("failed to get mappings from aux server %s: %v", node, err)
					return
				}
				defer resp.Body.Close()

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

				m.rebalance(mappings)

			}(node)
		}
	}
	m.activeAuxServers[aliveAux] = true
	log.Printf("heart of %s is beating... ", aliveAux)
}

// Checks the heartbeat of aux server every {duration} seconds
func (m *Master) HealthCheck(duration time.Duration, stop <-chan interface{}) {
	log.Printf("checking health of aux servers... %v", m.auxServers)

	done := make(chan struct{})
	defer close(done)

	deadAuxChan := make(chan string)
	aliveAuxChan := make(chan string)

	for _, aux := range m.auxServers {

		go func(aux string) {

			for {
				select {
				case <-done:
					return
				default:
				}

				if !m.checkAuxServerHealth(aux) {
					select {
					case deadAuxChan <- aux:
					case <-done:
						return
					}
				} else {
					select {
					case aliveAuxChan <- aux:
					case <-done:
						return
					}
				}

				time.Sleep(duration)
			}
		}(aux)
	}

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
