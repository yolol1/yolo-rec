package twitch

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/hr3lxphr6j/requests"
	"github.com/tidwall/gjson"

	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/live/internal"
)

const (
	domain = "www.twitch.tv"
	cnName = "twitch"

	gqlUrl   = "https://gql.twitch.tv/gql"
	clientId = "kimne78kx3ncx6brgo4mv6wki5h1ko"
)

func init() {
	live.Register(domain, new(builder))
}

type builder struct{}

func (b *builder) Build(url *url.URL) (live.Live, error) {
	return &Live{
		BaseLive: internal.NewBaseLive(url),
	}, nil
}

type Live struct {
	internal.BaseLive
	login, hostName, roomName string
	isLive                    bool
}

func (l *Live) parseInfo() error {
	if l.login == "" {
		paths := strings.Split(l.Url.Path, "/")
		if len(paths) < 2 {
			return live.ErrRoomUrlIncorrect
		}
		l.login = paths[1]
	}

	payload := fmt.Sprintf(`{"query": "query($login: String!) { user(login: $login) { displayName stream { title } } }", "variables": {"login": "%s"}}`, l.login)
	resp, err := l.RequestSession.Post(gqlUrl, live.CommonUserAgent,
		requests.Header("Client-Id", clientId),
		requests.Header("Content-Type", "application/json"),
		requests.Body(strings.NewReader(payload)))
	if err != nil {
		return err
	}
	body, err := resp.Bytes()
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("gql request failed with status: %d", resp.StatusCode)
	}

	userJson := gjson.GetBytes(body, "data.user")
	if !userJson.Exists() || userJson.Type == gjson.Null {
		return live.ErrRoomNotExist
	}

	l.hostName = userJson.Get("displayName").String()
	streamJson := userJson.Get("stream")
	if streamJson.Exists() && streamJson.Type != gjson.Null {
		l.isLive = true
		l.roomName = streamJson.Get("title").String()
	} else {
		l.isLive = false
	}

	return nil
}

func (l *Live) GetInfo() (info *live.Info, err error) {
	if err := l.parseInfo(); err != nil {
		return nil, err
	}
	info = &live.Info{
		Live:     l,
		HostName: l.hostName,
		RoomName: l.roomName,
		Status:   l.isLive,
	}
	return info, nil
}

func (l *Live) GetStreamUrls() (us []*url.URL, err error) {
	if l.login == "" {
		if err := l.parseInfo(); err != nil {
			return nil, err
		}
	}

	payload := fmt.Sprintf(`{"query": "query($channelName: String!) { streamPlaybackAccessToken(channelName: $channelName, params: {platform: \"web\", playerBackend: \"mediaplayer\", playerType: \"embed\"}) { value signature } }", "variables": {"channelName": "%s"}}`, l.login)
	resp, err := l.RequestSession.Post(gqlUrl, live.CommonUserAgent,
		requests.Header("Client-Id", clientId),
		requests.Header("Content-Type", "application/json"),
		requests.Body(strings.NewReader(payload)))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gql token request failed with status: %d", resp.StatusCode)
	}
	body, err := resp.Bytes()
	if err != nil {
		return nil, err
	}

	tokenJson := gjson.GetBytes(body, "data.streamPlaybackAccessToken")
	if !tokenJson.Exists() || tokenJson.Type == gjson.Null {
		return nil, live.ErrRoomNotExist
	}

	token := tokenJson.Get("value").String()
	sig := tokenJson.Get("signature").String()

	u, err := url.Parse(fmt.Sprintf("https://usher.ttvnw.net/api/channel/hls/%s.m3u8", l.login))
	if err != nil {
		return nil, err
	}

	v := url.Values{}
	v.Add("allow_source", "true")
	v.Add("allow_audio_only", "true")
	v.Add("sig", sig)
	v.Add("token", token)
	v.Add("fast_bread", "true")
	u.RawQuery = v.Encode()

	return []*url.URL{u}, nil
}

func (l *Live) GetPlatformCNName() string {
	return cnName
}
