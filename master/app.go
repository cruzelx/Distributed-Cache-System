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
	// get aux server host and port from env
	servers := os.Getenv("AUX_SERVERS")
	auxServers := strings.Split(servers, ",")

	m := NewMaster()
	// add env aux host:port to hashring
	for _, auxServer := range auxServers {
		m.hashring.AddNode(auxServer)
	}

	// Restore and rebalance if backup file exists
	go m.RestoreCacheFromDisk()

	r := mux.NewRouter()
	r.Use(mux.CORSMethodMiddleware(r))

	// handlers
	r.HandleFunc("/data", m.Put).Methods("POST")
	r.HandleFunc("/data/{key}", m.Get).Methods("GET")

	// rebalance when a aux server is shutting down
	r.HandleFunc("/rebalance-dead-aux", m.RebalanceDeadAuxServer).Methods("POST")

	// instrumentation
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
		errChan <- srv.ListenAndServe()
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	log.Printf("Server is listening on the port %s\n", port)

	healthChan := make(chan interface{})
	go m.HealthCheck(time.Second*5, healthChan)

	select {
	case err := <-errChan:
		fmt.Printf("Error: %s\n", err)

		healthChan <- struct{}{}
		defer close(healthChan)

	case <-sigChan:
		fmt.Println("Shutting down successfully...")

		healthChan <- struct{}{}
		defer close(healthChan)

		ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
		defer cancel()

		if err := srv.Shutdown(ctx); err != nil {
			fmt.Printf("Error: %s\n", err)
		}
	}

}
