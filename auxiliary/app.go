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
)

func Start() {
	// Load server configuration form env
	port := os.Getenv("PORT")
	serverId := os.Getenv("ID")

	// Path of the file that stores the cache for persistence
	filepath := "/data/" + serverId + "-" + "data.dat"

	// Init LRU cache with 3 replica set and the location of cache file
	lru := NewLRU(3, filepath)

	// Check if the cache file already exists and load the data in LRU cache
	if ok, err := lru.loadFromDisk(); !ok {
		log.Println("Error loading from disk:  ", err)
	} else {
		log.Println("Cache loaded from the disk")
	}

	aux := Auxiliary{
		LRU: lru,
	}

	// Connect to zookeeper servers

	manager := NewManager()
	defer manager.Close()

	if err := manager.CreateAuxiliaryNode(); err != nil {
		log.Printf("%s: failed to create auxiliary node in zookeeper: %v\n", serverId, err)
	}

	r := mux.NewRouter()
	r.Use(mux.CORSMethodMiddleware(r))

	r.HandleFunc("/data", aux.Put).Methods("POST")
	r.HandleFunc("/data/{key}", aux.Get).Methods("GET")
	r.HandleFunc("/mappings", aux.Mappings).Methods("GET")
	r.HandleFunc("/erase", aux.Erase).Methods("DELETE")
	r.HandleFunc("/health", aux.Health).Methods("GET")

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

	log.Println("Aux is listening on the port ", port)
	// Save cache to disk every 10 sec for persistent storage
	go func() {
		for {
			time.Sleep(time.Second * 10)
			log.Println("Saving cache to disk...")
			if ok, err := lru.saveToDisk(); !ok {
				log.Printf("Error saving to disk. Err: %s\n", err.Error())
			}
		}
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)

	go func() {
		signal := <-shutdown

		log.Printf("Received signal: %v\n", signal)
		log.Println("Shutting down successfully...")
		log.Println("Saving cache to disk...")

		if ok, err := lru.saveToDisk(); !ok {
			log.Printf("Couldn't save the cache on disk\n error: %s", err.Error())
		}

		aux.SendMappings()

		ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)

		defer func() {
			cancel()
			close(errChan)
		}()

		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("Error: %s\n", err)
			errChan <- err
		} else {
			log.Println("Shutdown complete")
			errChan <- nil
		}

	}()

	if err := <-errChan; err != nil {
		log.Printf("Error: %v\n", err)
	}

}
