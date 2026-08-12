package servers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/gorilla/mux"
	"github.com/tidwall/gjson"

	"github.com/bililive-go/bililive-go/src/consts"
	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/listeners"
	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/livestate"
	applog "github.com/bililive-go/bililive-go/src/log"
	"github.com/bililive-go/bililive-go/src/pkg/livelogger"
	"github.com/bililive-go/bililive-go/src/pkg/events"
	"github.com/bililive-go/bililive-go/src/pkg/memstats"
	"github.com/bililive-go/bililive-go/src/pkg/ratelimit"
	"github.com/bililive-go/bililive-go/src/pkg/utils"
	"github.com/bililive-go/bililive-go/src/recorders"
	"github.com/bililive-go/bililive-go/src/tools"
	"github.com/bililive-go/bililive-go/src/types"
)

// FIXME: remove this
func parseInfo(ctx context.Context, l live.Live, preFetchedRoom ...*livestate.LiveRoom) *live.Info {
	inst := instance.GetInstance(ctx)

	// 尝试从缓存获取信息
	obj, err := inst.Cache.Get(l)

	var info *live.Info
	if err != nil || obj == nil {
		// 缓存中没有信息，可能是 InitializingLive 还未初始化
		// 创建一个基础信息
		info = &live.Info{
			Live:         l,
			HostName:     "初始化中...",
			RoomName:     l.GetRawUrl(),
			Status:       false,
			Initializing: true,
		}
	} else {
		// 浅拷贝，避免修改缓存中的共享对象导致并发渲染或状态冲突问题
		cachedInfo := obj.(*live.Info)
		infoCopy := *cachedInfo
		info = &infoCopy
	}

	info.Listening = inst.ListenerManager.(listeners.Manager).HasListener(ctx, l.GetLiveId())
	// 区分"有 recorder"和"真正在录制"
	// HasRecorder=true 但输出文件没有数据时，说明在重试（获取流 URL、连接失败等）
	// 前端应显示"录制准备中"而非"录制中"，避免用户误以为正在正常录制
	//
	// 注意：info 是从缓存获取的共享对象，必须先重置两个互斥字段，
	// 否则前一次调用的残留值会导致 recording=true + recording_preparing=true 同时返回
	info.Recording = false
	info.RecordingPreparing = false
	recorderMgr := inst.RecorderManager.(recorders.Manager)
	if recorderMgr.HasRecorder(ctx, l.GetLiveId()) {
		if recorder, err := recorderMgr.GetRecorder(ctx, l.GetLiveId()); err == nil && recorder.IsRecording() {
			info.Recording = true
		} else {
			// 有 recorder 但尚未真正开始录制（例如流 URL 404 导致不断重试）
			info.RecordingPreparing = true
		}
	}

	// 尝试从数据库同步历史数据（关播时间、主播名称、直播间名称）
	if store, ok := inst.LiveStateStore.(livestate.Store); ok && store != nil {
		var dbRoom *livestate.LiveRoom
		if len(preFetchedRoom) > 0 {
			dbRoom = preFetchedRoom[0]
		} else {
			dbRoom, _ = store.GetLiveRoom(ctx, string(l.GetLiveId()))
		}
		
		if dbRoom != nil {
			if l.GetLastEndTime().IsZero() && !dbRoom.LastEndTime.IsZero() {
				l.SetLastEndTime(dbRoom.LastEndTime)
			}
			if info.HostName == "" && dbRoom.HostName != "" {
				info.HostName = dbRoom.HostName
			}
			if info.RoomName == "" && dbRoom.RoomName != "" {
				info.RoomName = dbRoom.RoomName
			}
		}
	}

	if cfg := configs.GetCurrentConfig(); cfg != nil {
		if room, err := cfg.GetLiveRoomByUrl(l.GetRawUrl()); err == nil {
			info.AutoRecord = room.IsAutoRecord()
		}
	}

	if info.HostName == "" {
		info.HostName = "获取失败"
	}
	if info.RoomName == "" {
		info.RoomName = l.GetRawUrl()
	}
	return info
}

// extractStreamAttributeCombinations 从可用流中提取所有属性组合
// 返回所有流的 AttributesForStreamSelect，供前端动态生成选择器
func extractStreamAttributeCombinations(streams []*live.AvailableStreamInfo) []map[string]string {
	if len(streams) == 0 {
		return nil
	}

	combinations := make([]map[string]string, 0, len(streams))
	for _, stream := range streams {
		if len(stream.AttributesForStreamSelect) > 0 {
			combinations = append(combinations, stream.AttributesForStreamSelect)
		}
	}

	return combinations
}

func getAllLives(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())

	// 预先获取所有的 DB room 信息，避免 N+1 查询
	var allRooms []*livestate.LiveRoom
	if store, ok := inst.LiveStateStore.(livestate.Store); ok && store != nil {
		allRooms, _ = store.GetAllLiveRooms(r.Context())
	}
	dbRoomsMap := make(map[string]*livestate.LiveRoom)
	for _, dbRoom := range allRooms {
		dbRoomsMap[dbRoom.LiveID] = dbRoom
	}

	lives := liveSlice(make([]*live.Info, 0, 4))
	inst.Lives.Range(func(_ types.LiveID, v live.Live) bool {
		dbRoom := dbRoomsMap[string(v.GetLiveId())]
		lives = append(lives, parseInfo(r.Context(), v, dbRoom))
		return true
	})
	sort.Sort(lives)
	writeJSON(writer, lives)
}

func getLive(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	vars := mux.Vars(r)
	liveObj, ok := inst.Lives.Get(types.LiveID(vars["id"]))
	if !ok {
		writeJsonWithStatusCode(writer, http.StatusNotFound, commonResp{
			ErrNo:  http.StatusNotFound,
			ErrMsg: fmt.Sprintf("live id: %s can not find", vars["id"]),
		})
		return
	}

	// 获取基本信息
	info := parseInfo(r.Context(), liveObj)

	// 获取全局配置
	cfg := configs.GetCurrentConfig()
	if cfg == nil {
		writeJSON(writer, info) // 如果配置为空，返回基本信息
		return
	}

	// 获取房间配置
	room, err := cfg.GetLiveRoomByUrl(liveObj.GetRawUrl())
	if err != nil {
		writeJSON(writer, info) // 如果找不到房间配置，返回基本信息
		return
	}

	// 获取平台key
	platformKey := configs.GetPlatformKeyFromUrl(liveObj.GetRawUrl())

	// 解析最终生效的配置
	resolvedConfig := cfg.ResolveConfigForRoom(room, platformKey)

	// 获取平台相关的连接统计
	// 从 URL 中提取主机名用于匹配连接统计
	rawURL := liveObj.GetRawUrl()
	parsedURL, _ := url.Parse(rawURL)
	var connStats []utils.ConnStats
	if parsedURL != nil {
		// 提取 API 主机名前缀（如 bilibili 对应 api.live.bilibili.com）
		host := parsedURL.Host
		// 移除端口号
		if colonIdx := strings.Index(host, ":"); colonIdx != -1 {
			host = host[:colonIdx]
		}
		// 提取主域名部分用于匹配
		parts := strings.Split(host, ".")
		if len(parts) >= 2 {
			domainPrefix := parts[len(parts)-2] // 如 "bilibili"
			connStats = utils.ConnCounterManager.GetStatsByHostPrefix(domainPrefix)
		}
	}

	// 获取平台等待状态信息
	waitInfo := ratelimit.GetGlobalRateLimiter().GetPlatformWaitInfo(platformKey)

	// 获取调度器状态信息
	var schedulerStatus *live.SchedulerStatus
	if provider, ok := liveObj.(live.SchedulerStatusProvider); ok {
		status := provider.GetSchedulerStatus()
		schedulerStatus = &status
	}

	// 获取录制状态和下载速度
	var recorderStatus map[string]interface{}
	if info.Recording || info.RecordingPreparing {
		// 正在录制或录制准备中，获取 recorder 状态（流信息等）
		if recorderMgr, ok := inst.RecorderManager.(recorders.Manager); ok {
			recorder, err := recorderMgr.GetRecorder(r.Context(), info.Live.GetLiveId())
			if err == nil {
				status, statusErr := recorder.GetStatus()
				if statusErr != nil {
					info.Live.GetLogger().Warnf("failed to get recorder status: %v", statusErr)
				}
				recorderStatus = status
			}
		}
	}

	// 获取录制开始时间
	var recordStartTime string
	if info.Recording || info.RecordingPreparing {
		if recorderMgr, ok := inst.RecorderManager.(recorders.Manager); ok {
			recorder, err := recorderMgr.GetRecorder(r.Context(), info.Live.GetLiveId())
			if err == nil {
				recordStartTime = recorder.StartTime().Format("2006-01-02 15:04:05")
			}
		}
	}

	// 获取开播时间
	var liveStartTime string
	lastStartTime := liveObj.GetLastStartTime()
	if !lastStartTime.IsZero() {
		liveStartTime = lastStartTime.Format("2006-01-02 15:04:05")
	}

	// 构造详细响应
	detailedInfo := map[string]interface{}{
		// 基本信息
		"host_name":           info.HostName,
		"room_name":           info.RoomName,
		"status":              info.Status,
		"listening":           info.Listening,
		"recording":           info.Recording,
		"recording_preparing": info.RecordingPreparing,
		"live_id":             info.Live.GetLiveId(),
		"raw_url":             info.Live.GetRawUrl(),
		"platform":            info.Live.GetPlatformCNName(),

		// 有效配置信息
		"platform_key":          platformKey,
		"effective_interval":    resolvedConfig.Interval,
		"effective_out_path":    resolvedConfig.OutPutPath,
		"effective_ffmpeg_path": resolvedConfig.FfmpegPath,
		"quality":               room.Quality,
		"audio_only":            room.AudioOnly,

		// 平台访问限制
		"platform_rate_limit": cfg.GetPlatformMinAccessInterval(platformKey),

		// 平台等待状态
		"rate_limit_info": map[string]interface{}{
			"waited_seconds":      waitInfo.WaitedSeconds,
			"next_request_in_sec": waitInfo.NextRequestInSec,
			"min_interval_sec":    waitInfo.MinIntervalSec,
		},

		// 配置来源信息
		"config_sources": map[string]string{
			"interval":     getConfigSource(cfg, *room, platformKey, "interval"),
			"out_put_path": getConfigSource(cfg, *room, platformKey, "out_put_path"),
			"ffmpeg_path":  getConfigSource(cfg, *room, platformKey, "ffmpeg_path"),
		},

		// 运行时信息 - 连接统计
		"conn_stats": connStats,

		// 录制器状态（包括下载速度等）
		"recorder_status": recorderStatus,

		// 调度器状态（用于显示"距离下次刷新"）
		"scheduler_status": schedulerStatus,

		// 时间信息
		"live_start_time":  liveStartTime,   // 本次开播时间
		"last_record_time": recordStartTime, // 本次录制开始时间

		// 原始配置信息
		"room_config": room,
	}

	// 添加可用流信息（优先使用内存缓存，否则从数据库读取）
	if len(info.AvailableStreams) > 0 {
		detailedInfo["available_streams"] = info.AvailableStreams
		detailedInfo["available_streams_updated_at"] = info.AvailableStreamsUpdatedAt
		// 提取属性组合供前端流选择器使用
		detailedInfo["available_stream_attributes"] = extractStreamAttributeCombinations(info.AvailableStreams)
	} else {
		// 尝试从数据库读取
		if store, ok := inst.LiveStateStore.(livestate.Store); ok {
			streams, err := store.GetAvailableStreams(r.Context(), string(info.Live.GetLiveId()))
			if err == nil && len(streams) > 0 {
				// 转换为 API 格式
				apiStreams := make([]map[string]interface{}, 0, len(streams))
				for _, s := range streams {
					streamData := map[string]interface{}{
						"quality":                      s.Quality,
						"attributes_for_stream_select": s.Attributes,
					}
					apiStreams = append(apiStreams, streamData)
				}
				detailedInfo["available_streams"] = apiStreams
				if len(streams) > 0 && !streams[0].UpdatedAt.IsZero() {
					detailedInfo["available_streams_updated_at"] = streams[0].UpdatedAt.Unix()
				}

				// 提取属性组合供前端流选择器使用
				attributeCombinations := make([]map[string]string, 0, len(streams))
				for _, s := range streams {
					if len(s.Attributes) > 0 {
						attributeCombinations = append(attributeCombinations, s.Attributes)
					}
				}
				detailedInfo["available_stream_attributes"] = attributeCombinations
			}
		}
	}

	writeJSON(writer, detailedInfo)
}

// getConfigSource 获取配置项的来源级别
func getConfigSource(config *configs.Config, room configs.LiveRoom, platformKey, configKey string) string {
	// 检查房间级配置
	switch configKey {
	case "interval":
		if room.Interval != nil {
			return "room"
		}
	case "out_put_path":
		if room.OutPutPath != nil {
			return "room"
		}
	case "ffmpeg_path":
		if room.FfmpegPath != nil {
			return "room"
		}
	}

	// 检查平台级配置
	if platformConfig, exists := config.PlatformConfigs[platformKey]; exists {
		switch configKey {
		case "interval":
			if platformConfig.Interval != nil {
				return "platform"
			}
		case "out_put_path":
			if platformConfig.OutPutPath != nil {
				return "platform"
			}
		case "ffmpeg_path":
			if platformConfig.FfmpegPath != nil {
				return "platform"
			}
		}
	}

	// 默认为全局配置
	return "global"
}

func getLiveLogs(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	vars := mux.Vars(r)
	linesStr := r.URL.Query().Get("lines")
	lines := 100 // 默认100行
	if linesStr != "" {
		if parsedLines, err := strconv.Atoi(linesStr); err == nil && parsedLines > 0 {
			lines = parsedLines
		}
	}

	liveID := types.LiveID(vars["id"])

	// 查找直播间
	liveInstance, ok := inst.Lives.Get(liveID)
	if !ok {
		writeJsonWithStatusCode(writer, http.StatusNotFound, commonResp{
			ErrNo:  http.StatusNotFound,
			ErrMsg: fmt.Sprintf("live id: %s can not find", vars["id"]),
		})
		return
	}

	// 从直播间的 Logger 获取日志（原始文本形式）
	logsText := liveInstance.GetLogger().GetLogs()

	// 按行分割日志
	var logLines []string
	if logsText != "" {
		// 分割成行，去掉末尾空行
		for _, line := range strings.Split(logsText, "\n") {
			if line != "" {
				logLines = append(logLines, line)
			}
		}
	}

	// 如果请求了行数限制，只返回最后 N 行
	if lines > 0 && len(logLines) > lines {
		logLines = logLines[len(logLines)-lines:]
	}

	logResponse := map[string]interface{}{
		"lines":     logLines,
		"total":     len(logLines),
		"max_lines": lines,
	}

	writeJSON(writer, logResponse)
}

func parseLiveAction(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	vars := mux.Vars(r)
	resp := commonResp{}
	live, ok := inst.Lives.Get(types.LiveID(vars["id"]))
	if !ok {
		resp.ErrNo = http.StatusNotFound
		resp.ErrMsg = fmt.Sprintf("live id: %s can not find", vars["id"])
		writeJsonWithStatusCode(writer, http.StatusNotFound, resp)
		return
	}
	cfg := configs.GetCurrentConfig()
	_, err := cfg.GetLiveRoomByUrl(live.GetRawUrl())
	if err != nil {
		resp.ErrNo = http.StatusNotFound
		resp.ErrMsg = fmt.Sprintf("room : %s can not find", live.GetRawUrl())
		writeJsonWithStatusCode(writer, http.StatusNotFound, resp)
		return
	}
	switch vars["action"] {
	case "start":
		if err := startListening(inst.Ctx, live); err != nil {
			resp.ErrNo = http.StatusBadRequest
			resp.ErrMsg = err.Error()
			writeJsonWithStatusCode(writer, http.StatusBadRequest, resp)
			return
		}
		if _, err := configs.SetLiveRoomListening(live.GetRawUrl(), true); err != nil {
			live.GetLogger().Error("failed to set live room listening: " + err.Error())
		}
		// 广播监控开启事件
		GetSSEHub().BroadcastListChange(live.GetLiveId(), "listen_start", map[string]interface{}{
			"live_id": string(live.GetLiveId()),
		})
	case "stop":
		if err := stopListening(inst.Ctx, live.GetLiveId()); err != nil {
			resp.ErrNo = http.StatusBadRequest
			resp.ErrMsg = err.Error()
			writeJsonWithStatusCode(writer, http.StatusBadRequest, resp)
			return
		}
		if _, err := configs.SetLiveRoomListening(live.GetRawUrl(), false); err != nil {
			live.GetLogger().Error("failed to set live room listening: " + err.Error())
		}
		// 记录用户停止监控（结束当前会话）
		if manager, ok := inst.LiveStateManager.(*livestate.Manager); ok && manager != nil {
			manager.OnUserStopMonitoring(string(live.GetLiveId()))
		}
		// 广播监控停止事件
		GetSSEHub().BroadcastListChange(live.GetLiveId(), "listen_stop", map[string]interface{}{
			"live_id": string(live.GetLiveId()),
		})
	case "start-recording":
		// 检查监听状态
		isListening := inst.ListenerManager.(listeners.Manager).HasListener(inst.Ctx, live.GetLiveId())
		if !isListening {
			resp.ErrNo = http.StatusBadRequest
			resp.ErrMsg = "直播间未开启监控，请先开启监控"
			writeJsonWithStatusCode(writer, http.StatusBadRequest, resp)
			return
		}
		// 检查开播状态
		info := parseInfo(r.Context(), live)
		if !info.Status {
			resp.ErrNo = http.StatusBadRequest
			resp.ErrMsg = "主播未开播，无法开始录制"
			writeJsonWithStatusCode(writer, http.StatusBadRequest, resp)
			return
		}
		// 检查录制状态
		recorderMgr := inst.RecorderManager.(recorders.Manager)
		if recorderMgr.HasRecorder(inst.Ctx, live.GetLiveId()) {
			resp.ErrNo = http.StatusBadRequest
			resp.ErrMsg = "直播间已在录制或准备录制中"
			writeJsonWithStatusCode(writer, http.StatusBadRequest, resp)
			return
		}
		// 开始录制
		if err := recorderMgr.AddRecorder(inst.Ctx, live); err != nil {
			resp.ErrNo = http.StatusInternalServerError
			resp.ErrMsg = fmt.Sprintf("开启录制失败: %s", err.Error())
			writeJsonWithStatusCode(writer, http.StatusInternalServerError, resp)
			return
		}
		// 广播事件
		GetSSEHub().BroadcastListChange(live.GetLiveId(), "record_start", map[string]interface{}{
			"live_id": string(live.GetLiveId()),
		})
	case "stop-recording":
		recorderMgr := inst.RecorderManager.(recorders.Manager)
		if !recorderMgr.HasRecorder(inst.Ctx, live.GetLiveId()) {
			resp.ErrNo = http.StatusBadRequest
			resp.ErrMsg = "直播间当前未在录制"
			writeJsonWithStatusCode(writer, http.StatusBadRequest, resp)
			return
		}
		// 停止录制
		if err := recorderMgr.RemoveRecorder(inst.Ctx, live.GetLiveId()); err != nil {
			resp.ErrNo = http.StatusInternalServerError
			resp.ErrMsg = fmt.Sprintf("停止录制失败: %s", err.Error())
			writeJsonWithStatusCode(writer, http.StatusInternalServerError, resp)
			return
		}
		// 广播事件
		GetSSEHub().BroadcastListChange(live.GetLiveId(), "record_stop", map[string]interface{}{
			"live_id": string(live.GetLiveId()),
		})
	case "enable-auto-record":
		if _, err := configs.SetLiveRoomAutoRecord(live.GetRawUrl(), true); err != nil {
			resp.ErrNo = http.StatusInternalServerError
			resp.ErrMsg = fmt.Sprintf("设置自动录制失败: %s", err.Error())
			writeJsonWithStatusCode(writer, http.StatusInternalServerError, resp)
			return
		}
		GetSSEHub().BroadcastListChange(live.GetLiveId(), "auto_record_enable", map[string]interface{}{
			"live_id": string(live.GetLiveId()),
		})
	case "disable-auto-record":
		if _, err := configs.SetLiveRoomAutoRecord(live.GetRawUrl(), false); err != nil {
			resp.ErrNo = http.StatusInternalServerError
			resp.ErrMsg = fmt.Sprintf("设置自动录制失败: %s", err.Error())
			writeJsonWithStatusCode(writer, http.StatusInternalServerError, resp)
			return
		}
		GetSSEHub().BroadcastListChange(live.GetLiveId(), "auto_record_disable", map[string]interface{}{
			"live_id": string(live.GetLiveId()),
		})
	case "forceRefresh":
		// 强制刷新：忽略平台访问频率限制，立即获取最新信息
		platformKey := configs.GetPlatformKeyFromUrl(live.GetRawUrl())
		ratelimit.GetGlobalRateLimiter().ForceAccess(platformKey)

		// 手动调用 GetInfo 获取最新信息
		info, err := live.GetInfo()
		if err != nil {
			resp.ErrNo = http.StatusInternalServerError
			resp.ErrMsg = fmt.Sprintf("force refresh failed: %s", err.Error())
			writeJsonWithStatusCode(writer, http.StatusInternalServerError, resp)
			return
		}

		// 广播频率限制更新事件，通知前端更新倒计时
		waitInfo := ratelimit.GetGlobalRateLimiter().GetPlatformWaitInfo(platformKey)
		GetSSEHub().BroadcastRateLimitUpdate(live.GetLiveId(), map[string]interface{}{
			"waited_seconds":      waitInfo.WaitedSeconds,
			"next_request_in_sec": waitInfo.NextRequestInSec,
			"min_interval_sec":    waitInfo.MinIntervalSec,
		})

		// 返回刷新后的信息
		writeJSON(writer, map[string]interface{}{
			"success":   true,
			"message":   "强制刷新成功",
			"host_name": info.HostName,
			"room_name": info.RoomName,
			"status":    info.Status,
		})
		return
	case "segment":
		// 请求在下一个关键帧处分段（仅在使用 FLV 代理时有效）
		recorderMgr, ok := inst.RecorderManager.(recorders.Manager)
		if !ok {
			resp.ErrNo = http.StatusInternalServerError
			resp.ErrMsg = "录制管理器不可用"
			writeJsonWithStatusCode(writer, http.StatusInternalServerError, resp)
			return
		}

		recorder, err := recorderMgr.GetRecorder(r.Context(), live.GetLiveId())
		if err != nil {
			resp.ErrNo = http.StatusBadRequest
			resp.ErrMsg = "直播间未在录制中"
			writeJsonWithStatusCode(writer, http.StatusBadRequest, resp)
			return
		}

		// 检查是否支持 FLV 代理分段
		if !recorder.HasFlvProxy() {
			resp.ErrNo = http.StatusBadRequest
			resp.ErrMsg = "当前录制未使用 FLV 代理，不支持手动分段（请在配置中启用 enable_flv_proxy_segment）"
			writeJsonWithStatusCode(writer, http.StatusBadRequest, resp)
			return
		}

		// 请求分段
		if recorder.RequestSegment() {
			writeJSON(writer, map[string]interface{}{
				"success": true,
				"message": "分段请求已接受，将在下一个关键帧处分段",
			})
		} else {
			resp.ErrNo = http.StatusTooManyRequests
			resp.ErrMsg = "分段请求被拒绝，可能距离上次分段时间过短（最小间隔 10 秒）"
			writeJsonWithStatusCode(writer, http.StatusTooManyRequests, resp)
		}
		return
	default:
		resp.ErrNo = http.StatusBadRequest
		resp.ErrMsg = fmt.Sprintf("invalid Action: %s", vars["action"])
		writeJsonWithStatusCode(writer, http.StatusBadRequest, resp)
		return
	}
	writeJSON(writer, parseInfo(r.Context(), live))
}

// switchStreamHandler 处理切换流设置的请求
func switchStreamHandler(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	vars := mux.Vars(r)
	resp := commonResp{}

	live, ok := inst.Lives.Get(types.LiveID(vars["id"]))
	if !ok {
		resp.ErrNo = http.StatusNotFound
		resp.ErrMsg = fmt.Sprintf("直播间 ID: %s 未找到", vars["id"])
		writeJsonWithStatusCode(writer, http.StatusNotFound, resp)
		return
	}

	// 读取请求体获取目标流设置
	b, err := io.ReadAll(r.Body)
	if err != nil {
		resp.ErrNo = http.StatusBadRequest
		resp.ErrMsg = err.Error()
		writeJsonWithStatusCode(writer, http.StatusBadRequest, resp)
		return
	}

	var streamRequest configs.ResolvedStreamPreference
	if err := json.Unmarshal(b, &streamRequest); err != nil {
		resp.ErrNo = http.StatusBadRequest
		resp.ErrMsg = "无效的请求格式: " + err.Error()
		writeJsonWithStatusCode(writer, http.StatusBadRequest, resp)
		return
	}

	// 验证参数
	// if streamRequest.Quality == "" {
	// 	resp.ErrNo = http.StatusBadRequest
	// 	resp.ErrMsg = "必须指定 quality"
	// 	writeJsonWithStatusCode(writer, http.StatusBadRequest, resp)
	// 	return
	// }

	// 更新流配置
	_, err = configs.UpdateWithRetry(func(c *configs.Config) error {
		liveRoom, err1 := c.GetLiveRoomByUrl(live.GetRawUrl())
		if err1 != nil {
			return err1
		}

		if liveRoom.StreamPreference == nil {
			liveRoom.StreamPreference = &configs.StreamPreference{}
		}

		liveRoom.StreamPreference.Quality = &streamRequest.Quality
		liveRoom.StreamPreference.Attributes = &streamRequest.Attributes

		return nil
	}, 3, 10*time.Millisecond)

	if err != nil {
		resp.ErrNo = http.StatusInternalServerError
		resp.ErrMsg = "更新流配置失败: " + err.Error()
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, resp)
		return
	}

	// 检查录制器是否存在，存在则重启
	recorderMgr, ok := inst.RecorderManager.(recorders.Manager)
	if !ok {
		resp.ErrNo = http.StatusInternalServerError
		resp.ErrMsg = "录制管理器不可用"
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, resp)
		return
	}

	if recorderMgr.HasRecorder(inst.Ctx, live.GetLiveId()) {
		// 重启录制器以应用新的流设置
		if err := recorderMgr.RestartRecorder(inst.Ctx, live); err != nil {
			resp.ErrNo = http.StatusInternalServerError
			resp.ErrMsg = "重启录制器失败: " + err.Error()
			writeJsonWithStatusCode(writer, http.StatusInternalServerError, resp)
			return
		}
		live.GetLogger().Infof("流设置已更新并重启录制: quality=%s", streamRequest.Quality)

		writeJSON(writer, map[string]interface{}{
			"success": true,
			"message": "流设置已更新，录制已重新启动",
			"stream_config": map[string]interface{}{
				"quality":    streamRequest.Quality,
				"attributes": streamRequest.Attributes,
			},
		})
	} else {
		// 不在录制中，仅保存配置
		live.GetLogger().Infof("流设置已更新（未在录制中）: quality=%s", streamRequest.Quality)

		writeJSON(writer, map[string]interface{}{
			"success":      true,
			"message":      "流设置已更新（直播间未在录制中，将在下次录制时生效）",
			"is_recording": false,
			"stream_config": map[string]interface{}{
				"quality":    streamRequest.Quality,
				"attributes": streamRequest.Attributes,
			},
		})
	}
}

func startListening(ctx context.Context, live live.Live) error {
	inst := instance.GetInstance(ctx)
	return inst.ListenerManager.(listeners.Manager).AddListener(ctx, live)
}

func stopListening(ctx context.Context, liveId types.LiveID) error {
	inst := instance.GetInstance(ctx)
	return inst.ListenerManager.(listeners.Manager).RemoveListener(ctx, liveId)
}

/*
	Post data example

[

	{
		"url": "http://live.bilibili.com/1030",
		"listen": true
	},
	{
		"url": "https://live.bilibili.com/493",
		"listen": true
	}

]
*/
func addLives(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	b, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(writer, map[string]any{
			"error": err.Error(),
		})
		return
	}
	info := liveSlice(make([]*live.Info, 0))
	errorMessages := make([]string, 0, 4)
	gjson.ParseBytes(b).ForEach(func(key, value gjson.Result) bool {
		isListen := value.Get("listen").Bool()
		urlStr := strings.Trim(value.Get("url").String(), " ")
		if retInfo, err := addLiveImpl(inst.Ctx, urlStr, isListen); err != nil {
			msg := urlStr + ": " + err.Error()
			applog.GetLogger().Error(msg)
			errorMessages = append(errorMessages, msg)
			return true
		} else {
			info = append(info, retInfo)
		}
		return true
	})
	sort.Sort(info)
	// TODO return error messages too
	writeJSON(writer, info)
}

func addLiveImpl(ctx context.Context, urlStr string, isListen bool) (info *live.Info, err error) {
	if !strings.HasPrefix(urlStr, "http://") && !strings.HasPrefix(urlStr, "https://") {
		urlStr = "https://" + urlStr
	}
	u, err := url.Parse(urlStr)
	if err != nil {
		return nil, errors.New("can't parse url: " + urlStr)
	}
	inst := instance.GetInstance(ctx)
	needAppend := false
	liveRoom, err := configs.GetCurrentConfig().GetLiveRoomByUrl(u.String())
	if err != nil {
		liveRoom = &configs.LiveRoom{
			Url:         u.String(),
			IsListening: isListen,
		}
		needAppend = true
	}
	onInitFinished := func(initializingLive live.Live, originalLive live.Live, info *live.Info) {
		if ed, ok := inst.EventDispatcher.(events.Dispatcher); ok {
			ed.DispatchEvent(events.NewEvent(listeners.RoomInitializingFinished, live.InitializingFinishedParam{
				InitializingLive: initializingLive,
				Live:             originalLive,
				Info:             info,
			}))
		}
	}

	newLive, err := live.NewInitializing(ctx, liveRoom, inst.Cache, onInitFinished)
	if err != nil {
		return nil, err
	}
	// 记录 LiveId 到全局配置（并发安全）
	configs.SetLiveRoomId(u.String(), newLive.GetLiveId())
	if inst.Lives.SetIfAbsent(newLive.GetLiveId(), newLive) {
		if isListen {
			inst.ListenerManager.(listeners.Manager).AddListener(ctx, newLive)
		}
		info = parseInfo(ctx, newLive)

		if needAppend {
			if liveRoom == nil {
				return nil, errors.New("liveRoom is nil, cannot append to LiveRooms")
			}
			// 使用统一的 Update 接口做 COW 并原子替换
			if _, err := configs.AppendLiveRoom(*liveRoom); err != nil {
				return nil, err
			}
		}
		// 广播直播间列表变更事件
		GetSSEHub().BroadcastListChange(newLive.GetLiveId(), "room_added", map[string]interface{}{
			"live_id":   string(newLive.GetLiveId()),
			"url":       urlStr,
			"listening": isListen,
		})
	} else {
		if existingLive, ok := inst.Lives.Get(newLive.GetLiveId()); ok {
			info = parseInfo(ctx, existingLive)
		} else {
			info = parseInfo(ctx, newLive)
		}
	}
	return info, nil
}

func removeLive(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	vars := mux.Vars(r)
	live, ok := inst.Lives.Get(types.LiveID(vars["id"]))
	if !ok {
		writeJsonWithStatusCode(writer, http.StatusNotFound, commonResp{
			ErrNo:  http.StatusNotFound,
			ErrMsg: fmt.Sprintf("live id: %s can not find", vars["id"]),
		})
		return
	}
	if err := removeLiveImpl(inst.Ctx, live); err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: err.Error(),
		})
		return
	}
	writeJSON(writer, commonResp{
		Data: "OK",
	})
}

func removeLiveImpl(ctx context.Context, live live.Live) error {
	inst := instance.GetInstance(ctx)
	liveId := live.GetLiveId()
	lm := inst.ListenerManager.(listeners.Manager)
	if lm.HasListener(ctx, liveId) {
		if err := lm.RemoveListener(ctx, liveId); err != nil {
			return err
		}
	}
	inst.Lives.Delete(liveId)
	if _, err := configs.RemoveLiveRoomByUrl(live.GetRawUrl()); err != nil {
		return err
	}
	// 广播直播间列表变更事件
	GetSSEHub().BroadcastListChange(liveId, "room_removed", map[string]interface{}{
		"live_id": string(liveId),
	})
	return nil
}

func getConfig(writer http.ResponseWriter, r *http.Request) {
	writeJSON(writer, configs.GetCurrentConfig())
}

func putConfig(writer http.ResponseWriter, r *http.Request) {
	config := configs.GetCurrentConfig()
	config.RefreshLiveRoomIndexCache()
	if err := config.Marshal(); err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: err.Error(),
		})
		return
	}
	writeJsonWithStatusCode(writer, http.StatusOK, commonResp{
		Data: "OK",
	})
}

func applyLiveRoomsByConfig(ctx context.Context, oldConfig *configs.Config, newConfig *configs.Config) error {
	inst := instance.GetInstance(ctx)
	newLiveRooms := newConfig.LiveRooms
	newUrlMap := make(map[string]*configs.LiveRoom)
	for index := range newLiveRooms {
		newRoom := &newLiveRooms[index]
		newUrlMap[newRoom.Url] = newRoom
		if room, err := oldConfig.GetLiveRoomByUrl(newRoom.Url); err != nil {
			// add live
			if _, err := addLiveImpl(ctx, newRoom.Url, newRoom.IsListening); err != nil {
				return err
			}
		} else {
			live, ok := inst.Lives.Get(types.LiveID(room.LiveId))
			if !ok {
				return fmt.Errorf("live id: %s can not find", room.LiveId)
			}
			live.UpdateLiveOptionsbyConfig(ctx, newRoom)
			if room.IsListening != newRoom.IsListening {
				if newRoom.IsListening {
					// start listening
					if err := startListening(ctx, live); err != nil {
						return err
					}
				} else {
					// stop listening
					if err := stopListening(ctx, live.GetLiveId()); err != nil {
						return err
					}
				}
			}
		}
	}
	loopRooms := oldConfig.LiveRooms
	for _, room := range loopRooms {
		if _, ok := newUrlMap[room.Url]; !ok {
			// remove live
			live, ok := inst.Lives.Get(types.LiveID(room.LiveId))
			if !ok {
				return fmt.Errorf("live id: %s can not find", room.LiveId)
			}
			removeLiveImpl(ctx, live)
		}
	}
	return nil
}

func getInfo(writer http.ResponseWriter, r *http.Request) {
	writeJSON(writer, consts.GetAppInfo())
}

// EffectiveConfigResponse 用于返回配置及其实际生效值
type EffectiveConfigResponse struct {
	*configs.Config

	// 额外的实际生效值字段
	ActualOutPutPath         string `json:"actual_out_put_path"`
	ActualFfmpegPath         string `json:"actual_ffmpeg_path"`
	ActualLogFolder          string `json:"actual_log_folder"`
	ActualAppDataPath        string `json:"actual_app_data_path"`
	ActualReadOnlyToolFolder string `json:"actual_read_only_tool_folder"`
	ActualToolRootFolder     string `json:"actual_tool_root_folder"`
	DefaultOutPutTmpl        string `json:"default_out_put_tmpl"`
	TimeoutInSeconds         int    `json:"timeout_in_seconds"`
	LiveRoomsCount           int    `json:"live_rooms_count"`

	// 下载器可用性信息
	DownloaderAvailability tools.DownloaderAvailability `json:"downloader_availability"`
	// 可用的下载器类型列表
	AvailableDownloaders []string `json:"available_downloaders"`
}

// getEffectiveConfig 获取实际生效的配置值（用于GUI模式显示）
func getEffectiveConfig(writer http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	cfg := configs.GetCurrentConfig()
	if cfg == nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "配置未初始化",
		})
		return
	}

	// 获取实际的 ffmpeg 路径
	actualFfmpegPath, err := utils.GetFFmpegPath(ctx)
	if err == nil {
		actualFfmpegPath, _ = filepath.Abs(actualFfmpegPath)
	} else {
		actualFfmpegPath = "未找到"
	}

	// 获取输出路径的绝对路径
	actualOutPutPath, _ := filepath.Abs(cfg.OutPutPath)

	// 获取日志输出目录的绝对路径
	actualLogFolder := cfg.Log.OutPutFolder
	if actualLogFolder != "" {
		actualLogFolder, _ = filepath.Abs(actualLogFolder)
	}

	// 获取应用数据目录的绝对路径
	actualAppDataPath := cfg.AppDataPath
	if actualAppDataPath == "" {
		actualAppDataPath = filepath.Join(cfg.OutPutPath, ".appdata")
	}
	actualAppDataPath, _ = filepath.Abs(actualAppDataPath)

	// 获取只读工具目录的绝对路径
	actualReadOnlyToolFolder := cfg.ReadOnlyToolFolder
	if actualReadOnlyToolFolder != "" {
		actualReadOnlyToolFolder, _ = filepath.Abs(actualReadOnlyToolFolder)
	}

	// 获取可写工具目录的绝对路径
	actualToolRootFolder := cfg.ToolRootFolder
	if actualToolRootFolder != "" {
		actualToolRootFolder, _ = filepath.Abs(actualToolRootFolder)
	}

	// 默认输出模板
	defaultOutputTmpl := `{{ .Live.GetPlatformCNName }}/{{ with .Live.GetOptions.NickName }}{{ . | filenameFilter }}{{ else }}{{ .HostName | filenameFilter }}{{ end }}/[{{ now | date "2006-01-02 15-04-05"}}][{{ .HostName | filenameFilter }}][{{ .RoomName | filenameFilter }}].flv`

	// 获取下载器可用性信息
	downloaderAvail := tools.GetDownloaderAvailability()

	// 构建可用下载器列表
	availableDownloaders := []string{string(configs.DownloaderNative)} // native 始终可用
	if downloaderAvail.FFmpegAvailable {
		// 把 ffmpeg 放在第一位作为推荐选项
		availableDownloaders = append([]string{string(configs.DownloaderFFmpeg)}, availableDownloaders...)
	}
	if downloaderAvail.BililiveRecorderAvailable {
		availableDownloaders = append(availableDownloaders, string(configs.DownloaderBililiveRecorder))
	}

	// 构建响应
	response := &EffectiveConfigResponse{
		Config:                   cfg,
		ActualOutPutPath:         actualOutPutPath,
		ActualFfmpegPath:         actualFfmpegPath,
		ActualLogFolder:          actualLogFolder,
		ActualAppDataPath:        actualAppDataPath,
		ActualReadOnlyToolFolder: actualReadOnlyToolFolder,
		ActualToolRootFolder:     actualToolRootFolder,
		DefaultOutPutTmpl:        defaultOutputTmpl,
		TimeoutInSeconds:         cfg.TimeoutInUs / 1000000,
		LiveRoomsCount:           len(cfg.LiveRooms),
		DownloaderAvailability:   downloaderAvail,
		AvailableDownloaders:     availableDownloaders,
	}

	writeJSON(writer, response)
}

// getPlatformStats 获取平台相关的直播间统计
func getPlatformStats(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	cfg := configs.GetCurrentConfig()
	if cfg == nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "配置未初始化",
		})
		return
	}

	// 统计每个平台的直播间（只统计正在监控的）
	platformRooms := make(map[string][]map[string]interface{})
	platformListeningCount := make(map[string]int) // 每个平台正在监控的直播间数量

	for _, room := range cfg.LiveRooms {
		platformKey := configs.GetPlatformKeyFromUrl(room.Url)
		if platformKey == "" {
			platformKey = "unknown"
		}

		// 计算 LiveId（从 URL 生成，与 live/internal/genLiveId 逻辑一致）
		var liveId string
		if room.LiveId != "" {
			liveId = string(room.LiveId)
		} else if parsedUrl, err := url.Parse(room.Url); err == nil {
			liveId = string(types.LiveID(utils.GetMd5String([]byte(parsedUrl.Host + parsedUrl.Path))))
		}

		roomInfo := map[string]interface{}{
			"url":          room.Url,
			"is_listening": room.IsListening,
			"quality":      room.Quality,
			"audio_only":   room.AudioOnly,
			"nick_name":    room.NickName,
			"live_id":      liveId,
			"room_config": map[string]interface{}{
				"danmaku_enable": room.DanmakuEnable,
				"danmaku":        room.Danmaku,
			},
		}

		// 从缓存获取直播间信息（不触发网络请求）
		if liveInstance, ok := inst.Lives.Get(room.LiveId); ok {
			if obj, err := inst.Cache.Get(liveInstance); err == nil {
				if info, ok := obj.(*live.Info); ok && info != nil {
					roomInfo["host_name"] = info.HostName
					roomInfo["room_name"] = info.RoomName
					roomInfo["status"] = info.Status
					roomInfo["recording"] = info.Recording
					roomInfo["recording_preparing"] = info.RecordingPreparing
					roomInfo["initializing"] = info.Initializing
				}
			}
		}

		platformRooms[platformKey] = append(platformRooms[platformKey], roomInfo)
		if room.IsListening {
			platformListeningCount[platformKey]++
		}
	}

	// 所有已知平台列表
	allKnownPlatforms := []string{
		"bilibili", "douyin", "douyu", "huya", "kuaishou", "yy", "acfun",
		"lang", "missevan", "openrec", "weibolive", "xiaohongshu", "yizhibo",
		"hongdoufm", "zhanqi", "cc", "twitch", "qq", "huajiao", "sooplive",
	}

	// 构建平台统计响应
	stats := make([]map[string]interface{}, 0)

	// 首先添加有直播间的平台（按是否有配置和直播间数量排序）
	processedPlatforms := make(map[string]bool)

	// 1. 先添加配置中定义且有直播间的平台
	for platformKey, platformConfig := range cfg.PlatformConfigs {
		rooms := platformRooms[platformKey]
		if rooms == nil {
			rooms = []map[string]interface{}{}
		}
		listeningCount := platformListeningCount[platformKey]

		// 计算实际访问间隔
		interval := cfg.Interval
		if platformConfig.Interval != nil {
			interval = *platformConfig.Interval
		}
		actualAccessInterval := 0.0
		if listeningCount > 0 {
			actualAccessInterval = float64(interval) / float64(listeningCount)
		}

		// 检查是否低于最小访问间隔
		warningMessage := ""
		if listeningCount > 0 && platformConfig.MinAccessIntervalSec > 0 && actualAccessInterval < float64(platformConfig.MinAccessIntervalSec) {
			effectiveInterval := float64(platformConfig.MinAccessIntervalSec) * float64(listeningCount)
			warningMessage = fmt.Sprintf("当前设置下实际每个直播间的检测间隔约为 %.1f 秒（受最小访问间隔限制）", effectiveInterval)
		}

		stats = append(stats, map[string]interface{}{
			"platform_key":            platformKey,
			"platform_name":           platformConfig.Name,
			"room_count":              len(rooms),
			"listening_count":         listeningCount,
			"rooms":                   rooms,
			"has_config":              true,
			"has_rooms":               len(rooms) > 0,
			"min_access_interval_sec": platformConfig.MinAccessIntervalSec,
			"interval":                platformConfig.Interval,
			"effective_interval":      interval,
			"actual_access_interval":  actualAccessInterval,
			"warning_message":         warningMessage,
			"out_put_path":            platformConfig.OutPutPath,
			"ffmpeg_path":             platformConfig.FfmpegPath,
		})
		processedPlatforms[platformKey] = true
	}

	// 2. 添加有直播间但没有配置的平台
	for platformKey, rooms := range platformRooms {
		if processedPlatforms[platformKey] {
			continue
		}
		listeningCount := platformListeningCount[platformKey]

		// 使用全局间隔计算实际访问间隔
		actualAccessInterval := 0.0
		if listeningCount > 0 {
			actualAccessInterval = float64(cfg.Interval) / float64(listeningCount)
		}

		stats = append(stats, map[string]interface{}{
			"platform_key":           platformKey,
			"room_count":             len(rooms),
			"listening_count":        listeningCount,
			"rooms":                  rooms,
			"has_config":             false,
			"has_rooms":              true,
			"effective_interval":     cfg.Interval,
			"actual_access_interval": actualAccessInterval,
		})
		processedPlatforms[platformKey] = true
	}

	// 3. 添加没有直播间但有配置的平台
	for platformKey, platformConfig := range cfg.PlatformConfigs {
		if processedPlatforms[platformKey] {
			continue
		}
		stats = append(stats, map[string]interface{}{
			"platform_key":            platformKey,
			"platform_name":           platformConfig.Name,
			"room_count":              0,
			"listening_count":         0,
			"rooms":                   []map[string]interface{}{},
			"has_config":              true,
			"has_rooms":               false,
			"min_access_interval_sec": platformConfig.MinAccessIntervalSec,
			"interval":                platformConfig.Interval,
			"out_put_path":            platformConfig.OutPutPath,
			"ffmpeg_path":             platformConfig.FfmpegPath,
		})
		processedPlatforms[platformKey] = true
	}

	// 返回所有已知平台（用于添加新平台配置）
	availablePlatforms := make([]string, 0)
	for _, p := range allKnownPlatforms {
		if !processedPlatforms[p] {
			availablePlatforms = append(availablePlatforms, p)
		}
	}

	response := map[string]interface{}{
		"platforms":           stats,
		"available_platforms": availablePlatforms,
		"global_interval":     cfg.Interval,
	}

	writeJSON(writer, response)
}

// previewOutputTmpl 预览输出模板生成的路径
func previewOutputTmpl(writer http.ResponseWriter, r *http.Request) {
	cfg := configs.GetCurrentConfig()
	if cfg == nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "配置未初始化",
		})
		return
	}

	b, err := io.ReadAll(r.Body)
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: err.Error(),
		})
		return
	}

	var req struct {
		Template   string `json:"template"`
		OutPutPath string `json:"out_put_path"`
	}
	if err := json.Unmarshal(b, &req); err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: "无效的JSON格式: " + err.Error(),
		})
		return
	}

	// 使用默认模板如果未提供
	templateStr := req.Template
	if templateStr == "" {
		templateStr = `{{ .Live.GetPlatformCNName }}/{{ with .Live.GetOptions.NickName }}{{ . | filenameFilter }}{{ else }}{{ .HostName | filenameFilter }}{{ end }}/[{{ now | date "2006-01-02 15-04-05"}}][{{ .HostName | filenameFilter }}][{{ .RoomName | filenameFilter }}].flv`
	}

	outPutPath := req.OutPutPath
	if outPutPath == "" {
		outPutPath = cfg.OutPutPath
	}

	// 解析模板
	tmpl, err := template.New("preview").Funcs(utils.GetFuncMap(cfg)).Parse(templateStr)
	if err != nil {
		writeJSON(writer, map[string]interface{}{
			"success":    false,
			"error":      "模板语法错误: " + err.Error(),
			"error_type": "parse_error",
		})
		return
	}

	// 创建模拟数据
	mockInfo := &live.Info{
		HostName: "示例主播",
		RoomName: "示例直播间标题",
	}

	// 创建一个模拟的 Live 对象用于预览
	mockLive := &mockLiveForPreview{
		platformCNName: "示例平台",
		options: &live.Options{
			NickName: "示例昵称",
		},
	}
	mockInfo.Live = mockLive

	// 执行模板
	buf := new(bytes.Buffer)
	if err := tmpl.Execute(buf, mockInfo); err != nil {
		writeJSON(writer, map[string]interface{}{
			"success":    false,
			"error":      "模板执行错误: " + err.Error(),
			"error_type": "execute_error",
		})
		return
	}

	// 计算最终路径
	absOutPutPath, _ := filepath.Abs(outPutPath)
	previewPath := filepath.Join(absOutPutPath, buf.String())

	writeJSON(writer, map[string]interface{}{
		"success":       true,
		"preview_path":  previewPath,
		"relative_path": buf.String(),
		"base_path":     absOutPutPath,
	})
}

// mockLiveForPreview 用于模板预览的模拟 Live 对象
type mockLiveForPreview struct {
	platformCNName string
	options        *live.Options
}

func (m *mockLiveForPreview) GetPlatformCNName() string {
	return m.platformCNName
}

func (m *mockLiveForPreview) GetOptions() *live.Options {
	return m.options
}

// 实现 live.Live 接口的其他方法（返回空值）
func (m *mockLiveForPreview) SetLiveIdByString(string)     {}
func (m *mockLiveForPreview) GetLiveId() types.LiveID      { return "" }
func (m *mockLiveForPreview) GetRawUrl() string            { return "" }
func (m *mockLiveForPreview) GetInfo() (*live.Info, error) { return nil, nil }
func (m *mockLiveForPreview) GetInfoWithInterval(ctx context.Context) (*live.Info, error) {
	return nil, nil
}
func (m *mockLiveForPreview) GetStreamUrls() ([]*url.URL, error)             { return nil, nil }
func (m *mockLiveForPreview) Close()                                         {}
func (m *mockLiveForPreview) GetStreamInfos() ([]*live.StreamUrlInfo, error) { return nil, nil }
func (m *mockLiveForPreview) GetLastStartTime() time.Time                    { return time.Time{} }
func (m *mockLiveForPreview) SetLastStartTime(time.Time)                     {}
func (m *mockLiveForPreview) GetLastEndTime() time.Time                      { return time.Time{} }
func (m *mockLiveForPreview) SetLastEndTime(time.Time)                       {}
func (m *mockLiveForPreview) UpdateLiveOptionsbyConfig(ctx context.Context, room *configs.LiveRoom) error {
	return nil
}
func (m *mockLiveForPreview) GetLogger() *livelogger.LiveLogger { return nil }

// updateConfig 更新配置（支持部分更新）
func updateConfig(writer http.ResponseWriter, r *http.Request) {
	b, err := io.ReadAll(r.Body)
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: err.Error(),
		})
		return
	}

	var updates map[string]interface{}
	if err := json.Unmarshal(b, &updates); err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: "无效的JSON格式: " + err.Error(),
		})
		return
	}

	_, err = configs.UpdateWithRetry(func(c *configs.Config) error {
		// 应用更新到配置
		if err := applyConfigUpdates(c, updates); err != nil {
			return err
		}
		// 校验配置
		return c.Verify()
	}, 3, 10*time.Millisecond)

	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "更新配置失败: " + err.Error(),
		})
		return
	}

	writeJSON(writer, commonResp{
		Data: "OK",
	})
}

// applyConfigUpdates 将更新应用到配置
func applyConfigUpdates(c *configs.Config, updates map[string]interface{}) error {
	// 处理 RPC 配置
	if rpc, ok := updates["rpc"].(map[string]interface{}); ok {
		if enable, ok := rpc["enable"].(bool); ok {
			c.RPC.Enable = enable
		}
		if bind, ok := rpc["bind"].(string); ok {
			c.RPC.Bind = bind
		}
	}

	// 处理基本配置
	if debug, ok := updates["debug"].(bool); ok {
		c.Debug = debug
	}
	if interval, ok := updates["interval"].(float64); ok {
		c.Interval = int(interval)
	}
	if outPutPath, ok := updates["out_put_path"].(string); ok {
		c.OutPutPath = outPutPath
	}
	if ffmpegPath, ok := updates["ffmpeg_path"].(string); ok {
		c.FfmpegPath = ffmpegPath
	}
	if outputTmpl, ok := updates["out_put_tmpl"].(string); ok {
		c.OutputTmpl = outputTmpl
	}
	if timeoutSec, ok := updates["timeout_in_seconds"].(float64); ok {
		c.TimeoutInUs = int(timeoutSec * 1000000)
	}
	if appDataPath, ok := updates["app_data_path"].(string); ok {
		c.AppDataPath = appDataPath
	}
	if readOnlyToolFolder, ok := updates["read_only_tool_folder"].(string); ok {
		c.ReadOnlyToolFolder = readOnlyToolFolder
	}
	if toolRootFolder, ok := updates["tool_root_folder"].(string); ok {
		c.ToolRootFolder = toolRootFolder
	}
	if danmakuEnable, ok := updates["danmaku_enable"].(bool); ok {
		c.DanmakuEnable = danmakuEnable
	}
	if danmaku, ok := updates["danmaku"].(map[string]interface{}); ok {
		if fontSize, ok := danmaku["font_size"].(float64); ok {
			c.Danmaku.FontSize = int(fontSize)
		}
		if fontName, ok := danmaku["font_name"].(string); ok {
			c.Danmaku.FontName = fontName
		}
		if displayMode, ok := danmaku["scroll_area"].(string); ok {
			c.Danmaku.ScrollArea = displayMode
		}
		if scrollTime, ok := danmaku["scroll_time"].(float64); ok {
			c.Danmaku.ScrollTime = int(scrollTime)
		}
		if resolution, ok := danmaku["resolution"].(string); ok {
			c.Danmaku.Resolution = resolution
		}
		if outline, ok := danmaku["outline"].(float64); ok {
			c.Danmaku.Outline = int(outline)
		}
		if opacity, ok := danmaku["opacity"].(float64); ok {
			c.Danmaku.Opacity = int(opacity)
		}
		if recordGift, ok := danmaku["record_gift"].(bool); ok {
			c.Danmaku.RecordGift = configs.BoolPtr(recordGift)
		} else if _, exists := danmaku["record_gift"]; exists && danmaku["record_gift"] == nil {
			c.Danmaku.RecordGift = nil
		}
		if recordGuard, ok := danmaku["record_guard"].(bool); ok {
			c.Danmaku.RecordGuard = configs.BoolPtr(recordGuard)
		} else if _, exists := danmaku["record_guard"]; exists && danmaku["record_guard"] == nil {
			c.Danmaku.RecordGuard = nil
		}
		if recordSuperChat, ok := danmaku["record_super_chat"].(bool); ok {
			c.Danmaku.RecordSuperChat = configs.BoolPtr(recordSuperChat)
		} else if _, exists := danmaku["record_super_chat"]; exists && danmaku["record_super_chat"] == nil {
			c.Danmaku.RecordSuperChat = nil
		}
		if guardPosition, ok := danmaku["guard_position"].(string); ok {
			c.Danmaku.GuardPosition = guardPosition
		}
		if scPosition, ok := danmaku["sc_position"].(string); ok {
			c.Danmaku.ScPosition = scPosition
		}
		if err := c.Danmaku.Validate(); err != nil {
			return fmt.Errorf("弹幕参数无效: %w", err)
		}
	}
	if soopAuth, ok := updates["sooplive_auth"].(map[string]interface{}); ok {
		if username, ok := soopAuth["username"].(string); ok {
			c.SoopLiveAuth.Username = username
		}
		if password, ok := soopAuth["password"].(string); ok {
			c.SoopLiveAuth.Password = password
		}
	}

	// 处理日志配置
	if log, ok := updates["log"].(map[string]interface{}); ok {
		if outPutFolder, ok := log["out_put_folder"].(string); ok {
			c.Log.OutPutFolder = outPutFolder
		}
		if saveLastLog, ok := log["save_last_log"].(bool); ok {
			c.Log.SaveLastLog = saveLastLog
		}
		if saveEveryLog, ok := log["save_every_log"].(bool); ok {
			c.Log.SaveEveryLog = saveEveryLog
		}
		if rotateDays, ok := log["rotate_days"].(float64); ok {
			c.Log.RotateDays = int(rotateDays)
		}
	}

	// 处理功能特性配置
	if feature, ok := updates["feature"].(map[string]interface{}); ok {
		if downloaderType, ok := feature["downloader_type"].(string); ok {
			c.Feature.DownloaderType = configs.ParseDownloaderType(downloaderType)
		}
		if useNativeFlvParser, ok := feature["use_native_flv_parser"].(bool); ok {
			c.Feature.UseNativeFlvParser = useNativeFlvParser
		}
		if removeSymbolOther, ok := feature["remove_symbol_other_character"].(bool); ok {
			c.Feature.RemoveSymbolOtherCharacter = removeSymbolOther
		}
		if saveAsTS, ok := feature["save_as_ts"].(bool); ok {
			c.Feature.SaveAsTS = saveAsTS
		}
	}

	// 处理视频分割策略
	if vss, ok := updates["video_split_strategies"].(map[string]interface{}); ok {
		if onRoomNameChanged, ok := vss["on_room_name_changed"].(bool); ok {
			c.VideoSplitStrategies.OnRoomNameChanged = onRoomNameChanged
		}
		if maxDuration, ok := vss["max_duration"].(float64); ok {
			c.VideoSplitStrategies.MaxDuration = time.Duration(maxDuration)
		}
		if maxFileSize, ok := vss["max_file_size"].(float64); ok {
			c.VideoSplitStrategies.MaxFileSize = configs.ByteSize(int64(maxFileSize))
		} else if maxFileSizeStr, ok := vss["max_file_size"].(string); ok {
			if parsed, err := configs.ParseByteSize(maxFileSizeStr); err == nil {
				c.VideoSplitStrategies.MaxFileSize = parsed
			}
		}
	}

	// 处理录制完成后动作
	if orf, ok := updates["on_record_finished"].(map[string]interface{}); ok {
		if convertToMp4, ok := orf["convert_to_mp4"].(bool); ok {
			c.OnRecordFinished.ConvertToMp4 = convertToMp4
		}
		if deleteFlv, ok := orf["delete_flv_after_convert"].(bool); ok {
			c.OnRecordFinished.DeleteFlvAfterConvert = deleteFlv
		}
		if customCmd, ok := orf["custom_commandline"].(string); ok {
			c.OnRecordFinished.CustomCommandline = customCmd
		}
		if fixFlv, ok := orf["fix_flv_at_first"].(bool); ok {
			c.OnRecordFinished.FixFlvAtFirst = fixFlv
		}
		if burnSubtitles, ok := orf["burn_subtitles"].(bool); ok {
			c.OnRecordFinished.BurnSubtitles = burnSubtitles
		}
		if codec, ok := orf["burn_subtitles_codec"].(string); ok {
			c.OnRecordFinished.BurnSubtitlesCodec = codec
		}
		if crf, ok := orf["burn_subtitles_crf"].(string); ok {
			c.OnRecordFinished.BurnSubtitlesCrf = crf
		}
		if preset, ok := orf["burn_subtitles_preset"].(string); ok {
			c.OnRecordFinished.BurnSubtitlesPreset = preset
		}
		if deleteAss, ok := orf["burn_delete_ass"].(bool); ok {
			c.OnRecordFinished.BurnDeleteAss = deleteAss
		}
		if deleteSource, ok := orf["burn_delete_source"].(bool); ok {
			c.OnRecordFinished.BurnDeleteSource = deleteSource
		}
		if saveCover, ok := orf["save_cover"].(bool); ok {
			c.OnRecordFinished.SaveCover = saveCover
		}
		if uploadTiming, ok := orf["upload_timing"].(string); ok {
			c.OnRecordFinished.UploadTiming = configs.UploadTiming(uploadTiming)
		}
		// 处理云上传配置
		if cloudUpload, ok := orf["cloud_upload"].(map[string]interface{}); ok {
			if enable, ok := cloudUpload["enable"].(bool); ok {
				c.OnRecordFinished.CloudUpload.Enable = enable
			}
			if apiUrl, ok := cloudUpload["api_url"].(string); ok {
				c.OnRecordFinished.CloudUpload.ApiUrl = apiUrl
			}
			if username, ok := cloudUpload["username"].(string); ok {
				c.OnRecordFinished.CloudUpload.Username = username
			}
			if password, ok := cloudUpload["password"].(string); ok {
				c.OnRecordFinished.CloudUpload.Password = password
			}
			if storageName, ok := cloudUpload["storage_name"].(string); ok {
				c.OnRecordFinished.CloudUpload.StorageName = storageName
			}
			if uploadPathTmpl, ok := cloudUpload["upload_path_tmpl"].(string); ok {
				c.OnRecordFinished.CloudUpload.UploadPathTmpl = uploadPathTmpl
			}
			if deleteAfterUpload, ok := cloudUpload["delete_after_upload"].(bool); ok {
				c.OnRecordFinished.CloudUpload.DeleteAfterUpload = deleteAfterUpload
			}
			if uploadSpeedLimit, ok := cloudUpload["upload_speed_limit"].(float64); ok {
				c.OnRecordFinished.CloudUpload.UploadSpeedLimit = int(uploadSpeedLimit)
			}
			if maxConcurrentUploads, ok := cloudUpload["max_concurrent_uploads"].(float64); ok {
				c.OnRecordFinished.CloudUpload.MaxConcurrentUploads = int(maxConcurrentUploads)
			}
			// 处理额外存储列表
			if additionalStorages, ok := cloudUpload["additional_storages"].([]interface{}); ok {
				storages := make([]string, 0, len(additionalStorages))
				for _, s := range additionalStorages {
					if str, ok := s.(string); ok && str != "" {
						storages = append(storages, str)
					}
				}
				c.OnRecordFinished.CloudUpload.AdditionalStorages = storages
			}
		}
	}

	// 处理通知配置
	if notify, ok := updates["notify"].(map[string]interface{}); ok {
		if sendRecordingSummary, ok := notify["send_recording_summary"].(bool); ok {
			c.Notify.SendRecordingSummary = sendRecordingSummary
		}
		if telegram, ok := notify["telegram"].(map[string]interface{}); ok {
			if enable, ok := telegram["enable"].(bool); ok {
				c.Notify.Telegram.Enable = enable
			}
			if withNotification, ok := telegram["withNotification"].(bool); ok {
				c.Notify.Telegram.WithNotification = withNotification
			}
			if botToken, ok := telegram["botToken"].(string); ok {
				c.Notify.Telegram.BotToken = botToken
			}
			if chatID, ok := telegram["chatID"].(string); ok {
				c.Notify.Telegram.ChatID = chatID
			}
		}
		if email, ok := notify["email"].(map[string]interface{}); ok {
			if enable, ok := email["enable"].(bool); ok {
				c.Notify.Email.Enable = enable
			}
			if smtpHost, ok := email["smtpHost"].(string); ok {
				c.Notify.Email.SMTPHost = smtpHost
			}
			if smtpPort, ok := email["smtpPort"].(float64); ok {
				c.Notify.Email.SMTPPort = int(smtpPort)
			}
			if senderEmail, ok := email["senderEmail"].(string); ok {
				c.Notify.Email.SenderEmail = senderEmail
			}
			if senderPassword, ok := email["senderPassword"].(string); ok {
				c.Notify.Email.SenderPassword = senderPassword
			}
			if recipientEmail, ok := email["recipientEmail"].(string); ok {
				c.Notify.Email.RecipientEmail = recipientEmail
			}
		}
		if barkCfg, ok := notify["bark"].(map[string]interface{}); ok {
			if enable, ok := barkCfg["enable"].(bool); ok {
				c.Notify.Bark.Enable = enable
			}
			if serverURL, ok := barkCfg["serverURL"].(string); ok {
				c.Notify.Bark.ServerURL = serverURL
			}
			if deviceKey, ok := barkCfg["deviceKey"].(string); ok {
				c.Notify.Bark.DeviceKey = deviceKey
			}
			if sound, ok := barkCfg["sound"].(string); ok {
				c.Notify.Bark.Sound = sound
			}
			if group, ok := barkCfg["group"].(string); ok {
				c.Notify.Bark.Group = group
			}
			if icon, ok := barkCfg["icon"].(string); ok {
				c.Notify.Bark.Icon = icon
			}
			if level, ok := barkCfg["level"].(string); ok {
				c.Notify.Bark.Level = level
			}
		}
	}

	// 处理代理配置
	if proxy, ok := updates["proxy"].(map[string]interface{}); ok {
		if enable, ok := proxy["enable"].(bool); ok {
			c.Proxy.Enable = enable
		}
		if url, ok := proxy["url"].(string); ok {
			c.Proxy.URL = url
		}
	}

	// 处理全局流偏好配置
	if streamPref, ok := updates["stream_preference"].(map[string]interface{}); ok {
		// 处理 quality
		if quality, ok := streamPref["quality"].(string); ok {
			if quality == "" {
				c.StreamPreference.Quality = nil
			} else {
				c.StreamPreference.Quality = &quality
			}
		}

		// 处理 attributes
		if attrs, ok := streamPref["attributes"].(map[string]interface{}); ok {
			if len(attrs) == 0 {
				c.StreamPreference.Attributes = nil
			} else {
				attrMap := make(map[string]string)
				for k, v := range attrs {
					if strVal, ok := v.(string); ok {
						attrMap[k] = strVal
					}
				}
				if len(attrMap) > 0 {
					c.StreamPreference.Attributes = &attrMap
				} else {
					c.StreamPreference.Attributes = nil
				}
			}
		}
	}

	// 处理自动更新配置
	if update, ok := updates["update"].(map[string]interface{}); ok {
		if autoCheck, ok := update["auto_check"].(bool); ok {
			c.Update.AutoCheck = autoCheck
		}
		if checkIntervalHours, ok := update["check_interval_hours"].(float64); ok {
			c.Update.CheckIntervalHours = int(checkIntervalHours)
		}
		if autoDownload, ok := update["auto_download"].(bool); ok {
			c.Update.AutoDownload = autoDownload
		}
		if includePrerelease, ok := update["include_prerelease"].(bool); ok {
			c.Update.IncludePrerelease = includePrerelease
		}
	}

	return nil
}

// updatePlatformConfig 更新平台配置
func updatePlatformConfig(writer http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	platformKey := vars["platform"]

	b, err := io.ReadAll(r.Body)
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: err.Error(),
		})
		return
	}

	var updates map[string]interface{}
	if err := json.Unmarshal(b, &updates); err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: "无效的JSON格式: " + err.Error(),
		})
		return
	}

	_, err = configs.UpdateWithRetry(func(c *configs.Config) error {
		if c.PlatformConfigs == nil {
			c.PlatformConfigs = make(map[string]configs.PlatformConfig)
		}

		pc := c.PlatformConfigs[platformKey]

		// 更新平台配置
		if name, ok := updates["name"].(string); ok {
			pc.Name = name
		}
		if minInterval, ok := updates["min_access_interval_sec"].(float64); ok {
			pc.MinAccessIntervalSec = int(minInterval)
		}
		// 使用助手函数更新可覆盖配置
		applyOverridableConfigUpdates(&pc.OverridableConfig, updates)

		// 验证弹幕配置有效性
		if pc.Danmaku != nil {
			if err := pc.Danmaku.Validate(); err != nil {
				return fmt.Errorf("弹幕参数无效: %w", err)
			}
		}

		c.PlatformConfigs[platformKey] = pc
		return nil
	}, 3, 10*time.Millisecond)

	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "更新平台配置失败: " + err.Error(),
		})
		return
	}

	writeJSON(writer, commonResp{
		Data: "OK",
	})
}

// deletePlatformConfig 删除平台配置
func deletePlatformConfig(writer http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	platformKey := vars["platform"]

	_, err := configs.UpdateWithRetry(func(c *configs.Config) error {
		if c.PlatformConfigs != nil {
			delete(c.PlatformConfigs, platformKey)
		}
		return nil
	}, 3, 10*time.Millisecond)

	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "删除平台配置失败: " + err.Error(),
		})
		return
	}

	writeJSON(writer, commonResp{
		Data: "OK",
	})
}

// updateRoomConfigById 通过 ID 更新直播间配置
func updateRoomConfigById(writer http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	liveId := vars["id"]

	b, err := io.ReadAll(r.Body)
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: err.Error(),
		})
		return
	}

	var updates map[string]interface{}
	if err := json.Unmarshal(b, &updates); err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: "无效的JSON格式: " + err.Error(),
		})
		return
	}

	_, err = configs.UpdateWithRetry(func(c *configs.Config) error {
		// 查找直播间（先按 LiveId，回退按 URL 计算的 hash）
		roomIdx := -1
		for i, room := range c.LiveRooms {
			if string(room.LiveId) == liveId {
				roomIdx = i
				break
			}
		}
		// LiveId 可能未初始化（yaml:"-"），回退按 URL 计算 hash 匹配
		if roomIdx == -1 {
			for i, room := range c.LiveRooms {
				if parsedUrl, err := url.Parse(room.Url); err == nil {
					computedId := string(types.LiveID(utils.GetMd5String([]byte(parsedUrl.Host + parsedUrl.Path))))
					if computedId == liveId {
						roomIdx = i
						break
					}
				}
			}
		}

		if roomIdx == -1 {
			return fmt.Errorf("未找到直播间: %s", liveId)
		}

		room := &c.LiveRooms[roomIdx]

		// 更新直播间特有字段
		if url, ok := updates["url"].(string); ok {
			room.Url = url
		}
		if isListening, ok := updates["is_listening"].(bool); ok {
			room.IsListening = isListening
		}
		if quality, ok := updates["quality"].(float64); ok {
			room.Quality = int(quality)
		}
		if audioOnly, ok := updates["audio_only"].(bool); ok {
			room.AudioOnly = audioOnly
		}
		if nickName, ok := updates["nick_name"].(string); ok {
			room.NickName = nickName
		}

		// 更新可覆盖配置
		applyOverridableConfigUpdates(&room.OverridableConfig, updates)

		// 验证弹幕配置有效性
		if room.Danmaku != nil {
			if err := room.Danmaku.Validate(); err != nil {
				return fmt.Errorf("弹幕参数无效: %w", err)
			}
		}

		return nil
	}, 3, 10*time.Millisecond)

	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "更新直播间配置失败: " + err.Error(),
		})
		return
	}

	writeJSON(writer, commonResp{
		Data: "OK",
	})
}

// applyOverridableConfigUpdates 统一处理可覆盖配置的更新
func applyOverridableConfigUpdates(oc *configs.OverridableConfig, updates map[string]interface{}) {
	if interval, ok := updates["interval"].(float64); ok {
		val := int(interval)
		oc.Interval = &val
	}
	if outPutPath, ok := updates["out_put_path"].(string); ok {
		if outPutPath == "" {
			oc.OutPutPath = nil
		} else {
			oc.OutPutPath = &outPutPath
		}
	}
	if ffmpegPath, ok := updates["ffmpeg_path"].(string); ok {
		if ffmpegPath == "" {
			oc.FfmpegPath = nil
		} else {
			oc.FfmpegPath = &ffmpegPath
		}
	}
	if outPutTmpl, ok := updates["out_put_tmpl"].(string); ok {
		if outPutTmpl == "" {
			oc.OutputTmpl = nil
		} else {
			oc.OutputTmpl = &outPutTmpl
		}
	}
	if timeoutSec, ok := updates["timeout_in_seconds"].(float64); ok {
		val := int(timeoutSec * 1000000)
		oc.TimeoutInUs = &val
	}

	// 处理 feature 配置（包括 downloader_type）
	if feature, ok := updates["feature"].(map[string]interface{}); ok {
		if oc.Feature == nil {
			oc.Feature = &configs.Feature{}
		}
		if downloaderType, ok := feature["downloader_type"].(string); ok {
			oc.Feature.DownloaderType = configs.ParseDownloaderType(downloaderType)
		}
		if useNativeFlvParser, ok := feature["use_native_flv_parser"].(bool); ok {
			oc.Feature.UseNativeFlvParser = useNativeFlvParser
		}
		if enableFlvProxySegment, ok := feature["enable_flv_proxy_segment"].(bool); ok {
			oc.Feature.EnableFlvProxySegment = enableFlvProxySegment
		}
		if saveAsTS, ok := feature["save_as_ts"].(bool); ok {
			oc.Feature.SaveAsTS = saveAsTS
		}
	}

	// 也支持直接在顶层设置 downloader_type（简化前端逻辑）
	if downloaderType, ok := updates["downloader_type"].(string); ok {
		if oc.Feature == nil {
			oc.Feature = &configs.Feature{}
		}
		oc.Feature.DownloaderType = configs.ParseDownloaderType(downloaderType)
	}

	// 处理 stream_preference 配置
	if streamPref, ok := updates["stream_preference"].(map[string]interface{}); ok {
		if oc.StreamPreference == nil {
			oc.StreamPreference = &configs.StreamPreference{}
		}

		// 处理 quality
		if quality, ok := streamPref["quality"].(string); ok {
			if quality == "" {
				oc.StreamPreference.Quality = nil
			} else {
				oc.StreamPreference.Quality = &quality
			}
		}

		// 处理 attributes
		if attrs, ok := streamPref["attributes"].(map[string]interface{}); ok {
			if len(attrs) == 0 {
				oc.StreamPreference.Attributes = nil
			} else {
				attrMap := make(map[string]string)
				for k, v := range attrs {
					if strVal, ok := v.(string); ok {
						attrMap[k] = strVal
					}
				}
				if len(attrMap) > 0 {
					oc.StreamPreference.Attributes = &attrMap
				} else {
					oc.StreamPreference.Attributes = nil
				}
			}
		}

		// 如果 quality 和 attributes 都为空，清空整个 StreamPreference
		if oc.StreamPreference.Quality == nil && oc.StreamPreference.Attributes == nil {
			oc.StreamPreference = nil
		}
	}

	// 处理 danmaku_enable 配置
	if danmakuEnable, ok := updates["danmaku_enable"].(bool); ok {
		oc.DanmakuEnable = &danmakuEnable
	} else if _, exists := updates["danmaku_enable"]; exists && updates["danmaku_enable"] == nil {
		// 显式 null → 清除覆盖，恢复继承
		oc.DanmakuEnable = nil
	}

	// 处理 danmaku 弹幕参数配置
	if danmaku, ok := updates["danmaku"].(map[string]interface{}); ok {
		if oc.Danmaku == nil {
			// 从全局默认值初始化，避免未设置的字段被覆盖为零值
			defaultCfg := configs.GetDefaultDanmakuConfig()
			oc.Danmaku = &defaultCfg
		}
		if fontSize, ok := danmaku["font_size"].(float64); ok {
			oc.Danmaku.FontSize = int(fontSize)
		}
		if fontName, ok := danmaku["font_name"].(string); ok {
			oc.Danmaku.FontName = fontName
		}
		if scrollArea, ok := danmaku["scroll_area"].(string); ok {
			oc.Danmaku.ScrollArea = scrollArea
		}
		if scrollTime, ok := danmaku["scroll_time"].(float64); ok {
			oc.Danmaku.ScrollTime = int(scrollTime)
		}
		if resolution, ok := danmaku["resolution"].(string); ok {
			oc.Danmaku.Resolution = resolution
		}
		if outline, ok := danmaku["outline"].(float64); ok {
			oc.Danmaku.Outline = int(outline)
		}
		if opacity, ok := danmaku["opacity"].(float64); ok {
			oc.Danmaku.Opacity = int(opacity)
		}
		if recordGift, ok := danmaku["record_gift"].(bool); ok {
			oc.Danmaku.RecordGift = configs.BoolPtr(recordGift)
		} else if _, exists := danmaku["record_gift"]; exists && danmaku["record_gift"] == nil {
			oc.Danmaku.RecordGift = nil
		}
		if recordGuard, ok := danmaku["record_guard"].(bool); ok {
			oc.Danmaku.RecordGuard = configs.BoolPtr(recordGuard)
		} else if _, exists := danmaku["record_guard"]; exists && danmaku["record_guard"] == nil {
			oc.Danmaku.RecordGuard = nil
		}
		if recordSuperChat, ok := danmaku["record_super_chat"].(bool); ok {
			oc.Danmaku.RecordSuperChat = configs.BoolPtr(recordSuperChat)
		} else if _, exists := danmaku["record_super_chat"]; exists && danmaku["record_super_chat"] == nil {
			oc.Danmaku.RecordSuperChat = nil
		}
		if guardPosition, ok := danmaku["guard_position"].(string); ok {
			oc.Danmaku.GuardPosition = guardPosition
		}
		if scPosition, ok := danmaku["sc_position"].(string); ok {
			oc.Danmaku.ScPosition = scPosition
		}
	} else if _, exists := updates["danmaku"]; exists && updates["danmaku"] == nil {
		// 显式 null → 清除覆盖，恢复继承
		oc.Danmaku = nil
	}
}

// updateRoomConfig 更新直播间配置
func updateRoomConfig(writer http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	roomUrl := vars["url"]

	// URL 解码
	decodedUrl, err := url.QueryUnescape(roomUrl)
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: "无效的URL: " + err.Error(),
		})
		return
	}

	b, err := io.ReadAll(r.Body)
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: err.Error(),
		})
		return
	}

	var updates map[string]interface{}
	if err := json.Unmarshal(b, &updates); err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: "无效的JSON格式: " + err.Error(),
		})
		return
	}

	_, err = configs.UpdateWithRetry(func(c *configs.Config) error {
		room, err := c.GetLiveRoomByUrl(decodedUrl)
		if err != nil {
			return errors.New("找不到直播间: " + decodedUrl)
		}

		// 更新直播间配置
		if quality, ok := updates["quality"].(float64); ok {
			room.Quality = int(quality)
		}
		if audioOnly, ok := updates["audio_only"].(bool); ok {
			room.AudioOnly = audioOnly
		}
		if nickName, ok := updates["nick_name"].(string); ok {
			room.NickName = nickName
		}

		// 处理可覆盖配置（弹幕、interval、outPutPath、ffmpegPath 等）
		applyOverridableConfigUpdates(&room.OverridableConfig, updates)

		// 验证弹幕配置
		if room.Danmaku != nil {
			if err := room.Danmaku.Validate(); err != nil {
				return fmt.Errorf("弹幕配置无效: %w", err)
			}
		}

		return nil
	}, 3, 10*time.Millisecond)

	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "更新直播间配置失败: " + err.Error(),
		})
		return
	}

	writeJSON(writer, commonResp{
		Data: "OK",
	})
}

// getMemoryStats 获取内存统计信息
func getMemoryStats(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())

	// 获取所有 parser 的 PID
	var pids []int
	if rm, ok := inst.RecorderManager.(recorders.Manager); ok {
		pids = rm.GetAllParserPIDs()
	}

	// 添加所有通过 tools 包启动的子进程 PID（如 bililive-tools、klive 等）
	toolsPIDs := tools.GetAllProcessPIDs()
	pids = append(pids, toolsPIDs...)

	stats, err := memstats.GetMemoryStats(pids)
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: fmt.Sprintf("获取内存统计失败: %v", err),
		})
		return
	}

	writeJSON(writer, stats)
}
