package servers

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/bililive-go/bililive-go/src/consts"
)

// RemoteWebuiStatusResponse 远程 WebUI 状态响应
type RemoteWebuiStatusResponse struct {
	Available          bool                   `json:"available"`
	AppVersion         string                 `json:"app_version"`
	RemoteUIVersion    string                 `json:"remote_ui_version,omitempty"`
	LocalUIVersion     string                 `json:"local_ui_version,omitempty"`
	RemoteUIURL        string                 `json:"remote_ui_url,omitempty"`
	Error              string                 `json:"error,omitempty"`
	LastCheck          string                 `json:"last_check,omitempty"`
	RemoteWebuiBaseURL string                 `json:"remote_webui_base_url"`
	Status             map[string]interface{} `json:"status,omitempty"`
}

// RemoteWebuiInfo 远程 WebUI 信息
type RemoteWebuiInfo struct {
	UIVersion string `json:"uiVersion"`
	IndexURL  string `json:"indexUrl"`
	WebuiPath string `json:"webuiPath"`
}

// getRemoteWebuiStatus 获取远程 WebUI 状态
func getRemoteWebuiStatus(writer http.ResponseWriter, r *http.Request) {
	response := RemoteWebuiStatusResponse{
		Available:          false,
		AppVersion:         consts.AppVersion,
		RemoteWebuiBaseURL: "https://bililive-go.com",
	}

	response.LocalUIVersion = getLocalUIVersion()

	remoteInfo, err := fetchRemoteWebuiInfo(consts.AppVersion)
	if err != nil {
		response.Error = err.Error()
		writeJSON(writer, response)
		return
	}

	response.Available = true
	response.RemoteUIVersion = remoteInfo.UIVersion
	response.RemoteUIURL = remoteInfo.IndexURL
	response.LastCheck = time.Now().Format(time.RFC3339)

	writeJSON(writer, response)
}

// fetchRemoteWebuiInfo 从远程获取 WebUI 信息
func fetchRemoteWebuiInfo(appVersion string) (*RemoteWebuiInfo, error) {
	apiURL := fmt.Sprintf("https://bililive-go.com/api/webui?appversion=%s", url.QueryEscape(appVersion))

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch remote webui info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("remote API returned status %d: %s", resp.StatusCode, string(body))
	}

	var result struct {
		SelectedVersion struct {
			UIVersion string `json:"uiVersion"`
		} `json:"selectedVersion"`
		IndexURL  string `json:"indexUrl"`
		WebuiPath string `json:"webuiPath"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode remote webui info: %w", err)
	}

	return &RemoteWebuiInfo{
		UIVersion: result.SelectedVersion.UIVersion,
		IndexURL:  result.IndexURL,
		WebuiPath: result.WebuiPath,
	}, nil
}

// getLocalUIVersion 获取本地 UI 版本
func getLocalUIVersion() string {
	return "1.0.0" // TODO: 从嵌入的资源中读取
}

// checkRemoteWebuiUpdate 检查远程 WebUI 是否有更新
func checkRemoteWebuiUpdate(writer http.ResponseWriter, r *http.Request) {
	localVersion := getLocalUIVersion()

	remoteInfo, err := fetchRemoteWebuiInfo(consts.AppVersion)
	if err != nil {
		writeJSON(writer, map[string]interface{}{
			"has_update":    false,
			"error":         err.Error(),
			"local_version": localVersion,
			"app_version":   consts.AppVersion,
		})
		return
	}

	hasUpdate := remoteInfo.UIVersion != localVersion && remoteInfo.UIVersion != ""

	writeJSON(writer, map[string]interface{}{
		"has_update":     hasUpdate,
		"local_version":  localVersion,
		"remote_version": remoteInfo.UIVersion,
		"remote_url":     remoteInfo.IndexURL,
		"app_version":    consts.AppVersion,
	})
}
