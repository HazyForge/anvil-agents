package main

import (
	"flag"
	"log"
	"net/http"
	"os"

	"github.com/hazyforge/anvil-agents/internal/substrate"
)

func main() {
	listen := flag.String("listen", ":80", "Address the ACP echo actor listens on.")
	flag.Parse()
	mux := http.NewServeMux()
	mux.HandleFunc("/", substrate.ServeACPEcho)
	mux.HandleFunc("/healthz", substrate.ServeACPEcho)
	mux.HandleFunc("/readyz", substrate.ServeACPEcho)
	log.SetOutput(os.Stderr)
	log.Printf("anvil-actor-acp listening on %s", *listen)
	if err := http.ListenAndServe(*listen, mux); err != nil {
		log.Fatal(err)
	}
}
