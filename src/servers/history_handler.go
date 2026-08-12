package servers

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/gorilla/mux"

	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/livestate"
	"github.com/bililive-go/bililive-go/src/types"
)

// HistoryEvent 统一的历史事件格式
type HistoryEvent struct {
	ID        int64     `json:"id"`
	Type      string    `json:"type"`      // "session" 或 "name_change"
	Timestamp time.Time `json:"timestamp"` // 事件时间
	Data      any       `json:"data"`      // 事件详情
}

// getLiveSessionHistory 获取直播间的会话历史记录
func getLiveSessionHistory(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	vars := mux.Vars(r)
	liveID := vars["id"]

	if !inst.Lives.Has(types.LiveID(liveID)) {
		writeJsonWithStatusCode(writer, http.StatusNotFound, commonResp{
			ErrNo:  http.StatusNotFound,
			ErrMsg: fmt.Sprintf("live id: %s can not find", liveID),
		})
		return
	}

	limit := 20
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 {
			limit = parsedLimit
		}
	}

	manager, ok := inst.LiveStateManager.(*livestate.Manager)
	if !ok || manager == nil {
		writeJsonWithStatusCode(writer, http.StatusServiceUnavailable, commonResp{
			ErrNo:  http.StatusServiceUnavailable,
			ErrMsg: "状态持久化功能未启用",
		})
		return
	}

	sessions := manager.GetSessionHistory(liveID, limit)
	writeJSON(writer, map[string]interface{}{
		"live_id":  liveID,
		"sessions": sessions,
		"total":    len(sessions),
	})
}

// getLiveNameHistory 获取直播间的名称变更历史
func getLiveNameHistory(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	vars := mux.Vars(r)
	liveID := vars["id"]

	if !inst.Lives.Has(types.LiveID(liveID)) {
		writeJsonWithStatusCode(writer, http.StatusNotFound, commonResp{
			ErrNo:  http.StatusNotFound,
			ErrMsg: fmt.Sprintf("live id: %s can not find", liveID),
		})
		return
	}

	limit := 50
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if parsedLimit, err := strconv.Atoi(limitStr); err == nil && parsedLimit > 0 {
			limit = parsedLimit
		}
	}

	manager, ok := inst.LiveStateManager.(*livestate.Manager)
	if !ok || manager == nil {
		writeJsonWithStatusCode(writer, http.StatusServiceUnavailable, commonResp{
			ErrNo:  http.StatusServiceUnavailable,
			ErrMsg: "状态持久化功能未启用",
		})
		return
	}

	changes := manager.GetNameHistory(liveID, limit)
	writeJSON(writer, map[string]interface{}{
		"live_id":      liveID,
		"name_changes": changes,
		"total":        len(changes),
	})
}

// getLiveHistory 获取直播间的历史事件（统一接口，支持分页和筛选）
func getLiveHistory(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	vars := mux.Vars(r)
	liveID := vars["id"]

	if !inst.Lives.Has(types.LiveID(liveID)) {
		writeJsonWithStatusCode(writer, http.StatusNotFound, commonResp{
			ErrNo:  http.StatusNotFound,
			ErrMsg: fmt.Sprintf("live id: %s can not find", liveID),
		})
		return
	}

	manager, ok := inst.LiveStateManager.(*livestate.Manager)
	if !ok || manager == nil {
		writeJsonWithStatusCode(writer, http.StatusServiceUnavailable, commonResp{
			ErrNo:  http.StatusServiceUnavailable,
			ErrMsg: "状态持久化功能未启用",
		})
		return
	}

	query := r.URL.Query()

	page := 1
	pageSize := 20
	if pageStr := query.Get("page"); pageStr != "" {
		if p, err := strconv.Atoi(pageStr); err == nil && p > 0 {
			page = p
		}
	}
	if pageSizeStr := query.Get("page_size"); pageSizeStr != "" {
		if ps, err := strconv.Atoi(pageSizeStr); err == nil && ps > 0 && ps <= 100 {
			pageSize = ps
		}
	}

	// 时间范围参数
	var startTime, endTime time.Time
	if startStr := query.Get("start_time"); startStr != "" {
		if t, err := time.Parse(time.RFC3339, startStr); err == nil {
			startTime = t
		} else if ts, err := strconv.ParseInt(startStr, 10, 64); err == nil {
			startTime = time.Unix(ts, 0)
		}
	}
	if endStr := query.Get("end_time"); endStr != "" {
		if t, err := time.Parse(time.RFC3339, endStr); err == nil {
			endTime = t
		} else if ts, err := strconv.ParseInt(endStr, 10, 64); err == nil {
			endTime = time.Unix(ts, 0)
		}
	}

	// 事件类型筛选
	eventTypes := query["type"]
	includeSession := len(eventTypes) == 0 || containsStr(eventTypes, "session")
	includeNameChange := len(eventTypes) == 0 || containsStr(eventTypes, "name_change")

	var events []HistoryEvent

	if includeSession {
		sessions := manager.GetSessionHistory(liveID, 1000)
		for _, s := range sessions {
			eventTime := s.StartTime
			if !startTime.IsZero() && eventTime.Before(startTime) {
				continue
			}
			if !endTime.IsZero() && eventTime.After(endTime) {
				continue
			}
			events = append(events, HistoryEvent{
				ID:        s.ID,
				Type:      "session",
				Timestamp: eventTime,
				Data:      s,
			})
		}
	}

	if includeNameChange {
		changes := manager.GetNameHistory(liveID, 1000)
		for _, c := range changes {
			if !startTime.IsZero() && c.ChangedAt.Before(startTime) {
				continue
			}
			if !endTime.IsZero() && c.ChangedAt.After(endTime) {
				continue
			}
			events = append(events, HistoryEvent{
				ID:        c.ID,
				Type:      "name_change",
				Timestamp: c.ChangedAt,
				Data:      c,
			})
		}
	}

	// 按时间倒序排序
	sort.Slice(events, func(i, j int) bool {
		return events[i].Timestamp.After(events[j].Timestamp)
	})

	total := len(events)
	totalPages := (total + pageSize - 1) / pageSize
	startIdx := (page - 1) * pageSize
	endIdx := startIdx + pageSize
	if startIdx >= total {
		events = []HistoryEvent{}
	} else {
		if endIdx > total {
			endIdx = total
		}
		events = events[startIdx:endIdx]
	}

	writeJSON(writer, map[string]interface{}{
		"live_id":     liveID,
		"events":      events,
		"total":       total,
		"page":        page,
		"page_size":   pageSize,
		"total_pages": totalPages,
	})
}

// containsStr 检查切片是否包含某个值
func containsStr(slice []string, val string) bool {
	for _, item := range slice {
		if item == val {
			return true
		}
	}
	return false
}
