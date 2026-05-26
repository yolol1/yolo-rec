package main

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/bililive-go/bililive-go/src/live"
	_ "github.com/bililive-go/bililive-go/src/live/douyu"
)

func main() {
	u, _ := url.Parse("https://www.douyu.com/5551871")
	l, err := live.NewLive(u)
	if err != nil {
		fmt.Println("Error creating live:", err)
		return
	}

	start := time.Now()
	infos, err := l.GetStreamInfos()
	if err != nil {
		fmt.Println("Error GetStreamInfos:", err)
		return
	}
	fmt.Printf("GetStreamInfos took %v, found %d streams\n", time.Since(start), len(infos))
	for _, info := range infos {
		fmt.Printf(" - %s\n", info.Name)
	}
}
