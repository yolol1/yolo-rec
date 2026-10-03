package notify

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/consts"
	"github.com/bililive-go/bililive-go/src/pkg/livelogger"
)

// newTestLogger 创建一个用于测试的 LiveLogger 实例
func newTestLogger() *livelogger.LiveLogger {
	return livelogger.New(0, logrus.Fields{"test": true})
}

// TestSendTestNotification 测试SendTestNotification函数
func TestSendTestNotification(t *testing.T) {
	// 由于SendTestNotification函数主要打印输出和调用SendNotification，
	// 我们在这里主要是确保函数能够正常运行，不会出现panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("SendTestNotification panicked: %v", r)
		}
	}()

	// 调用测试函数
	SendTestNotification(newTestLogger())

	// 如果没有panic，则测试通过
	// 注意：实际的通知发送测试需要mock相关的服务
}

// TestSendNotificationStart 测试SendNotification函数发送开始直播通知
func TestSendNotificationStart(t *testing.T) {
	// 由于实际发送通知需要配置和网络连接，这里主要测试函数是否能正常处理开始状态
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("SendNotification with LiveStatusStart panicked: %v", r)
		}
	}()

	// 调用SendNotification函数，使用开始状态
	err := SendNotification(newTestLogger(), "测试主播", "测试平台", "https://example.com/live", consts.LiveStatusStart)

	// 检查是否有错误返回（注意：在没有配置的情况下，可能会返回错误）
	// 这里我们主要关注函数是否能正常执行，而不是是否真的发送了通知
	_ = err // 在实际测试中，我们可能需要检查错误

	// 如果没有panic，则测试通过
}

// TestSendNotificationStop 测试SendNotification函数发送结束直播通知
func TestSendNotificationStop(t *testing.T) {
	// 由于实际发送通知需要配置和网络连接，这里主要测试函数是否能正常处理结束状态
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("SendNotification with LiveStatusStop panicked: %v", r)
		}
	}()

	// 调用SendNotification函数，使用结束状态
	err := SendNotification(newTestLogger(), "测试主播", "测试平台", "https://example.com/live", consts.LiveStatusStop)

	// 检查是否有错误返回（注意：在没有配置的情况下，可能会返回错误）
	// 这里我们主要关注函数是否能正常执行，而不是是否真的发送了通知
	_ = err // 在实际测试中，我们可能需要检查错误

	// 如果没有panic，则测试通过
}

// TestSendNotificationUnknown 测试SendNotification函数处理未知状态
func TestSendNotificationUnknown(t *testing.T) {
	// 测试函数处理未知状态的能力
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("SendNotification with unknown status panicked: %v", r)
		}
	}()

	// 调用SendNotification函数，使用未知状态
	err := SendNotification(newTestLogger(), "测试主播", "测试平台", "https://example.com/live", "unknown_status")

	// 检查是否有错误返回
	_ = err // 在实际测试中，我们可能需要检查错误

	// 如果没有panic，则测试通过
}

// TestResolveRoomNotifyMode 测试按直播间配置解析通知所需的实际设置状态
func TestResolveRoomNotifyMode(t *testing.T) {
	cfg, err := configs.NewConfigWithBytes([]byte(`
live_rooms:
  - url: "https://live.bilibili.com/1"
    is_listening: true
  - url: "https://live.bilibili.com/2"
    is_listening: true
    auto_record: false
    scheme: "bilibili://live/2"
`))
	if err != nil {
		t.Fatalf("构造测试配置失败: %v", err)
	}

	// 未显式设置 auto_record 的房间沿用默认值（自动录像）
	if mode := resolveRoomNotifyMode(cfg, "https://live.bilibili.com/1"); !mode.autoRecord {
		t.Errorf("未设置 auto_record 的房间应默认为自动录像")
	}

	// 显式关闭自动录像的房间属于"仅监控"
	mode := resolveRoomNotifyMode(cfg, "https://live.bilibili.com/2")
	if mode.autoRecord {
		t.Errorf("auto_record=false 的房间不应被判定为自动录像")
	}
	if mode.schemeUrl != "bilibili://live/2" {
		t.Errorf("scheme URL 解析错误，got %q", mode.schemeUrl)
	}

	// 配置中不存在的直播间（例如测试通知）保持原有默认行为
	if mode := resolveRoomNotifyMode(cfg, "https://example.com/live"); !mode.autoRecord {
		t.Errorf("未在配置中的直播间应保持默认的自动录像提示")
	}
	if mode := resolveRoomNotifyMode(nil, "https://live.bilibili.com/1"); !mode.autoRecord {
		t.Errorf("配置为空时应保持默认的自动录像提示")
	}
}

// TestLiveStatusText 测试状态文案会跟随自动录像设置变化
func TestLiveStatusText(t *testing.T) {
	cases := []struct {
		name       string
		status     string
		autoRecord bool
		want       string
	}{
		{"自动录像开播", consts.LiveStatusStart, true, "已开始直播,正在录制中"},
		{"仅监控开播", consts.LiveStatusStart, false, "已开始直播,未开启自动录制"},
		{"自动录像结束", consts.LiveStatusStop, true, "已结束直播,录制已停止"},
		{"仅监控结束", consts.LiveStatusStop, false, "已结束直播"},
		{"未知状态", "unknown_status", true, "直播状态未知"},
	}
	for _, tc := range cases {
		if got := liveStatusText(tc.status, tc.autoRecord); got != tc.want {
			t.Errorf("%s: liveStatusText(%q, %v) = %q, want %q", tc.name, tc.status, tc.autoRecord, got, tc.want)
		}
	}
}

// TestSendNotificationNtfyMessageByAutoRecord 测试 ntfy 通知文案会跟随房间的自动录像设置变化
func TestSendNotificationNtfyMessageByAutoRecord(t *testing.T) {
	type receivedMessage struct {
		title string
		body  string
	}
	var received []receivedMessage
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("读取 ntfy 请求体失败: %v", err)
		}
		received = append(received, receivedMessage{title: r.Header.Get("Title"), body: string(body)})
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg, err := configs.NewConfigWithBytes([]byte(fmt.Sprintf(`
notify:
  ntfy:
    enable: true
    URL: %q
live_rooms:
  - url: "https://live.bilibili.com/1"
    is_listening: true
  - url: "https://live.bilibili.com/2"
    is_listening: true
    auto_record: false
`, server.URL)))
	if err != nil {
		t.Fatalf("构造测试配置失败: %v", err)
	}

	previous := configs.GetCurrentConfig()
	configs.SetCurrentConfig(cfg)
	defer configs.SetCurrentConfig(previous)

	logger := newTestLogger()
	if err := SendNotification(logger, "自动录像主播", "bilibili", "https://live.bilibili.com/1", consts.LiveStatusStart); err != nil {
		t.Fatalf("发送开播通知失败: %v", err)
	}
	if err := SendNotification(logger, "仅监控主播", "bilibili", "https://live.bilibili.com/2", consts.LiveStatusStart); err != nil {
		t.Fatalf("发送开播通知失败: %v", err)
	}
	if err := SendNotification(logger, "仅监控主播", "bilibili", "https://live.bilibili.com/2", consts.LiveStatusStop); err != nil {
		t.Fatalf("发送停播通知失败: %v", err)
	}

	if len(received) != 3 {
		t.Fatalf("期望收到 3 条 ntfy 通知，实际 %d 条", len(received))
	}

	// 标题仍为主播名，行为保持不变
	if received[0].title != "自动录像主播" || received[1].title != "仅监控主播" {
		t.Errorf("ntfy 通知标题应为主播名，实际: %q / %q", received[0].title, received[1].title)
	}

	// 自动录像的房间：保持原有"正在录制中"提示
	if !strings.Contains(received[0].body, "正在录制中") {
		t.Errorf("自动录像的直播间开播应提示正在录制中，实际: %q", received[0].body)
	}
	// 仅监控的房间：开播不应提示正在录制
	if strings.Contains(received[1].body, "正在录制中") {
		t.Errorf("仅监控的直播间开播不应提示正在录制中，实际: %q", received[1].body)
	}
	if !strings.Contains(received[1].body, "未开启自动录制") {
		t.Errorf("仅监控的直播间开播应提示未开启自动录制，实际: %q", received[1].body)
	}
	// 仅监控的房间：停播不应提示录制已停止
	if strings.Contains(received[2].body, "录制已停止") {
		t.Errorf("仅监控的直播间停播不应提示录制已停止，实际: %q", received[2].body)
	}
	if !strings.Contains(received[2].body, "直播已结束") {
		t.Errorf("仅监控的直播间停播应提示直播已结束，实际: %q", received[2].body)
	}
}
