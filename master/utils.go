package main

import (
	"os"
	"strings"
)

func getAuxServers() []string {
	servers := os.Getenv("AUX_SERVERS")
	auxServers := strings.Split(servers, ",")

	return auxServers
}
