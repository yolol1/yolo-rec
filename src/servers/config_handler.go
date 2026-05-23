package servers

import (
	"encoding/json"
	"io"
	"net/http"

	"gopkg.in/yaml.v3"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/instance"
	applog "github.com/bililive-go/bililive-go/src/log"
)

func getRawConfig(writer http.ResponseWriter, r *http.Request) {
	b, err := yaml.Marshal(configs.GetCurrentConfig())
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: err.Error(),
		})
		return
	}
	writeJSON(writer, map[string]string{
		"config": string(b),
	})
}

func putRawConfig(writer http.ResponseWriter, r *http.Request) {
	inst := instance.GetInstance(r.Context())
	b, err := io.ReadAll(r.Body)
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: err.Error(),
		})
		return
	}
	ctx := inst.Ctx
	var jsonBody map[string]any
	if err := json.Unmarshal(b, &jsonBody); err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: "invalid json: " + err.Error(),
		})
		return
	}

	cfgVal, ok := jsonBody["config"].(string)
	if !ok {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{
			ErrNo:  http.StatusBadRequest,
			ErrMsg: "missing or invalid string field: config",
		})
		return
	}

	newConfig, err := configs.NewConfigWithBytes([]byte(cfgVal))
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: err.Error(),
		})
		return
	}
	oldConfig := configs.GetCurrentConfig()
	oldConfig.RefreshLiveRoomIndexCache()
	// 继承原配置的文件路径
	newConfig.File = oldConfig.File
	// 预先将旧配置中的 LiveId 迁移到新配置（相同 URL）
	oldMap := make(map[string]configs.LiveRoom, len(oldConfig.LiveRooms))
	for _, room := range oldConfig.LiveRooms {
		oldMap[room.Url] = room
	}
	for i := range newConfig.LiveRooms {
		if rOld, ok := oldMap[newConfig.LiveRooms[i].Url]; ok {
			newConfig.LiveRooms[i].LiveId = rOld.LiveId
		}
	}
	// 先设置为当前全局配置，再驱动运行态差异变更
	configs.SetCurrentConfig(newConfig)
	if err := applyLiveRoomsByConfig(ctx, oldConfig, newConfig); err != nil {
		writeJSON(writer, map[string]any{
			"error": err.Error(),
		})
		return
	}
	if err := newConfig.Marshal(); err != nil {
		applog.GetLogger().Error("failed to save config: " + err.Error())
	}
	writeJSON(writer, commonResp{
		Data: "OK",
	})
}
