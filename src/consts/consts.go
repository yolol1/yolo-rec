package consts

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const (
	AppName = "BiliLive-go"
)

const (
	LiveStatusStart = "start"
	LiveStatusStop  = "stop"
)

type Info struct {
	AppName    string `json:"app_name"`
	AppVersion string `json:"app_version"`
	BuildTime  string `json:"build_time"`
	GitHash    string `json:"git_hash"`
	Pid        int    `json:"pid"`
	Platform   string `json:"platform"`
	GoVersion  string `json:"go_version"`
	BgoExePath string `json:"bgo_exe_path"`
}

var (
	BuildTime  string
	AppVersion string
	GitHash    string
)

// GetAppInfo 返回应用信息。
// AppVersion 等字段通过 -ldflags 在链接阶段注入，因此必须在运行时读取。
func GetAppInfo() Info {
	// 获取当前 bgo 可执行文件的绝对路径
	bgoExePath := ""
	if exePath, err := os.Executable(); err == nil {
		if absPath, err := filepath.Abs(exePath); err == nil {
			bgoExePath = absPath
		} else {
			bgoExePath = exePath
		}
	}

	return Info{
		AppName:    AppName,
		AppVersion: AppVersion,
		BuildTime:  BuildTime,
		GitHash:    GitHash,
		Pid:        os.Getpid(),
		Platform:   fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH),
		GoVersion:  runtime.Version(),
		BgoExePath: bgoExePath,
	}
}
