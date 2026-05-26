package twitch

import (
	"net/url"
	"testing"
)

func TestTwitch(t *testing.T) {
	u, err := url.Parse("https://www.twitch.tv/irissiri129")
	if err != nil {
		t.Fatal(err)
	}
	b := new(builder)
	l, err := b.Build(u)
	if err != nil {
		t.Fatal(err)
	}
	info, err := l.GetInfo()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("HostName: %s, RoomName: %s, Status: %v", info.HostName, info.RoomName, info.Status)
	urls, err := l.(*Live).GetStreamUrls()
	if err != nil {
		t.Fatal(err)
	}
	for _, val := range urls {
		t.Logf("Stream URL: %s", val.String())
	}
}
