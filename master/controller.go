package main

import (
	"bytes"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
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
	isPrimary         atomic.Bool
	role              string // "primary" or "standby"
	standby           string // address of standby server (primary only)
	replicationFactor int

	// health check state — set by HealthCheck, used by startAuxMonitor/AddNodeHandler
	deadAuxChan  chan string
	aliveAuxChan chan string
	healthDone   chan struct{}

	// replicaSem limits total concurrent outgoing writes to aux nodes.
	replicaSem chan struct{}
}

func NewMaster(role, standby string) *Master {
	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
	}
	client := &http.Client{Transport: transport}

	initMetrics()

	rf := 2
	if val := os.Getenv("REPLICATION_FACTOR"); val != "" {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			rf = n
		}
	}

	m := &Master{
		client:            client,
		hashring:          NewHashRing(150),
		requests:          masterRequests,
		responseTime:      masterResponseTime,
		filepath:          BackupFilePath,
		auxServers:        getAuxServers(),
		activeAuxServers:  make(map[string]bool),
		role:              role,
		standby:           standby,
		replicationFactor: rf,
		replicaSem:        make(chan struct{}, 64),
	}
	m.isPrimary.Store(role == "primary")
	return m
}

type KeyVal struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	TTL   int    `json:"ttl,omitempty"` // seconds; 0 means no expiry
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

	nodes, err := m.hashring.GetNodes(kv.Key, m.replicationFactor)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	postBody, err := json.Marshal(kv)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Write to all replicas in parallel; succeed if at least one write lands.
	errs := make(chan error, len(nodes))
	for _, node := range nodes {
		go func(node string) {
			m.replicaSem <- struct{}{}
			defer func() { <-m.replicaSem }()
			resp, err := m.client.Post(fmt.Sprintf("http://%s/data", node), "application/json", bytes.NewBuffer(postBody))
			if err != nil {
				errs <- err
				return
			}
			resp.Body.Close()
			errs <- nil
		}(node)
	}

	succeeded := 0
	for range nodes {
		if err := <-errs; err != nil {
			log.Printf("Put: replica write to failed: %v", err)
		} else {
			succeeded++
		}
	}

	if succeeded == 0 {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
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

	nodes, err := m.hashring.GetNodes(key, m.replicationFactor)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Shuffle replicas so reads are spread across all replicas, not always hitting node[0].
	rand.Shuffle(len(nodes), func(i, j int) { nodes[i], nodes[j] = nodes[j], nodes[i] })
	for _, node := range nodes {
		resp, err := m.client.Get(fmt.Sprintf("http://%s/data/%s", node, key))
		if err != nil {
			log.Printf("Get: replica %s unavailable: %v", node, err)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			continue
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		io.Copy(w, resp.Body)
		resp.Body.Close()
		elapsedTime := time.Since(startTime).Seconds()
		m.requests.WithLabelValues(r.Method).Inc()
		m.responseTime.WithLabelValues(r.Method).Observe(elapsedTime)
		return
	}

	elapsedTime := time.Since(startTime).Seconds()
	m.requests.WithLabelValues(r.Method).Inc()
	m.responseTime.WithLabelValues(r.Method).Observe(elapsedTime)
	http.Error(w, fmt.Sprintf("key %s not found", key), http.StatusNotFound)
}

func (m *Master) Delete(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	key, ok := vars["key"]
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	nodes, err := m.hashring.GetNodes(key, m.replicationFactor)
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// Delete from all replicas; 200 if at least one had the key.
	deleted := false
	for _, node := range nodes {
		req, err := http.NewRequest(http.MethodDelete, fmt.Sprintf("http://%s/data/%s", node, key), nil)
		if err != nil {
			continue
		}
		resp, err := m.client.Do(req)
		if err != nil {
			continue
		}
		if resp.StatusCode == http.StatusOK {
			deleted = true
		}
		resp.Body.Close()
	}

	if !deleted {
		http.Error(w, fmt.Sprintf("key %s not found", key), http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (m *Master) BulkPut(w http.ResponseWriter, r *http.Request) {
	var entries []KeyVal
	if err := json.NewDecoder(r.Body).Decode(&entries); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// Group entries by target nodes; each entry goes to all its replicas.
	groups := make(map[string][]KeyVal)
	for _, kv := range entries {
		nodes, err := m.hashring.GetNodes(kv.Key, m.replicationFactor)
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		for _, node := range nodes {
			groups[node] = append(groups[node], kv)
		}
	}

	// Fan out to each node in parallel.
	errs := make(chan error, len(groups))
	for node, batch := range groups {
		go func(node string, batch []KeyVal) {
			m.replicaSem <- struct{}{}
			defer func() { <-m.replicaSem }()
			body, err := json.Marshal(batch)
			if err != nil {
				errs <- err
				return
			}
			resp, err := m.client.Post(fmt.Sprintf("http://%s/bulk", node), "application/json", bytes.NewBuffer(body))
			if err != nil {
				errs <- err
				return
			}
			resp.Body.Close()
			errs <- nil
		}(node, batch)
	}

	for range groups {
		if err := <-errs; err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
}

func (m *Master) BulkGet(w http.ResponseWriter, r *http.Request) {
	var keys []string
	if err := json.NewDecoder(r.Body).Decode(&keys); err != nil {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	// Group keys by a randomly chosen replica so hot keys spread across replicas.
	groups := make(map[string][]string)
	for _, key := range keys {
		nodes, err := m.hashring.GetNodes(key, m.replicationFactor)
		if err != nil {
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		chosen := nodes[rand.Intn(len(nodes))]
		groups[chosen] = append(groups[chosen], key)
	}

	type nodeResult struct {
		data map[string]string
		err  error
	}
	resultCh := make(chan nodeResult, len(groups))

	for node, batch := range groups {
		go func(node string, batch []string) {
			body, err := json.Marshal(batch)
			if err != nil {
				resultCh <- nodeResult{err: err}
				return
			}
			resp, err := m.client.Post(fmt.Sprintf("http://%s/bulk/get", node), "application/json", bytes.NewBuffer(body))
			if err != nil {
				resultCh <- nodeResult{err: err}
				return
			}
			defer resp.Body.Close()
			var found map[string]string
			if err := json.NewDecoder(resp.Body).Decode(&found); err != nil {
				resultCh <- nodeResult{err: err}
				return
			}
			resultCh <- nodeResult{data: found}
		}(node, batch)
	}

	merged := make(map[string]string, len(keys))
	for range groups {
		res := <-resultCh
		if res.err == nil {
			for k, v := range res.data {
				merged[k] = v
			}
		}
	}

	// Second pass: for keys not found on their primary, try secondary replicas.
	if m.replicationFactor > 1 {
		for _, key := range keys {
			if _, found := merged[key]; found {
				continue
			}
			nodes, err := m.hashring.GetNodes(key, m.replicationFactor)
			if err != nil {
				continue
			}
			for _, node := range nodes[1:] {
				resp, err := m.client.Get(fmt.Sprintf("http://%s/data/%s", node, key))
				if err != nil {
					continue
				}
				if resp.StatusCode == http.StatusOK {
					var kv KeyVal
					if err := json.NewDecoder(resp.Body).Decode(&kv); err == nil {
						merged[key] = kv.Value
					}
					resp.Body.Close()
					break
				}
				resp.Body.Close()
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(merged)
}

func (m *Master) rebalance(keyvals map[string]string) {
	if len(keyvals) == 0 {
		return
	}

	startTime := time.Now()

	numOfWorkers := 16
	if len(keyvals) < numOfWorkers {
		numOfWorkers = len(keyvals)
	}

	log.Printf("rebalancing %d keys with %d workers", len(keyvals), numOfWorkers)

	keyvalChan := make(chan KeyVal)
	var wg sync.WaitGroup

	for i := 0; i < numOfWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for keyval := range keyvalChan {
				nodes, err := m.hashring.GetNodes(keyval.Key, m.replicationFactor)
				if err != nil {
					log.Printf("failed to remap key %s: %v", keyval.Key, err)
					continue
				}
				postBody, err := json.Marshal(keyval)
				if err != nil {
					log.Printf("failed to marshal key-value pair: %v", err)
					continue
				}
				for _, node := range nodes {
					resp, err := m.client.Post(fmt.Sprintf("http://%s/data", node), "application/json", bytes.NewBuffer(postBody))
					if err != nil {
						log.Printf("failed to send key %s to aux server %s: %v", keyval.Key, node, err)
						continue
					}
					resp.Body.Close()
				}
			}
		}()
	}

	for k, v := range keyvals {
		keyvalChan <- KeyVal{Key: k, Value: v}
	}
	close(keyvalChan)
	wg.Wait()

	log.Printf("rebalanced %d keys in %.3fs", len(keyvals), time.Since(startTime).Seconds())
}

func (m *Master) backupCacheToDisk(keyvals map[string]string) error {
	file, err := os.OpenFile(m.filepath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to open backup file %s: %v", m.filepath, err)
	}
	defer file.Close()

	if err := gob.NewEncoder(file).Encode(keyvals); err != nil {
		return fmt.Errorf("failed to encode cache to %s: %v", m.filepath, err)
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
	m.auxMu.Lock()
	m.activeAuxServers[auxServer] = false
	m.hashring.RemoveNode(auxServer)
	m.auxMu.Unlock()
	go m.pushRingUpdate("remove", auxServer)
	m.rebalance(auxMappings)

	// Persist the redistributed mappings so they survive a full restart.
	go func() {
		if err := m.backupCacheToDisk(auxMappings); err != nil {
			log.Println(err)
		}
	}()

}

func (m *Master) HealthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func (m *Master) RoleHandler(w http.ResponseWriter, r *http.Request) {
	role := "standby"
	if m.isPrimary.Load() {
		role = "primary"
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"role": role})
}

// checkStandbyRole queries the standby's /role endpoint.
// Returns "primary", "standby", or "" if unreachable.
func checkStandbyRole(client *http.Client, standbyAddr string) string {
	resp, err := client.Get(fmt.Sprintf("http://%s/role", standbyAddr))
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var result struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ""
	}
	return result.Role
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

// AddNodeHandler registers a new aux node into the ring at runtime.
func (m *Master) AddNodeHandler(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Addr string `json:"addr"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Addr == "" {
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}

	if !m.checkAuxServerHealth(req.Addr) {
		http.Error(w, fmt.Sprintf("node %s unreachable", req.Addr), http.StatusBadGateway)
		return
	}

	m.auxMu.Lock()
	if active, exists := m.activeAuxServers[req.Addr]; exists && active {
		m.auxMu.Unlock()
		w.WriteHeader(http.StatusOK)
		return
	}
	m.auxServers = append(m.auxServers, req.Addr)
	m.activeAuxServers[req.Addr] = true
	// Compute ring neighbors before adding so we know whose keys will migrate.
	neighbors := m.getDistinctNodesToRebalance(req.Addr)
	m.hashring.AddNode(req.Addr)
	m.auxMu.Unlock()

	go m.pushRingUpdate("add", req.Addr)

	// Rebalance keys from ring neighbors that now belong to the new node.
	for _, node := range neighbors {
		go func(node string) {
			resp, err := m.client.Get(fmt.Sprintf("http://%s/mappings", node))
			if err != nil {
				log.Printf("AddNode: failed to get mappings from %s: %v", node, err)
				return
			}
			defer resp.Body.Close()
			var mappings map[string]string
			if err := json.NewDecoder(resp.Body).Decode(&mappings); err != nil {
				log.Printf("AddNode: failed to decode mappings from %s: %v", node, err)
				return
			}
			m.rebalance(mappings)
		}(node)
	}

	// Start health monitoring for the new node if HealthCheck is running.
	if m.healthDone != nil {
		m.startAuxMonitor(req.Addr, 5*time.Second)
	}

	log.Printf("dynamically added aux node %s", req.Addr)
	w.WriteHeader(http.StatusOK)
}

// startAuxMonitor spawns a single health-polling goroutine for aux.
// Must only be called after HealthCheck has initialized the shared channels.
func (m *Master) startAuxMonitor(aux string, duration time.Duration) {
	go func() {
		for {
			select {
			case <-m.healthDone:
				return
			default:
			}

			if !m.checkAuxServerHealth(aux) {
				select {
				case m.deadAuxChan <- aux:
				case <-m.healthDone:
					return
				}
			} else {
				select {
				case m.aliveAuxChan <- aux:
				case <-m.healthDone:
					return
				}
			}

			time.Sleep(duration)
		}
	}()
}

// Checks the heartbeat of aux server every {duration} seconds
func (m *Master) HealthCheck(duration time.Duration, stop <-chan interface{}) {
	log.Printf("checking health of aux servers... %v", m.auxServers)

	m.deadAuxChan = make(chan string)
	m.aliveAuxChan = make(chan string)
	m.healthDone = make(chan struct{})
	defer close(m.healthDone)

	for _, aux := range m.auxServers {
		m.startAuxMonitor(aux, duration)
	}

	for {
		select {
		case deadAux := <-m.deadAuxChan:
			m.handleDeadAuxServer(deadAux)

		case aliveAux := <-m.aliveAuxChan:
			m.handleAliveAuxServer(aliveAux)

		case <-stop:
			log.Println("exiting health check...")
			return
		}
	}
}
