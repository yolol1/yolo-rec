package douyin

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bililive-go/bililive-go/src/configs"
	blog "github.com/bililive-go/bililive-go/src/log"
)

var (
	autoCookie string
	lastSynced string
	syncMu     sync.Mutex // Ensures only one sync operation runs at a time
	cookieMu   sync.RWMutex
)

// getDouyinCookie 获取抖音 Cookie。
// 优先使用用户配置的 Cookie，如果没有则自动获取 ttwid 并缓存。
func getDouyinCookie() string {
	cfg := configs.GetCurrentConfig()
	var c string
	if cfg != nil && cfg.Cookies != nil {
		if val, ok := cfg.Cookies["live.douyin.com"]; ok && strings.TrimSpace(val) != "" {
			c = val
		}
	}

	if c == "" {
		cookieMu.RLock()
		c = autoCookie
		cookieMu.RUnlock()
	}

	if c == "" {
		cookieMu.Lock()
		if autoCookie != "" {
			c = autoCookie
			cookieMu.Unlock()
		} else {
			fetched, err := autoFetchDouyinCookie()
			if err != nil {
				blog.GetLogger().WithError(err).Warn("自动获取抖音 ttwid 失败，这可能导致直播流信息提取失败")
				cookieMu.Unlock()
				return ""
			}
			autoCookie = fetched
			c = fetched
			cookieMu.Unlock()
		}
	}

	// 检查是否需要同步到 btools
	cookieMu.RLock()
	needSync := c != lastSynced
	cookieMu.RUnlock()

	if needSync {
		syncMu.Lock()
		// 双重检查
		cookieMu.RLock()
		stillNeedSync := c != lastSynced
		cookieMu.RUnlock()

		if stillNeedSync {
			success := syncCookieToBtools(c)
			if success {
				cookieMu.Lock()
				lastSynced = c
				cookieMu.Unlock()
			}
		}
		syncMu.Unlock()
	}

	return c
}

// syncCookieToBtools 将 Cookie 同步到 btools 配置中，包含重试机制
func syncCookieToBtools(cookieVal string) bool {
	if strings.TrimSpace(cookieVal) == "" {
		return false
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/config/set", getBtoolsPort())

	// 构造 JSON 请求体，把 cookie 设置到 recorder.douyin.cookie
	payloadMap := map[string]string{
		"key":   "recorder.douyin.cookie",
		"value": cookieVal,
	}
	payloadBytes, err := json.Marshal(payloadMap)
	if err != nil {
		blog.GetLogger().WithError(err).Error("序列化 Cookie 同步 payload 失败")
		return false
	}

	for i := 0; i < 15; i++ {
		req, err := http.NewRequest("POST", url, bytes.NewBuffer(payloadBytes))
		if err != nil {
			blog.GetLogger().WithError(err).Error("创建 Cookie 同步请求失败")
			return false
		}

		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", getBtoolsToken())

		resp, err := btoolsHttpClient.Do(req)
		if err != nil {
			blog.GetLogger().WithError(err).Debug("同步 Cookie 到 btools 失败，btools 可能尚未就绪，稍后重试...")
			time.Sleep(1 * time.Second)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			blog.GetLogger().Warnf("同步 Cookie 到 btools 响应状态异常: %d, body: %s，稍后重试...", resp.StatusCode, string(body))
			time.Sleep(1 * time.Second)
			continue
		}

		blog.GetLogger().Info("成功同步最新抖音 Cookie 到 btools")
		return true
	}

	blog.GetLogger().Error("同步 Cookie 到 btools 最终失败")
	return false
}

func autoFetchDouyinCookie() (string, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 3 {
				return fmt.Errorf("too many redirects")
			}
			return nil
		},
	}

	req, err := http.NewRequest("GET", "https://live.douyin.com/", nil)
	if err != nil {
		return "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36 Edg/140.0.0.0")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch live.douyin.com: %w", err)
	}
	defer resp.Body.Close()

	for _, cookie := range resp.Cookies() {
		if cookie.Name == "ttwid" {
			blog.GetLogger().Info("自动获取抖音 ttwid 成功")
			return "ttwid=" + cookie.Value, nil
		}
	}

	return "", fmt.Errorf("ttwid not found in response cookies")
}

