//go:build dev

package tools

import (
	"os"
	"path/filepath"
)

func getConfigData() (data []byte, err error) {
	data, err = os.ReadFile("src/tools/remote-tools-config.json")
	if err == nil {
		return data, nil
	}
	
	exePath, _ := os.Executable()
	if exePath != "" {
		projectRoot := filepath.Dir(filepath.Dir(exePath))
		return os.ReadFile(filepath.Join(projectRoot, "src", "tools", "remote-tools-config.json"))
	}
	
	return nil, err
}
