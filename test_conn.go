package main
import "fmt"
import "net/http"
import "time"
func main() {
    start := time.Now()
    client := &http.Client{Timeout: 3 * time.Second}
    req, _ := http.NewRequest("GET", "http://127.0.0.1:18110/", nil)
    resp, err := client.Do(req)
    fmt.Println(time.Since(start), resp, err)
}
