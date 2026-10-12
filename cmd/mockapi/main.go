// Command mockapi runs the mock shop API.
package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/revolutionrace/mcp-drift-investigation/internal/mockapi"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8100", "listen address")
	flag.Parse()
	log.Printf("mock shop API on http://%s", *addr)
	log.Fatal(http.ListenAndServe(*addr, mockapi.New().Handler()))
}
