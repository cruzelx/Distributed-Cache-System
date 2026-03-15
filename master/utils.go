package main

import (
	"os"
	"strings"
)

func getAuxServers() []string {
	var result []string
	for _, s := range strings.Split(os.Getenv("AUX_SERVERS"), ",") {
		if s = strings.TrimSpace(s); s != "" {
			result = append(result, s)
		}
	}
	return result
}
