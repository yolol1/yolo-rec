package configs

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"gopkg.in/yaml.v3"
)

func TestNormalizeLiveRoomURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "抖音直播长链接去除冗余跟踪参数",
			in:   "https://live.douyin.com/859554777294?enter_from_merge=personal_homepage&enter_method=live_history_enter&room_id=7670023600765733670&action_type=click&group_id=undefined",
			want: "https://live.douyin.com/859554777294",
		},
		{
			name: "抖音短链去除跟踪参数",
			in:   "https://v.douyin.com/abc/?from=share",
			want: "https://v.douyin.com/abc/",
		},
		{
			name: "抖音无参数链接保持不变",
			in:   "https://live.douyin.com/859554777294",
			want: "https://live.douyin.com/859554777294",
		},
		{
			name: "抖音根路径不处理",
			in:   "https://live.douyin.com/?x=1",
			want: "https://live.douyin.com/?x=1",
		},
		{
			name: "其他平台保留查询参数（红逗依赖 roomId）",
			in:   "https://live.hongdoulive.com/LiveRoom/getRoomInfo?roomId=123",
			want: "https://live.hongdoulive.com/LiveRoom/getRoomInfo?roomId=123",
		},
		{
			name: "B站链接暂不清理",
			in:   "https://live.bilibili.com/123?spm_id_from=333",
			want: "https://live.bilibili.com/123?spm_id_from=333",
		},
		{
			name: "非法URL原样返回",
			in:   "http://[::1",
			want: "http://[::1",
		},
		{
			name: "缺少协议的地址原样返回",
			in:   "live.douyin.com/123?a=1",
			want: "live.douyin.com/123?a=1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, NormalizeLiveRoomURL(tt.in))
		})
	}
}

func TestLiveRoomUnmarshalYAMLNormalizesURL(t *testing.T) {
	// map 形式
	var roomMap LiveRoom
	err := yaml.Unmarshal([]byte("url: https://live.douyin.com/123?a=1"), &roomMap)
	assert.NoError(t, err)
	assert.Equal(t, "https://live.douyin.com/123", roomMap.Url)

	// 字符串形式
	var roomStr LiveRoom
	err = yaml.Unmarshal([]byte("https://live.douyin.com/123?a=1"), &roomStr)
	assert.NoError(t, err)
	assert.Equal(t, "https://live.douyin.com/123", roomStr.Url)

	// 非白名单平台不受影响
	var roomOther LiveRoom
	err = yaml.Unmarshal([]byte("url: https://live.hongdoulive.com/LiveRoom/getRoomInfo?roomId=123"), &roomOther)
	assert.NoError(t, err)
	assert.Equal(t, "https://live.hongdoulive.com/LiveRoom/getRoomInfo?roomId=123", roomOther.Url)
}

func TestNewLiveRoomsWithStringsNormalizesURL(t *testing.T) {
	rooms := NewLiveRoomsWithStrings([]string{
		"https://live.douyin.com/123?enter_from=page",
		"https://live.bilibili.com/456?spm_id_from=333",
	})
	assert.Len(t, rooms, 2)
	assert.Equal(t, "https://live.douyin.com/123", rooms[0].Url)
	assert.True(t, rooms[0].IsListening)
	assert.Equal(t, "https://live.bilibili.com/456?spm_id_from=333", rooms[1].Url)
}
