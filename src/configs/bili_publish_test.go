package configs

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLiveRoomIsBiliPublish(t *testing.T) {
	room := &LiveRoom{}

	// nil 表示默认不投稿（安全默认）
	assert.False(t, room.IsBiliPublish())

	room.BiliPublish = BoolPtr(false)
	assert.False(t, room.IsBiliPublish())

	room.BiliPublish = BoolPtr(true)
	assert.True(t, room.IsBiliPublish())
}

func TestLiveRoomIsCloudUploadEnabled(t *testing.T) {
	room := &LiveRoom{}

	// nil 跟随全局
	assert.True(t, room.IsCloudUploadEnabled(true))
	assert.False(t, room.IsCloudUploadEnabled(false))

	// true 强制开启（全局关闭时也生效）
	room.CloudUpload = BoolPtr(true)
	assert.True(t, room.IsCloudUploadEnabled(true))
	assert.True(t, room.IsCloudUploadEnabled(false))

	// false 强制关闭（全局开启时也不生效）
	room.CloudUpload = BoolPtr(false)
	assert.False(t, room.IsCloudUploadEnabled(true))
	assert.False(t, room.IsCloudUploadEnabled(false))
}
