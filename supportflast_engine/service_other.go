//go:build !windows

package main

import "net/http"

func runServer(server *http.Server) error {
	return server.ListenAndServe()
}

func runTLSServer(server *http.Server) error {
	return server.ListenAndServeTLS("", "")
}
