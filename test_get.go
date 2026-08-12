package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

func main() {
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get("http://127.0.0.1:8080/api/lives")
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	body, _ := io.ReadAll(resp.Body)
	
	var lives []map[string]interface{}
	json.Unmarshal(body, &lives)
	
	fmt.Printf("Total rooms: %d\n\n", len(lives))
	for _, l := range lives {
		url := l["live_url"]
		hostName := l["host_name"]
		initializing := l["initializing"]
		listening := l["listening"]
		id := l["id"]
		fmt.Printf("ID: %s\n  URL: %s\n  Host: %s\n  Initializing: %v\n  Listening: %v\n\n", id, url, hostName, initializing, listening)
	}
}
