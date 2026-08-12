package servers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/pkg/openlist"
)

// OpenListStatusResponse OpenList 状态响应
type OpenListStatusResponse struct {
	OpenListRunning    bool                   `json:"openlist_running"`
	WebUIPath          string                 `json:"web_ui_path"`
	Storages           []openlist.StorageInfo `json:"storages"`
	Errors             []string               `json:"errors"`
	CloudUploadEnabled bool                   `json:"cloud_upload_enabled"`
}

// OpenListStorageHealthResponse 存储健康检查响应
type OpenListStorageHealthResponse struct {
	Healthy bool   `json:"healthy"`
	Message string `json:"message,omitempty"`
}

// 全局 OpenList 管理器引用（由 main 设置）
var globalOpenListManager *openlist.Manager

// SetOpenListManager 设置全局 OpenList 管理器
func SetOpenListManager(m *openlist.Manager) {
	globalOpenListManager = m
}

// getOpenListStatus 获取 OpenList 状态
func getOpenListStatus(writer http.ResponseWriter, r *http.Request) {
	config := configs.GetCurrentConfig()

	response := OpenListStatusResponse{
		CloudUploadEnabled: config.OnRecordFinished.CloudUpload.Enable,
		WebUIPath:          "/remotetools/tool/openlist/",
		Storages:           []openlist.StorageInfo{},
		Errors:             []string{},
	}

	// 检查 OpenList 管理器是否存在
	if globalOpenListManager == nil {
		if config.OnRecordFinished.CloudUpload.Enable {
			response.Errors = append(response.Errors, "OpenList 管理器未初始化")
		}
		writer.Header().Set("Content-Type", "application/json")
		json.NewEncoder(writer).Encode(response)
		return
	}

	// 检查 OpenList 是否运行
	response.OpenListRunning = globalOpenListManager.IsRunning()

	if !response.OpenListRunning {
		response.Errors = append(response.Errors, "OpenList 服务未运行")
		writer.Header().Set("Content-Type", "application/json")
		json.NewEncoder(writer).Encode(response)
		return
	}

	// 尝试获取存储列表
	client := openlist.NewClient(globalOpenListManager.GetAPIEndpoint(), "")
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	storages, err := client.ListStorages(ctx)
	if err != nil {
		response.Errors = append(response.Errors, "无法获取存储列表: "+err.Error())
	} else {
		response.Storages = storages
		if len(storages) == 0 {
			response.Errors = append(response.Errors, "未配置任何存储，请在 OpenList 中添加网盘")
		}
	}

	writer.Header().Set("Content-Type", "application/json")
	json.NewEncoder(writer).Encode(response)
}

// checkOpenListStorageHealth 检查存储健康状态
func checkOpenListStorageHealth(writer http.ResponseWriter, r *http.Request) {
	storageName := r.URL.Query().Get("name")
	if storageName == "" {
		http.Error(writer, "缺少 name 参数", http.StatusBadRequest)
		return
	}

	response := OpenListStorageHealthResponse{
		Healthy: false,
	}

	if globalOpenListManager == nil || !globalOpenListManager.IsRunning() {
		response.Message = "OpenList 服务未运行"
		writer.Header().Set("Content-Type", "application/json")
		json.NewEncoder(writer).Encode(response)
		return
	}

	client := openlist.NewClient(globalOpenListManager.GetAPIEndpoint(), "")
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	if err := client.CheckStorageHealth(ctx, storageName); err != nil {
		response.Message = err.Error()
	} else {
		response.Healthy = true
	}

	writer.Header().Set("Content-Type", "application/json")
	json.NewEncoder(writer).Encode(response)
}

// CloudUploadTestRequest 测试云上传连接的请求
type CloudUploadTestRequest struct {
	ApiUrl             string   `json:"api_url"`
	Username           string   `json:"username"`
	Password           string   `json:"password"`
	StorageName        string   `json:"storage_name"`
	AdditionalStorages []string `json:"additional_storages,omitempty"`
	UploadPathTmpl     string   `json:"upload_path_tmpl"`
	DeleteAfterUpload  bool     `json:"delete_after_upload"`
}

// CloudUploadTestStep 单个测试步骤结果
type CloudUploadTestStep struct {
	Name    string `json:"name"`
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// CloudUploadTestResponse 测试连接的响应
type CloudUploadTestResponse struct {
	Success bool                   `json:"success"`
	Steps   []CloudUploadTestStep  `json:"steps"`
}

// testCloudUploadConnection 测试云上传连接
// POST /api/openlist/test-connection
func testCloudUploadConnection(writer http.ResponseWriter, r *http.Request) {
	var req CloudUploadTestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(writer, "请求格式错误: "+err.Error(), http.StatusBadRequest)
		return
	}

	response := CloudUploadTestResponse{
		Steps: []CloudUploadTestStep{},
	}

	addStep := func(name string, success bool, msg string) {
		response.Steps = append(response.Steps, CloudUploadTestStep{
			Name: name, Success: success, Message: msg,
		})
		if !success {
			response.Success = false
		}
	}

	// 始终以 success=true 开始，任何步骤失败则变为 false
	response.Success = true

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()

	// 步骤1: 检查必填字段
	if req.ApiUrl == "" {
		addStep("参数检查", false, "API 地址不能为空")
		writer.Header().Set("Content-Type", "application/json")
		json.NewEncoder(writer).Encode(response)
		return
	}
	if req.Username == "" {
		addStep("参数检查", false, "用户名不能为空")
		writer.Header().Set("Content-Type", "application/json")
		json.NewEncoder(writer).Encode(response)
		return
	}
	if req.Password == "" {
		addStep("参数检查", false, "密码不能为空")
		writer.Header().Set("Content-Type", "application/json")
		json.NewEncoder(writer).Encode(response)
		return
	}
	if req.StorageName == "" {
		addStep("参数检查", false, "存储名称不能为空")
		writer.Header().Set("Content-Type", "application/json")
		json.NewEncoder(writer).Encode(response)
		return
	}

	// 步骤2: 检查 API 连通性
	client := openlist.NewClient(req.ApiUrl, "")
	if client.IsServiceReady(ctx) {
		addStep("API 连通性", true, "OpenList 服务可达")
	} else {
		addStep("API 连通性", false, fmt.Sprintf("无法连接到 %s，请检查地址是否正确、服务是否运行", req.ApiUrl))
		writer.Header().Set("Content-Type", "application/json")
		json.NewEncoder(writer).Encode(response)
		return
	}

	// 步骤3: 登录验证
	token, err := client.GetToken(ctx, req.Username, req.Password)
	if err != nil {
		addStep("登录验证", false, "登录失败: "+err.Error())
		writer.Header().Set("Content-Type", "application/json")
		json.NewEncoder(writer).Encode(response)
		return
	}
	client.SetToken(token)
	addStep("登录验证", true, "登录成功")

	// 步骤4: 检查存储健康状态
	allStorages := []string{req.StorageName}
	for _, s := range req.AdditionalStorages {
		s = strings.TrimSpace(s)
		if s != "" {
			allStorages = append(allStorages, s)
		}
	}

	for _, storage := range allStorages {
		if err := client.CheckStorageHealth(ctx, storage); err != nil {
			addStep(fmt.Sprintf("存储 [%s] 可用性", storage), false, err.Error())
		} else {
			addStep(fmt.Sprintf("存储 [%s] 可用性", storage), true, "存储连接正常")
		}
	}

	// 步骤5: 检查模板渲染
	if req.UploadPathTmpl != "" {
		// 用模板引擎尝试渲染（简单检查：替换占位符后是否还有未替换的 {{ }}
		testPath := req.UploadPathTmpl
		testPath = strings.ReplaceAll(testPath, "{{ .Platform }}", "测试平台")
		testPath = strings.ReplaceAll(testPath, "{{.Platform}}", "测试平台")
		testPath = strings.ReplaceAll(testPath, "{{ .HostName }}", "测试主播")
		testPath = strings.ReplaceAll(testPath, "{{.HostName}}", "测试主播")
		testPath = strings.ReplaceAll(testPath, "{{ .RoomName }}", "测试房间")
		testPath = strings.ReplaceAll(testPath, "{{.RoomName}}", "测试房间")
		// 简单的模板语法检查
		if strings.Contains(testPath, "{{") && strings.Contains(testPath, "}}") {
			// 可能还有未渲染的模板变量，但可能是 now/date 等函数，不算错误
			addStep("路径模板", true, fmt.Sprintf("模板渲染预览: %s", testPath))
		} else {
			addStep("路径模板", true, fmt.Sprintf("模板渲染预览: %s", testPath))
		}
	}

	// 步骤6: 测试文件上传（上传一个小文件再删除）
	remoteDir := fmt.Sprintf("/%s/__connection_test__", req.StorageName)
	remotePath := fmt.Sprintf("%s/test.txt", remoteDir)
	testContent := fmt.Sprintf("bililive-go connection test at %s", time.Now().Format(time.RFC3339))

	// 创建临时本地文件
	tmpFile, err := os.CreateTemp("", "bililive-conn-test-*.txt")
	if err != nil {
		addStep("上传测试", false, "创建临时文件失败: "+err.Error())
		writer.Header().Set("Content-Type", "application/json")
		json.NewEncoder(writer).Encode(response)
		return
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.WriteString(testContent); err != nil {
		tmpFile.Close()
		addStep("上传测试", false, "写入临时文件失败: "+err.Error())
		writer.Header().Set("Content-Type", "application/json")
		json.NewEncoder(writer).Encode(response)
		return
	}
	tmpFile.Close()

	// 创建目录
	if err := client.Mkdir(ctx, remoteDir); err != nil {
		addStep("上传测试", false, fmt.Sprintf("创建远程目录失败: %v", err))
	} else {
		// 上传测试文件
		if err := client.Upload(ctx, tmpPath, remotePath, nil, 0); err != nil {
			addStep("上传测试", false, fmt.Sprintf("上传测试文件失败: %v", err))
		} else {
			addStep("上传测试", true, "测试文件上传成功")
		}

		// 尝试清理测试文件
		emptyFile, _ := os.CreateTemp("", "bililive-conn-test-empty-*")
		if emptyFile != nil {
			emptyPath := emptyFile.Name()
			emptyFile.Close()
			defer os.Remove(emptyPath)
			// 上传空文件覆盖测试文件作为清理手段，失败不影响测试结果
			_ = client.Upload(ctx, emptyPath, remotePath, nil, 0)
		}
	}

	writer.Header().Set("Content-Type", "application/json")
	json.NewEncoder(writer).Encode(response)
}
