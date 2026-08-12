package main
import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func main() {
	start := time.Now()
	resp, err := http.Post("http://127.0.0.1:8080/api/lives", "application/json", strings.NewReader(`[{"url": "https://live.douyin.com/746226647909", "listen": true}]`))
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	body, _ := io.ReadAll(resp.Body)
	fmt.Println("Time:", time.Since(start), "Status:", resp.StatusCode, "Body:", string(body))
}
