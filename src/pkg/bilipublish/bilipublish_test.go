package bilipublish

import (
	"testing"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/stretchr/testify/assert"
)

func TestResolveCookiePriority(t *testing.T) {
	global := map[string]string{
		"www.bilibili.com":  "www-cookie",
		"bilibili.com":      "bilibili-cookie",
		"live.bilibili.com": "live-cookie",
	}

	// 显式配置优先
	cfg := configs.BiliPublish{Cookie: "explicit-cookie"}
	assert.Equal(t, "explicit-cookie", ResolveCookie(cfg, global))

	// 显式配置留空 → www.bilibili.com
	cfg = configs.BiliPublish{Cookie: "  "}
	assert.Equal(t, "www-cookie", ResolveCookie(cfg, global))

	// www 缺失 → bilibili.com
	global2 := map[string]string{
		"bilibili.com":      "bilibili-cookie",
		"live.bilibili.com": "live-cookie",
	}
	assert.Equal(t, "bilibili-cookie", ResolveCookie(configs.BiliPublish{}, global2))

	// 只剩 live.bilibili.com
	global3 := map[string]string{"live.bilibili.com": "live-cookie"}
	assert.Equal(t, "live-cookie", ResolveCookie(configs.BiliPublish{}, global3))

	// 全部缺失返回空
	assert.Equal(t, "", ResolveCookie(configs.BiliPublish{}, nil))
}

func TestExtractCSRF(t *testing.T) {
	assert.Equal(t, "abc123", extractCSRF("SESSDATA=xxx; bili_jct=abc123; DedeUserID=1"))
	assert.Equal(t, "", extractCSRF("SESSDATA=xxx"))
	assert.Equal(t, "", extractCSRF(""))
}

func TestNormalizeEndpoint(t *testing.T) {
	assert.Equal(t, "https://upos-sz-mirrorcos.bilivideo.com",
		normalizeEndpoint("upos-sz-mirrorcos.bilivideo.com"))
	assert.Equal(t, "https://upos-sz-mirrorcos.bilivideo.com",
		normalizeEndpoint("https://upos-sz-mirrorcos.bilivideo.com"))
	assert.Equal(t, "https://upos-sz-mirrorcos.bilivideo.com",
		normalizeEndpoint("upos-sz-mirrorcos.bilivideo.com/"))
	assert.Equal(t, "", normalizeEndpoint(""))
}
