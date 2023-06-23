package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/gorilla/handlers"
	"github.com/gorilla/mux"
)

func Start() {
	r := mux.NewRouter()
	r.Use(mux.CORSMethodMiddleware(r))

	lru := NewLRU(3)
	aux := Auxiliary{
		LRU: lru,
	}

	r.HandleFunc("/data", aux.Put).Methods("POST")
	r.HandleFunc("/data/{key}", aux.Get).Methods("GET")

	port := os.Getenv("PORT")

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
		errChan <- srv.ListenAndServe()
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)

	log.Println("Aux is listening on the port ", port)

	select {
	case err := <-errChan:
		fmt.Printf("Error: %s\n", err)
	case <-sigChan:
		fmt.Println("Shutting down successfully...")
		ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			fmt.Printf("Error: %s\n", err)
		}
	}

}
