package twitch

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/hr3lxphr6j/requests"
	"github.com/tidwall/gjson"

	"github.com/bililive-go/bililive-go/src/configs"
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

	var candidates []string
	cfg := configs.GetCurrentConfig()
	if cfg != nil && cfg.Feature.TwitchDisableAdsProxy != "" {
		candidates = append(candidates, strings.ReplaceAll(cfg.Feature.TwitchDisableAdsProxy, "{{ .Login }}", l.login))
	} else {
		// 如果用户未配置，使用默认社区去广告节点
		// api.ttv.lol is currently dead/blocked, prioritize luminous.dev
		candidates = append(candidates,
			fmt.Sprintf("https://eu.luminous.dev/live/%s", l.login),
			fmt.Sprintf("https://api.ttv.lol/playlist/%s.m3u8", l.login),
			fmt.Sprintf("https://lb-eu.ttv.lol/playlist/%s.m3u8", l.login),
		)
	}

	// 最后使用官方地址作为兜底，防止全部代理失效
	candidates = append(candidates, fmt.Sprintf("https://usher.ttvnw.net/api/channel/hls/%s.m3u8", l.login))

	var validM3u8Url *url.URL
	client := &http.Client{Timeout: 10 * time.Second}

	for _, curl := range candidates {
		reqUrl := curl
		if strings.Contains(curl, "usher.ttvnw.net") {
			u, _ := url.Parse(curl)
			v := url.Values{}
			v.Add("allow_source", "true")
			v.Add("allow_audio_only", "true")
			v.Add("sig", sig)
			v.Add("token", token)
			v.Add("fast_bread", "true")
			u.RawQuery = v.Encode()
			reqUrl = u.String()
		}

		req, err := http.NewRequest("GET", reqUrl, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

		respCheck, err := client.Do(req)
		if err == nil {
			if respCheck.StatusCode == http.StatusOK {
				// 检查是否真的是 m3u8（防止代理返回 Cloudflare HTML 拦截页面）
				buf := make([]byte, 7)
				io.ReadFull(respCheck.Body, buf)
				if string(buf) == "#EXTM3U" {
					validM3u8Url, _ = url.Parse(reqUrl)
					respCheck.Body.Close()
					break // 找到可用的节点，直接跳出
				}
			}
			respCheck.Body.Close()
		}
	}

	if validM3u8Url == nil {
		return nil, fmt.Errorf("所有 Twitch 代理和官方节点均不可用")
	}

	return []*url.URL{validM3u8Url}, nil
}

func (l *Live) GetPlatformCNName() string {
	return cnName
}
