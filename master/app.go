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
)

func Start() {
	r := mux.NewRouter()
	r.Use(mux.CORSMethodMiddleware(r))

	value := os.Getenv("AUX_SERVERS")
	aux_servers := strings.Split(value, ",")

	hr := NewHashRing(3)
	for _, aux_server := range aux_servers {
		hr.AddNode(aux_server)
	}
	m := Master{hashring: hr}

	r.HandleFunc("/data", m.Put).Methods("POST")
	r.HandleFunc("/data/{key}", m.Get).Methods("GET")

	loggedHandler := handlers.LoggingHandler(os.Stdout, r)

	srv := http.Server{
		Addr:         ":8000",
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

	log.Println("Server is listening on the port 8000")

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
