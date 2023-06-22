package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/gorilla/mux"
)

func Start() {
	r := mux.NewRouter()
	r.Use(mux.CORSMethodMiddleware(r))

	hr := NewHashRing(3)
	hr.AddNode("192.168.0.1:8081")
	hr.AddNode("192.168.0.2:8081")
	hr.AddNode("192.168.0.3:8081")

	m := Master{hashring: hr}

	r.HandleFunc("/data", m.Put).Methods("POST")
	r.HandleFunc("/data/{key}", m.Get).Methods("GET")

	srv := http.Server{
		Addr:         ":8080",
		WriteTimeout: time.Second * 15,
		ReadTimeout:  time.Second * 15,
		IdleTimeout:  time.Second * 60,
		Handler:      r,
	}

	errChan := make(chan error)

	go func() {
		errChan <- srv.ListenAndServe()
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt)

	log.Println("Server is listening on the port 8080")

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
