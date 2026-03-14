package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func Start() {
	role := os.Getenv("ROLE")
	if role == "" {
		role = "primary"
	}
	standby := os.Getenv("STANDBY_SERVER")
	primaryAddr := os.Getenv("PRIMARY_MASTER")

	m := NewMaster(role, standby)

	r := mux.NewRouter()
	r.Use(mux.CORSMethodMiddleware(r))

	r.HandleFunc("/data", m.Put).Methods("POST")
	r.HandleFunc("/data/{key}", m.Get).Methods("GET")
	r.HandleFunc("/rebalance-dead-aux", m.RebalanceDeadAuxServer).Methods("POST")
	r.HandleFunc("/health", m.HealthHandler).Methods("GET")
	r.HandleFunc("/state", m.StateHandler).Methods("GET")
	r.HandleFunc("/ring-update", m.RingUpdateHandler).Methods("POST")
	r.Handle("/metrics", promhttp.Handler())

	loggedHandler := handlers.LoggingHandler(os.Stdout, r)

	port := os.Getenv("PORT")
	srv := http.Server{
		Addr:         fmt.Sprintf(":%s", port),
		WriteTimeout: time.Second * 15,
		ReadTimeout:  time.Second * 15,
		IdleTimeout:  time.Second * 60,
		Handler:      loggedHandler,
	}

	errChan := make(chan error)

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errChan <- err
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	log.Printf("Server is listening on port %s (role: %s)\n", port, role)

	healthChan := make(chan interface{})

	// promoteChan is non-nil only for standby; a nil channel never fires in select.
	var promoteChan <-chan struct{}

	if role == "standby" {
		m.initFromPrimary(primaryAddr)
		ch := make(chan struct{}, 1)
		promoteChan = ch
		go m.monitorPrimary(primaryAddr, ch, healthChan)
		log.Printf("standby: monitoring primary at %s", primaryAddr)
	} else {
		servers := os.Getenv("AUX_SERVERS")
		for _, auxServer := range strings.Split(servers, ",") {
			m.hashring.AddNode(auxServer)
		}
		go m.RestoreCacheFromDisk()
		go m.HealthCheck(time.Second*5, healthChan)
	}

	defer func() {
		close(errChan)
		close(sigChan)
		close(healthChan)
	}()

	for {
		select {
		case err := <-errChan:
			log.Printf("error: %s\n", err.Error())
			healthChan <- struct{}{}
			return

		case <-sigChan:
			log.Println("shutting down...")
			healthChan <- struct{}{}

			ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
			defer cancel()

			if err := srv.Shutdown(ctx); err != nil {
				log.Printf("error: %s\n", err)
			}
			return

		case <-promoteChan:
			promoteChan = nil // prevent double-fire
			log.Println("promoted to primary, starting aux health checks")
			go m.HealthCheck(time.Second*5, healthChan)
		}
	}
}
