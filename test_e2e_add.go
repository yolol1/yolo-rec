package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type LiveInfo struct {
	ID           string `json:"id"`
	LiveURL      string `json:"live_url"`
	HostName     string `json:"host_name"`
	RoomName     string `json:"room_name"`
	Status       bool   `json:"status"`
	Listening    bool   `json:"listening"`
	Recording    bool   `json:"recording"`
	Initializing bool   `json:"initializing"`
	AutoRecord   bool   `json:"auto_record"`
}

func main() {
	client := &http.Client{Timeout: 30 * time.Second}
	targetURL := "https://live.douyin.com/746226647909"
	
	fmt.Println("=== 步骤 1: 获取当前列表 ===")
	lives := getLives(client)
	found := false
	for _, l := range lives {
		if l.LiveURL == targetURL {
			fmt.Printf("  发现目标房间! ID=%s, HostName=%s, Initializing=%v, Listening=%v\n", l.ID, l.HostName, l.Initializing, l.Listening)
			found = true
		}
	}
	if !found {
		fmt.Println("  目标房间不在当前列表中")
	}
	fmt.Printf("  列表总数: %d\n\n", len(lives))
	
	fmt.Println("=== 步骤 2: 添加直播间 ===")
	start := time.Now()
	resp, err := client.Post("http://127.0.0.1:8080/api/lives", "application/json",
		strings.NewReader(fmt.Sprintf(`[{"url": "%s", "listen": true}]`, targetURL)))
	elapsed := time.Since(start)
	if err != nil {
		fmt.Printf("  POST 失败: %v\n", err)
		return
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	fmt.Printf("  耗时: %v, 状态码: %d\n", elapsed, resp.StatusCode)
	fmt.Printf("  响应: %s\n\n", string(body))
	
	fmt.Println("=== 步骤 3: 再次获取列表 ===")
	lives2 := getLives(client)
	found2 := false
	for _, l := range lives2 {
		if l.LiveURL == targetURL {
			fmt.Printf("  发现目标房间! ID=%s, HostName=%s, Initializing=%v, Listening=%v\n", l.ID, l.HostName, l.Initializing, l.Listening)
			found2 = true
		}
	}
	if !found2 {
		fmt.Println("  !!! 目标房间不在列表中 — 这就是 BUG !!!")
	}
	fmt.Printf("  列表总数: %d\n", len(lives2))
}

func getLives(client *http.Client) []LiveInfo {
	resp, err := client.Get("http://127.0.0.1:8080/api/lives")
	if err != nil {
		fmt.Printf("  GET 失败: %v\n", err)
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var lives []LiveInfo
	json.Unmarshal(body, &lives)
	return lives
}
