// Command chaos toggles chaos switches on a running mock API.
//
//	go run ./cmd/chaos                  # show active + available switches
//	go run ./cmd/chaos rename_field     # set active switches (replaces current set)
//	go run ./cmd/chaos --reset          # turn everything off
package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
)

func main() {
	url := cmp.Or(os.Getenv("SHOP_API_URL"), "http://127.0.0.1:8100") + "/_chaos"
	args := os.Args[1:]
	var resp *http.Response
	var err error
	if len(args) == 0 {
		resp, err = http.Get(url)
	} else {
		active := args
		if len(args) == 1 && args[0] == "--reset" {
			active = []string{}
		}
		body, _ := json.Marshal(map[string][]string{"active": active})
		req, _ := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err = http.DefaultClient.Do(req)
	}
	if err != nil {
		log.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	fmt.Println(resp.Status, string(out))
	if resp.StatusCode >= 300 {
		os.Exit(1)
	}
}
