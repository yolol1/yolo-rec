package servers

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/gorilla/mux"

	"github.com/bililive-go/bililive-go/src/instance"
	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/playback"
	"github.com/bililive-go/bililive-go/src/types"
)

const (
	playbackResponseURLKey     = "url"
	playbackResponseQualityKey = "quality"
)

type livePlaybackHandler struct {
	manager *playback.Manager
}

// RegisterPlaybackHandlers 注册直播间播放地址创建与代理路由。
func RegisterPlaybackHandlers(router *mux.Router) {
	handler := &livePlaybackHandler{manager: playback.NewManager()}

	router.HandleFunc("/lives/{id}/playback", handler.createPlayback).Methods(http.MethodPost)
	router.HandleFunc("/playback/{token}/stream.flv", handler.proxyFLV).Methods(http.MethodGet, http.MethodHead)
	router.HandleFunc("/playback/{token}/stream.m3u8", handler.proxyHLSPlaylist).Methods(http.MethodGet, http.MethodHead)
	router.HandleFunc("/playback/{token}/hls/media", handler.proxyHLSMedia).Methods(http.MethodGet, http.MethodHead)
}

func (h *livePlaybackHandler) createPlayback(w http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	liveID := types.LiveID(mux.Vars(r)["id"])
	room, ok := inst.Lives.Get(liveID)
	if !ok {
		writeJsonWithStatusCode(w, http.StatusNotFound, commonResp{
			ErrNo:  http.StatusNotFound,
			ErrMsg: fmt.Sprintf("live id: %s can not find", liveID),
		})
		return
	}

	resolver := func(ctx context.Context) (*live.StreamUrlInfo, error) {
		infos, err := live.ResolveStreamInfos(room)
		if err != nil {
			return nil, err
		}
		selected := live.SelectPreferredStream(room, infos)
		if selected == nil {
			return nil, errors.New("当前没有可用的直播流")
		}
		return selected, nil
	}

	info, err := h.manager.Create(r.Context(), string(liveID), resolver)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, live.ErrLiveOffline) {
			status = http.StatusConflict
		}
		writeJsonWithStatusCode(w, status, commonResp{
			ErrNo:  status,
			ErrMsg: "创建播放地址失败: " + err.Error(),
		})
		return
	}

	writeJSON(w, map[string]interface{}{
		playbackResponseURLKey:     info.PlaybackPath(),
		"format":                   info.Format,
		playbackResponseQualityKey: info.Quality,
		"stream_name":              info.Name,
		"proxy":                    true,
	})
}

func (h *livePlaybackHandler) proxyFLV(w http.ResponseWriter, r *http.Request) {
	h.manager.ProxyFLV(w, r, mux.Vars(r)["token"])
}

func (h *livePlaybackHandler) proxyHLSPlaylist(w http.ResponseWriter, r *http.Request) {
	h.manager.ProxyHLSPlaylist(w, r, mux.Vars(r)["token"])
}

func (h *livePlaybackHandler) proxyHLSMedia(w http.ResponseWriter, r *http.Request) {
	h.manager.ProxyHLSMedia(w, r, mux.Vars(r)["token"])
}
