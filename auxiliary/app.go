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

	// path of the file that stores the cache for persistence
	serverId := os.Getenv("ID")
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

	r := mux.NewRouter()
	r.Use(mux.CORSMethodMiddleware(r))

	r.HandleFunc("/data", aux.Put).Methods("POST")
	r.HandleFunc("/data/{key}", aux.Get).Methods("GET")

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

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGINT, syscall.SIGQUIT)

	go func() {
		<-sigCtx.Done()

		log.Println("Shutting down successfully...")
		log.Println("Saving cache to disk...")

		if ok, err := lru.saveToDisk(); !ok {
			log.Printf("Couldn't save the cache on disk\n error: %s", err.Error())
		}

		ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)

		defer func() {
			stop()
			cancel()
			close(errChan)
		}()

		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("Error: %s\n", err)
			errChan <- err
		} else {
			log.Println("Shutdown complete")
		}

	}()
	// sigChan := make(chan os.Signal, 1)
	// signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM, syscall.SIGINT)

	log.Println("Aux is listening on the port ", port)

	if err := <-errChan; err != nil {
		log.Fatalf("Error while running: %s", err)
	}

	// select {
	// case err := <-errChan:
	// 	fmt.Printf("Error: %s\n", err)
	// case sig := <-sigChan:
	// 	fmt.Printf("Received signal: %s\n", sig)
	// 	fmt.Println("Shutting down successfully...")
	// 	fmt.Println("Saving cache to disk...")

	// 	if ok, err := lru.saveToDisk(); !ok {
	// 		fmt.Printf("Couldn't save the cache on disk\n error: %s", err.Error())
	// 	}
	// 	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	// 	defer cancel()
	// 	if err := srv.Shutdown(ctx); err != nil {
	// 		fmt.Printf("Error: %s\n", err)
	// 	} else {
	// 		fmt.Println("Shutdown complete")
	// 	}
	// }

}
