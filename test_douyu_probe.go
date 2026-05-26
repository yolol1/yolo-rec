package main

import (
	"context"
	"fmt"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/live"
	_ "github.com/bililive-go/bililive-go/src/live/douyu"
	_ "github.com/bililive-go/bililive-go/src/live/system"
	"github.com/bluele/gcache"
)

type debugLive interface {
	DebugGetSignParams() (map[string]string, error)
	DebugFetchPlayInfo(map[string]string) ([]byte, error)
}

func testRoom(roomUrl string) {
	fmt.Printf("=== Testing room: %s ===\n", roomUrl)
	room := &configs.LiveRoom{
		Url: roomUrl,
	}
	cache := gcache.New(10).Build()
	liveObj, err := live.New(context.Background(), room, cache)
	if err != nil {
		fmt.Printf("live.New failed: %v\n", err)
		return
	}

	var douyuLive debugLive
	
	// Unwrap live.WrappedLive
	for {
		if wl, ok := liveObj.(*live.WrappedLive); ok {
			liveObj = wl.Live
		} else {
			break
		}
	}

	if dl, ok := liveObj.(debugLive); ok {
		douyuLive = dl
	} else {
		fmt.Printf("Failed to cast liveObj (%T) to debugLive\n", liveObj)
		return
	}

	baseParams, err := douyuLive.DebugGetSignParams()
	if err != nil {
		fmt.Printf("GetSignParams failed: %v\n", err)
		return
	}

	// 打印一下基础参数
	fmt.Println("Base params:")
	for k, v := range baseParams {
		fmt.Printf("  %s: %s\n", k, v)
	}

	// 1. 测试不同的 pt (protocol) 参数
	pts := []string{"", "1", "2", "3", "4"}
	for _, pt := range pts {
		params := make(map[string]string)
		for k, v := range baseParams {
			params[k] = v
		}
		if pt != "" {
			params["pt"] = pt
		}
		// 我们默认用 vframe = h265 来测试
		params["vframe"] = "h265"

		body, err := douyuLive.DebugFetchPlayInfo(params)
		if err != nil {
			fmt.Printf("[pt=%s] FetchPlayInfo failed: %v\n", pt, err)
			continue
		}

		fmt.Printf("[pt=%s] Response: %s\n", pt, getCleanResponse(body))
	}

	// 2. 测试不同的 vframe 参数
	vframes := []string{"h264", "h265"}
	for _, vf := range vframes {
		params := make(map[string]string)
		for k, v := range baseParams {
			params[k] = v
		}
		params["vframe"] = vf
		params["pt"] = "3"

		body, err := douyuLive.DebugFetchPlayInfo(params)
		if err != nil {
			fmt.Printf("[vframe=%s, pt=3] FetchPlayInfo failed: %v\n", vf, err)
			continue
		}

		fmt.Printf("[vframe=%s, pt=3] Response: %s\n", vf, getCleanResponse(body))
	}

	// 3. 测试不同的 client 参数
	clients := []string{"", "pc", "wp", "h5", "android", "ios", "weixin"}
	for _, cl := range clients {
		params := make(map[string]string)
		for k, v := range baseParams {
			params[k] = v
		}
		if cl != "" {
			params["client"] = cl
		}
		params["vframe"] = "h265"

		body, err := douyuLive.DebugFetchPlayInfo(params)
		if err != nil {
			fmt.Printf("[client=%s] FetchPlayInfo failed: %v\n", cl, err)
			continue
		}

		fmt.Printf("[client=%s] Response: %s\n", cl, getCleanResponse(body))
	}
}

func getCleanResponse(body []byte) string {
	s := string(body)
	return s
}

func main() {
	// 用户测试的房间
	testRoom("https://www.douyu.com/10106809")
	
	// 常见的热门房间号
	popularRooms := []string{
		"https://www.douyu.com/288016", // LPL赛事
		"https://www.douyu.com/100",    // 斗鱼官方
		"https://www.douyu.com/9999",   // 热门主播
		"https://www.douyu.com/74751",  // 热门主播
		"https://www.douyu.com/30200",  // 热门主播
	}
	for _, r := range popularRooms {
		testRoom(r)
	}
}
