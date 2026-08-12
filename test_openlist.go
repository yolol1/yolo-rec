package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// 这是一个独立的诊断脚本，用于直接测试 OpenList API 连通性
// 用法: go run test_openlist.go <api_url> <username> <password>
// 示例: go run test_openlist.go http://127.0.0.1:5244 admin 123456

func main() {
	if len(os.Args) < 4 {
		fmt.Println("用法: go run test_openlist.go <api_url> <username> <password>")
		fmt.Println("示例: go run test_openlist.go http://127.0.0.1:5244 admin 123456")
		os.Exit(1)
	}

	apiURL := os.Args[1]
	username := os.Args[2]
	password := os.Args[3]

	client := &http.Client{Timeout: 10 * time.Second}

	// === 第1步：检查服务是否可达 ===
	fmt.Println("=== 第1步：检查 OpenList 服务是否可达 ===")
	resp, err := client.Get(apiURL + "/api/public/settings")
	if err != nil {
		fmt.Printf("❌ 无法连接到 %s: %v\n", apiURL, err)
		os.Exit(1)
	}
	resp.Body.Close()
	if resp.StatusCode == 200 {
		fmt.Printf("✅ 服务可达 (HTTP %d)\n", resp.StatusCode)
	} else {
		fmt.Printf("⚠️  服务返回了非 200 状态码: %d\n", resp.StatusCode)
	}

	// === 第2步：测试登录获取 Token ===
	fmt.Println("\n=== 第2步：测试登录获取 Token ===")
	loginBody := fmt.Sprintf(`{"username":"%s","password":"%s"}`, username, password)
	req, _ := http.NewRequest("POST", apiURL+"/api/auth/login", bytes.NewReader([]byte(loginBody)))
	req.Header.Set("Content-Type", "application/json")

	resp, err = client.Do(req)
	if err != nil {
		fmt.Printf("❌ 登录请求失败: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	fmt.Printf("   HTTP 状态码: %d\n", resp.StatusCode)
	fmt.Printf("   响应内容: %s\n", string(body))

	var loginResult struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			Token string `json:"token"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &loginResult); err != nil {
		fmt.Printf("❌ 解析登录响应失败: %v\n", err)
		os.Exit(1)
	}

	if loginResult.Code != 200 {
		fmt.Printf("❌ 登录失败: code=%d, message=%s\n", loginResult.Code, loginResult.Message)
		os.Exit(1)
	}

	token := loginResult.Data.Token
	fmt.Printf("✅ 登录成功! Token 前20字符: %s...\n", token[:min(20, len(token))])

	// === 第3步：测试创建目录 ===
	fmt.Println("\n=== 第3步：测试创建目录 ===")
	testDir := "/yolo-rec-test-" + fmt.Sprintf("%d", time.Now().Unix())
	mkdirBody := fmt.Sprintf(`{"path":"%s"}`, testDir)
	req, _ = http.NewRequest("POST", apiURL+"/api/fs/mkdir", bytes.NewReader([]byte(mkdirBody)))
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")

	resp, err = client.Do(req)
	if err != nil {
		fmt.Printf("❌ 创建目录请求失败: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ = io.ReadAll(resp.Body)
	fmt.Printf("   请求路径: %s\n", testDir)
	fmt.Printf("   HTTP 状态码: %d\n", resp.StatusCode)
	fmt.Printf("   响应内容: %s\n", string(body))

	var mkdirResult struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	}
	json.Unmarshal(body, &mkdirResult)

	if mkdirResult.Code == 200 {
		fmt.Printf("✅ 创建目录成功: %s\n", testDir)
	} else {
		fmt.Printf("❌ 创建目录失败: code=%d, message=%s\n", mkdirResult.Code, mkdirResult.Message)
	}

	// === 第4步：列出根目录，验证目录是否存在 ===
	fmt.Println("\n=== 第4步：列出根目录验证 ===")
	listBody := `{"path":"/","refresh":true}`
	req, _ = http.NewRequest("POST", apiURL+"/api/fs/list", bytes.NewReader([]byte(listBody)))
	req.Header.Set("Authorization", token)
	req.Header.Set("Content-Type", "application/json")

	resp, err = client.Do(req)
	if err != nil {
		fmt.Printf("❌ 列出目录请求失败: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	body, _ = io.ReadAll(resp.Body)
	fmt.Printf("   HTTP 状态码: %d\n", resp.StatusCode)
	fmt.Printf("   响应内容 (截取前500字符): %.500s\n", string(body))

	fmt.Println("\n=== 诊断完成 ===")
	fmt.Println("如果以上步骤全部成功，说明 OpenList API 连通正常。")
	fmt.Println("如果创建目录失败，可能是因为根目录下没有可写的存储挂载点。")
	fmt.Println("建议在存储名称下创建目录，例如: /<你的存储名称>/yolo-rec-test")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
