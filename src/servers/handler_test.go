package servers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bililive-go/bililive-go/src/configs"
)

func TestGetSoopLiveAuthConfigDoesNotExposeSavedPassword(t *testing.T) {
	cfg := configs.NewConfig()
	cfg.SoopLiveAuth.Username = "tester"
	cfg.SoopLiveAuth.Password = "secret"
	configs.SetCurrentConfig(cfg)

	recorder := httptest.NewRecorder()
	getSoopLiveAuthConfig(recorder, nil)

	assert.Equal(t, 200, recorder.Code)

	var resp commonResp
	err := json.Unmarshal(recorder.Body.Bytes(), &resp)
	assert.NoError(t, err)

	data, ok := resp.Data.(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "tester", data["username"])
	assert.Equal(t, true, data["has_saved_credentials"])
	_, exists := data["password"]
	assert.False(t, exists)
}

// TestUpdateConfigCloudUploadFull 测试完整云上传配置的保存和读取
func TestUpdateConfigCloudUploadFull(t *testing.T) {
	cfg := configs.NewConfig()
	cfg.OutPutPath = t.TempDir()
	configs.SetCurrentConfig(cfg)

	// 构建完整的云上传配置更新请求
	updateBody := map[string]any{
		"on_record_finished": map[string]any{
			"cloud_upload": map[string]any{
				"enable":              true,
				"api_url":             "http://192.168.1.100:5244",
				"username":            "admin",
				"password":            "secret123",
				"storage_name":        "aliyun",
				"upload_path_tmpl":    "/录播/{{ .Platform }}/{{ .HostName }}.mp4",
				"delete_after_upload": true,
				"additional_storages": []any{"baidu", "115"},
			},
			"upload_timing": "after_process",
			"save_cover":    true,
		},
	}

	bodyBytes, err := json.Marshal(updateBody)
	require.NoError(t, err)

	// 发送 PATCH 请求保存配置
	patchReq := httptest.NewRequest(http.MethodPatch, "/api/config", bytes.NewReader(bodyBytes))
	patchReq.Header.Set("Content-Type", "application/json")
	patchRecorder := httptest.NewRecorder()
	updateConfig(patchRecorder, patchReq)

	assert.Equal(t, 200, patchRecorder.Code)

	// 读取生效配置，验证所有字段
	getRecorder := httptest.NewRecorder()
	getReq := httptest.NewRequest(http.MethodGet, "/api/config/effective", nil)
	getEffectiveConfig(getRecorder, getReq)
	assert.Equal(t, 200, getRecorder.Code)

	var resp EffectiveConfigResponse
	err = json.Unmarshal(getRecorder.Body.Bytes(), &resp)
	require.NoError(t, err)

	cu := resp.OnRecordFinished.CloudUpload
	assert.True(t, cu.Enable, "enable 应为 true")
	assert.Equal(t, "http://192.168.1.100:5244", cu.ApiUrl, "api_url 不匹配")
	assert.Equal(t, "admin", cu.Username, "username 不匹配")
	assert.Equal(t, "secret123", cu.Password, "password 不匹配")
	assert.Equal(t, "aliyun", cu.StorageName, "storage_name 不匹配")
	assert.Equal(t, "/录播/{{ .Platform }}/{{ .HostName }}.mp4", cu.UploadPathTmpl, "upload_path_tmpl 不匹配")
	assert.True(t, cu.DeleteAfterUpload, "delete_after_upload 应为 true")
	assert.Equal(t, []string{"baidu", "115"}, cu.AdditionalStorages, "additional_storages 不匹配")

	assert.Equal(t, configs.UploadTiming("after_process"), resp.OnRecordFinished.UploadTiming, "upload_timing 不匹配")
	assert.True(t, resp.OnRecordFinished.SaveCover, "save_cover 应为 true")
}

// TestUpdateConfigCloudUploadPartial 测试部分更新云上传配置
func TestUpdateConfigCloudUploadPartial(t *testing.T) {
	cfg := configs.NewConfig()
	cfg.OutPutPath = t.TempDir()
	configs.SetCurrentConfig(cfg)

	// 先保存完整配置
	fullUpdate := map[string]any{
		"on_record_finished": map[string]any{
			"cloud_upload": map[string]any{
				"enable":       true,
				"api_url":      "http://192.168.1.100:5244",
				"username":     "admin",
				"password":     "secret123",
				"storage_name": "aliyun",
			},
		},
	}
	bodyBytes, _ := json.Marshal(fullUpdate)
	patchReq := httptest.NewRequest(http.MethodPatch, "/api/config", bytes.NewReader(bodyBytes))
	patchReq.Header.Set("Content-Type", "application/json")
	updateConfig(httptest.NewRecorder(), patchReq)

	// 只更新 enable 为 false
	partialUpdate := map[string]any{
		"on_record_finished": map[string]any{
			"cloud_upload": map[string]any{
				"enable": false,
			},
		},
	}
	bodyBytes, _ = json.Marshal(partialUpdate)
	patchReq = httptest.NewRequest(http.MethodPatch, "/api/config", bytes.NewReader(bodyBytes))
	patchReq.Header.Set("Content-Type", "application/json")
	updateConfig(httptest.NewRecorder(), patchReq)

	// 验证：enable 应为 false，其他字段保持不变
	getRecorder := httptest.NewRecorder()
	getReq := httptest.NewRequest(http.MethodGet, "/api/config/effective", nil)
	getEffectiveConfig(getRecorder, getReq)

	var resp EffectiveConfigResponse
	json.Unmarshal(getRecorder.Body.Bytes(), &resp)

	cu := resp.OnRecordFinished.CloudUpload
	assert.False(t, cu.Enable, "enable 应被更新为 false")
	assert.Equal(t, "http://192.168.1.100:5244", cu.ApiUrl, "api_url 应保持不变")
	assert.Equal(t, "admin", cu.Username, "username 应保持不变")
	assert.Equal(t, "secret123", cu.Password, "password 应保持不变")
	assert.Equal(t, "aliyun", cu.StorageName, "storage_name 应保持不变")
}

// TestUpdateConfigCloudUploadAdditionalStoragesEmpty 测试清空额外存储列表
func TestUpdateConfigCloudUploadAdditionalStoragesEmpty(t *testing.T) {
	cfg := configs.NewConfig()
	cfg.OutPutPath = t.TempDir()
	configs.SetCurrentConfig(cfg)

	// 先保存带额外存储的配置
	fullUpdate := map[string]any{
		"on_record_finished": map[string]any{
			"cloud_upload": map[string]any{
				"enable":              true,
				"storage_name":        "aliyun",
				"additional_storages": []any{"baidu", "115"},
			},
		},
	}
	bodyBytes, _ := json.Marshal(fullUpdate)
	patchReq := httptest.NewRequest(http.MethodPatch, "/api/config", bytes.NewReader(bodyBytes))
	patchReq.Header.Set("Content-Type", "application/json")
	updateConfig(httptest.NewRecorder(), patchReq)

	// 清空额外存储
	emptyUpdate := map[string]any{
		"on_record_finished": map[string]any{
			"cloud_upload": map[string]any{
				"additional_storages": []any{},
			},
		},
	}
	bodyBytes, _ = json.Marshal(emptyUpdate)
	patchReq = httptest.NewRequest(http.MethodPatch, "/api/config", bytes.NewReader(bodyBytes))
	patchReq.Header.Set("Content-Type", "application/json")
	updateConfig(httptest.NewRecorder(), patchReq)

	// 验证额外存储已被清空
	getRecorder := httptest.NewRecorder()
	getReq := httptest.NewRequest(http.MethodGet, "/api/config/effective", nil)
	getEffectiveConfig(getRecorder, getReq)

	var resp EffectiveConfigResponse
	json.Unmarshal(getRecorder.Body.Bytes(), &resp)

	assert.Empty(t, resp.OnRecordFinished.CloudUpload.AdditionalStorages, "additional_storages 应为空")
	assert.True(t, resp.OnRecordFinished.CloudUpload.Enable, "enable 应保持不变")
}

// TestUpdateConfigCloudUploadDefaultValues 测试默认零值配置能正确返回
func TestUpdateConfigCloudUploadDefaultValues(t *testing.T) {
	cfg := configs.NewConfig()
	cfg.OutPutPath = t.TempDir()
	configs.SetCurrentConfig(cfg)

	// 不设置任何云上传配置，直接读取生效配置
	getRecorder := httptest.NewRecorder()
	getReq := httptest.NewRequest(http.MethodGet, "/api/config/effective", nil)
	getEffectiveConfig(getRecorder, getReq)
	assert.Equal(t, 200, getRecorder.Code)

	var resp EffectiveConfigResponse
	err := json.Unmarshal(getRecorder.Body.Bytes(), &resp)
	require.NoError(t, err)

	// 验证默认值：cloud_upload 结构体存在，但字段为零值
	cu := resp.OnRecordFinished.CloudUpload
	assert.False(t, cu.Enable, "默认 enable 应为 false")
	assert.Empty(t, cu.ApiUrl, "默认 api_url 应为空")
	assert.Empty(t, cu.Username, "默认 username 应为空")
	assert.Empty(t, cu.Password, "默认 password 应为空")
	assert.Empty(t, cu.StorageName, "默认 storage_name 应为空")
	assert.NotEmpty(t, cu.UploadPathTmpl, "默认 upload_path_tmpl 不应为空（有默认值）")
	assert.False(t, cu.DeleteAfterUpload, "默认 delete_after_upload 应为 false")
	assert.Nil(t, cu.AdditionalStorages, "默认 additional_storages 应为 nil")
}

// TestUpdateConfigUploadTiming 测试上传时机各值的保存
func TestUpdateConfigUploadTiming(t *testing.T) {
	cfg := configs.NewConfig()
	cfg.OutPutPath = t.TempDir()
	configs.SetCurrentConfig(cfg)

	tests := []struct {
		name     string
		timing   string
		expected configs.UploadTiming
	}{
		{"immediate", "immediate", configs.UploadTimingImmediate},
		{"after_process", "after_process", configs.UploadTimingAfterProcess},
		{"empty", "", configs.UploadTiming("")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			updateBody := map[string]any{
				"on_record_finished": map[string]any{
					"upload_timing": tt.timing,
				},
			}
			bodyBytes, _ := json.Marshal(updateBody)
			patchReq := httptest.NewRequest(http.MethodPatch, "/api/config", bytes.NewReader(bodyBytes))
			patchReq.Header.Set("Content-Type", "application/json")
			updateConfig(httptest.NewRecorder(), patchReq)

			getRecorder := httptest.NewRecorder()
			getReq := httptest.NewRequest(http.MethodGet, "/api/config/effective", nil)
			getEffectiveConfig(getRecorder, getReq)

			var resp EffectiveConfigResponse
			json.Unmarshal(getRecorder.Body.Bytes(), &resp)
			assert.Equal(t, tt.expected, resp.OnRecordFinished.UploadTiming)
		})
	}
}
