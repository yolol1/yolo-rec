package main

import (
	"context"
	"fmt"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/live"
	_ "github.com/bililive-go/bililive-go/src/live/douyu"
	"github.com/bluele/gcache"
)

func main() {
	for _, roomUrl := range []string{"https://www.douyu.com/9999", "https://www.douyu.com/10106809", "https://www.douyu.com/5551871"} {
		fmt.Printf("=== Testing room: %s ===\n", roomUrl)
		quality := "高清"
		room := &configs.LiveRoom{
			Url: roomUrl,
			OverridableConfig: configs.OverridableConfig{
				StreamPreference: &configs.StreamPreference{
					Quality: &quality,
				},
			},
		}
		cfg := configs.NewConfig()
		cfg.LiveRooms = append(cfg.LiveRooms, *room)
		configs.SetCurrentConfig(cfg)
		cache := gcache.New(10).Build()
		liveObj, err := live.New(context.Background(), room, cache)
		if err != nil {
			fmt.Printf("live.New failed: %v\n", err)
			continue
		}

		info, err := liveObj.GetInfo()
		if err != nil {
			fmt.Printf("GetInfo failed: %v\n", err)
		} else {
			fmt.Printf("Room Status: %v (Host: %s, Title: %s)\n", info.Status, info.HostName, info.RoomName)
		}

		infos, err := liveObj.GetStreamInfos()
		if err != nil {
			fmt.Printf("GetStreamInfos failed: %v\n", err)
			continue
		}

		fmt.Printf("Found %d stream options:\n", len(infos))
		for i, info := range infos {
			fmt.Printf("[%d] %s (Codec: %s, Format: %s)\n", i, info.Name, info.Codec, info.Format)
			fmt.Printf("    Attributes: %v\n", info.AttributesForStreamSelect)
			urlStr := "<placeholder>"
			if info.Url != nil {
				urlStr = info.Url.String()
			}
			fmt.Printf("    URL: %s\n", urlStr)
		}
	}
}
