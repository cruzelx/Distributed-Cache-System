package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func Start() {

	// Load server configuration form env
	port := os.Getenv("PORT")
	serverId := os.Getenv("ID")

	// Path of the file that stores the cache for persistence
	filepath := "/data/" + serverId + "-" + "data.dat"

	aux := NewAuxiliary(3, filepath)

	// Check if the cache file already exists and load the data in LRU cache
	if ok, err := aux.LRU.loadFromDisk(); !ok {
		log.Println("error loading from disk:  ", err)
	} else {
		log.Println("cache loaded from the disk")
	}

	r := mux.NewRouter()
	r.Use(mux.CORSMethodMiddleware(r))

	// Handlers
	r.HandleFunc("/data", aux.Put).Methods("POST")
	r.HandleFunc("/data/{key}", aux.Get).Methods("GET")

	// Send all key-val mappings
	r.HandleFunc("/mappings", aux.Mappings).Methods("GET")

	// Empty the cache
	r.HandleFunc("/erase", aux.Erase).Methods("DELETE")

	// Monitor health to check alive status
	r.HandleFunc("/health", aux.Health).Methods("GET")

	// Instrumentation
	r.Handle("/metrics", promhttp.Handler())

	loggedHandler := handlers.LoggingHandler(os.Stdout, r)
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

	log.Println("aux is listening on the port ", port)

	// Save cache to disk every 10 sec for persistent storage
	go func() {
		for {
			time.Sleep(time.Second * 10)
			log.Println("saving cache to disk...")

			if ok, err := aux.LRU.saveToDisk(); !ok {
				log.Printf("failed saving cache to disk: %s\n", err.Error())
			}
		}
	}()

	// Listen to termination signals
	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)

	defer func() {
		close(errChan)
		close(shutdown)
	}()

	select {
	case err := <-errChan:
		log.Printf("error: %s\n", err.Error())
	case signal := <-shutdown:

		log.Printf("received signal: %v\n", signal)
		log.Println("shutting down...")
		log.Println("saving cache to disk...")

		if ok, err := aux.LRU.saveToDisk(); !ok {
			log.Printf("failed to save the cache to disk: %s", err.Error())
		}

		// send mappings to master before shutting down
		aux.SendMappings()

		ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
		defer cancel()

		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("error: %s\n", err.Error())
		}
	}

}
