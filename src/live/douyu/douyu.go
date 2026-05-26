package douyu

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/hr3lxphr6j/requests"

	"github.com/bililive-go/bililive-go/src/configs"
	"github.com/bililive-go/bililive-go/src/live"
	"github.com/bililive-go/bililive-go/src/live/internal"
	"github.com/bililive-go/bililive-go/src/pkg/utils"

	"github.com/robertkrimen/otto"
	uuid "github.com/satori/go.uuid"
	"github.com/tidwall/gjson"
)

/*
From https://github.com/zhangn1985/ykdl

Thanks
*/
const (
	domain = "www.douyu.com"
	cnName = "斗鱼"

	liveInfoUrl = "https://www.douyu.com/betard"
	liveEncUrl  = "https://www.douyu.com/swf_api/homeH5Enc"
	liveAPIUrl  = "https://www.douyu.com/lapi/live/getH5Play"
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

//go:embed crypto-js.min.js
var cryptoJS string

var (
	douyuRoomIDRegs = []string{
		`\$ROOM\.room_id\s*=\s*(\d+)`,
		`room_id\s*=\s*(\d+)`,
		`"room_id.?":(\d+)`,
		`data-onlineid=(\d+)`,
		`"aliase","(\d+)","d"`,
		`\"aliase\",\"(\d+)\",\"[a-z]\"`,
	}
	workflowReg = `function ub98484234\(.+?\Weval\((\w+)\);`
	jsDomTmpl   = template.Must(template.New("jsDom").Parse(`
		{{.DebugMessages}} = { {{.DecryptedCodes}}: []};
		if (!this.window) {window = {};}
		if (!this.document) {document = {};}
	`))
	jsPatchTmpl = template.Must(template.New("jsPatch").Parse(`
		{{.DebugMessages}}.{{.DecryptedCodes}}.push({{.Workflow}});
		var patchCode = function(workflow) {
			var testVari = /(\w+)=(\w+)\([\w\+]+\);.*?(\w+)="\w+";/.exec(workflow);
			if (testVari && testVari[1] == testVari[2]) {
				{{.Workflow}} += testVari[1] + "[" + testVari[3] + "] = function() {return true;};";
			}
		};
		patchCode({{.Workflow}});
		var subWorkflow = /(?:\w+=)?eval\((\w+)\)/.exec({{.Workflow}});
		if (subWorkflow) {
			var subPatch = (
				"{{.DebugMessages}}.{{.DecryptedCodes}}.push('sub workflow: ' + subWorkflow);" +
				"patchCode(subWorkflow);"
			).replace(/subWorkflow/g, subWorkflow[1]) + subWorkflow[0];
			{{.Workflow}} = {{.Workflow}}.replace(subWorkflow[0], subPatch);
		}
		eval({{.Workflow}});
	`))
	jsDebugTmpl = template.Must(template.New("jsDebug").Parse(`
		var {{.Ub98484234}} = ub98484234;
		ub98484234 = function(p1, p2, p3) {
			try {
				var resoult = {{.Ub98484234}}(p1, p2, p3);
				{{.DebugMessages}}.{{.Resoult}} = resoult;
			} catch(e) {
				{{.DebugMessages}}.{{.Resoult}} = e.message;
			}
			return {{.DebugMessages}};
		};
	`))
)

func render(tmpl *template.Template, data any) (string, error) {
	buf := bytes.NewBuffer(nil)
	if err := tmpl.Execute(buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func (l *Live) getEngineWithCryptoJS() (*otto.Otto, error) {
	engine := otto.New()
	if _, err := engine.Eval(cryptoJS); err != nil {
		return nil, err
	}
	return engine, nil
}

type Live struct {
	internal.BaseLive
	roomID string
}

func (l *Live) fetchRoomID() error {
	if l.roomID != "" {
		return nil
	}
	path := strings.Trim(l.Url.Path, "/")
	if _, err := strconv.Atoi(path); err == nil && path != "" {
		l.roomID = path
		return nil
	}
	var body []byte
	resp, err := l.RequestSession.Get(l.Url.String(), live.CommonUserAgent)
	if err != nil {
		return errors.New("request failed. error: " + err.Error())
	}
	if resp.StatusCode != http.StatusOK {
		return errors.New("response code is " + strconv.Itoa(resp.StatusCode))
	}
	body, err = resp.Bytes()
	if err != nil {
		return errors.New("failed to read response body. error: " + err.Error())
	}
	for _, reg := range douyuRoomIDRegs {
		if str := utils.Match1(reg, string(body)); str != "" {
			l.roomID = str
			return nil
		}
	}
	if strings.Contains(string(body), "该房间目前没有开放") {
		errorMessage := "房间未开放"
		return errors.New(errorMessage)
	}
	if strings.Contains(string(body), "您观看的房间已被关闭，请选择其他直播进行观看哦！") {
		errorMessage := "房间被关闭"
		return errors.New(errorMessage)
	}
	showedBodyMaxLength := 20
	bodyLen := len(body)
	if bodyLen < 20 {
		showedBodyMaxLength = bodyLen
	}
	errorMessage := "unexcepted error. body: " + string(body[:showedBodyMaxLength])
	if bodyLen > showedBodyMaxLength {
		errorMessage += "... "
	}
	return errors.New(errorMessage)
}

func (l *Live) GetInfo() (info *live.Info, err error) {
	if err := l.fetchRoomID(); err != nil {
		if err.Error() == "房间未开放" {
			return nil, errors.New("room not exists, fetchRoomID failed")
		} else if err.Error() == "房间被关闭" {
			return &live.Info{
				Live:     l,
				HostName: "您观看的房间已被关闭",
				RoomName: "您观看的房间已被关闭",
				Status:   false,
			}, nil
		} else {
			return nil, err
		}

	}
	resp, err := l.RequestSession.Get(fmt.Sprintf("%s/%s", liveInfoUrl, l.roomID), live.CommonUserAgent)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GetInfo() failed, response code: %d", resp.StatusCode)
	}
	body, err := resp.Bytes()
	if err != nil {
		return nil, err
	}
	info = &live.Info{
		Live:         l,
		HostName:     gjson.GetBytes(body, "room.owner_name").String(),
		RoomName:     gjson.GetBytes(body, "room.room_name").String(),
		Status:       gjson.GetBytes(body, "room.show_status").Int() == 1 && gjson.GetBytes(body, "room.videoLoop").Int() == 0,
		CustomLiveId: "douyu/" + l.roomID,
	}
	return info, nil
}

func (l *Live) getSignParams() (map[string]string, error) {
	resp, err := l.RequestSession.Get(liveEncUrl, live.CommonUserAgent, requests.Query("rids", l.roomID))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("getSignParams() failed, response code: %d", resp.StatusCode)
	}
	body, err := resp.Bytes()
	if err != nil {
		return nil, err
	}

	jsEnc := gjson.GetBytes(body, "data.room"+l.roomID).String()

	workflow := utils.Match1(workflowReg, jsEnc)

	context := struct {
		DebugMessages  string
		DecryptedCodes string
		Resoult        string
		Ub98484234     string
		Workflow       string
	}{
		DebugMessages:  utils.GenRandomName(8),
		DecryptedCodes: utils.GenRandomName(8),
		Resoult:        utils.GenRandomName(8),
		Ub98484234:     utils.GenRandomName(8),
		Workflow:       workflow,
	}
	jsDom, err := render(jsDomTmpl, context)
	if err != nil {
		return nil, err
	}
	jsPatch, err := render(jsPatchTmpl, context)
	if err != nil {
		return nil, err
	}
	jsDebug, err := render(jsDebugTmpl, context)
	if err != nil {
		return nil, err
	}

	jsEnc = strings.ReplaceAll(jsEnc, fmt.Sprintf("eval(%s);", context.Workflow), jsPatch)
	engine, err := l.getEngineWithCryptoJS()
	if err != nil {
		return nil, err
	}
	if _, err := engine.Eval(jsDom); err != nil {
		return nil, err
	}
	if _, err := engine.Eval(jsEnc); err != nil {
		return nil, err
	}
	if _, err := engine.Eval(jsDebug); err != nil {
		return nil, err
	}
	did := strings.ReplaceAll(uuid.Must(uuid.NewV4()).String(), "-", "")
	ts := time.Now()
	res, err := engine.Call("ub98484234", nil, l.roomID, did, ts.Unix())
	if err != nil {
		return nil, err
	}
	values := map[string]string{
		"cdn":  "",
		"iar":  "0",
		"ive":  "0",
		"rate": "0",
	}
	resoult, err := res.Object().Get(context.Resoult)
	if err != nil {
		return nil, err
	}
	for _, entry := range strings.Split(resoult.String(), "&") {
		if entry == "" {
			continue
		}
		strs := strings.SplitN(entry, "=", 2)
		values[strs[0]] = strs[1]
	}
	return values, nil
}

func (l *Live) DebugGetSignParams() (map[string]string, error) {
	return l.getSignParams()
}

func (l *Live) DebugFetchPlayInfo(params map[string]string) ([]byte, error) {
	return l.fetchPlayInfo(params)
}

func (l *Live) fetchPlayInfo(params map[string]string) ([]byte, error) {
	resp, err := l.RequestSession.Post(
		fmt.Sprintf("%s/%s", liveAPIUrl, l.roomID),
		requests.Form(params),
		requests.Header("origin", "https://www.douyu.com"),
		requests.Referer(l.GetRawUrl()),
		live.CommonUserAgent,
	)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, live.ErrInternalError
	}
	return resp.Bytes()
}

func getCodecFromPlayInfo(body []byte) string {
	cdnCode := gjson.GetBytes(body, "data.cdn").String()
	if cdnCode == "" {
		cdnCode = gjson.GetBytes(body, "data.rtmp_cdn").String()
	}
	cdnsResult := gjson.GetBytes(body, "data.cdnsWithName")
	if !cdnsResult.Exists() || len(cdnsResult.Array()) == 0 {
		cdnsResult = gjson.GetBytes(body, "data.cdns")
	}
	for _, c := range cdnsResult.Array() {
		if c.Get("cdn").String() == cdnCode {
			if c.Get("isH265").Bool() {
				return "hevc"
			}
			return "h264"
		}
	}
	return "h264"
}

func (l *Live) fetchStreamForRateAndCDN(baseParams map[string]string, rate int, cdn string, vframe string) ([]*url.URL, string, error) {
	params := make(map[string]string, len(baseParams))
	for k, v := range baseParams {
		params[k] = v
	}
	params["rate"] = strconv.Itoa(rate)
	params["cdn"] = cdn
	if vframe != "" {
		params["vframe"] = vframe
	} else {
		delete(params, "vframe")
	}

	body, err := l.fetchPlayInfo(params)
	if err != nil {
		return nil, "", err
	}
	if errorInt := gjson.GetBytes(body, "error").Int(); errorInt != 0 {
		return nil, "", fmt.Errorf("error: %d", errorInt)
	}

	rtmpUrl := gjson.GetBytes(body, "data.rtmp_url").String()
	rtmpLive := gjson.GetBytes(body, "data.rtmp_live").String()
	rawUrlStr := fmt.Sprintf("%s/%s", rtmpUrl, rtmpLive)
	urls, err := utils.GenUrls(rawUrlStr)
	if err != nil {
		return nil, "", err
	}
	codec := getCodecFromPlayInfo(body)
	return urls, codec, nil
}

func (l *Live) GetStreamInfos() (infos []*live.StreamUrlInfo, err error) {
	if err := l.fetchRoomID(); err != nil {
		return nil, err
	}
	baseParams, err := l.getSignParams()
	if err != nil {
		return nil, err
	}

	// 第一次主请求：使用 vframe="h265" 以尝试获取 HEVC 编码的流
	hevcParams := make(map[string]string, len(baseParams))
	for k, v := range baseParams {
		hevcParams[k] = v
	}
	hevcParams["vframe"] = "h265"

	body, err := l.fetchPlayInfo(hevcParams)
	if err != nil {
		return nil, err
	}
	if errorInt := gjson.GetBytes(body, "error").Int(); errorInt != 0 {
		if errorInt == -5 {
			return nil, live.ErrLiveOffline
		}
		return nil, fmt.Errorf("GetStreamInfos() failed, error: %d", errorInt)
	}

	defaultRtmpUrl := gjson.GetBytes(body, "data.rtmp_url").String()
	defaultRtmpLive := gjson.GetBytes(body, "data.rtmp_live").String()
	defaultRawUrlStr := fmt.Sprintf("%s/%s", defaultRtmpUrl, defaultRtmpLive)
	defaultUrls, err := utils.GenUrls(defaultRawUrlStr)
	if err != nil {
		return nil, err
	}

	// 适配 cdnsWithName，如果不存在则退回到 cdns
	cdnsResult := gjson.GetBytes(body, "data.cdnsWithName")
	if !cdnsResult.Exists() || len(cdnsResult.Array()) == 0 {
		cdnsResult = gjson.GetBytes(body, "data.cdns")
	}
	cdns := cdnsResult.Array()

	rates := gjson.GetBytes(body, "data.multirates").Array()

	// 适配 rtmp_cdn，如果不存在则退回到 cdn
	defaultCdnCode := gjson.GetBytes(body, "data.cdn").String()
	if defaultCdnCode == "" {
		defaultCdnCode = gjson.GetBytes(body, "data.rtmp_cdn").String()
	}

	defaultCdnName := "主线路"
	for _, c := range cdns {
		if c.Get("cdn").String() == defaultCdnCode {
			defaultCdnName = c.Get("name").String()
			break
		}
	}

	defaultCodec := getCodecFromPlayInfo(body)

	type douyuOption struct {
		cdnCode   string
		cdnName   string
		rateVal   int
		rateName  string
		codec     string
		isDefault bool
		urls      []*url.URL
	}

	options := []*douyuOption{
		{
			cdnCode:   defaultCdnCode,
			cdnName:   defaultCdnName,
			rateVal:   0,
			rateName:  "原画",
			codec:     defaultCodec,
			isDefault: true,
			urls:      defaultUrls,
		},
	}

	// 如果 defaultCodec 是 hevc 编码，说明当前请求到了 HEVC，为了兼容性（比如有些播放器只支持 H264）我们再发一次 h264 请求作为备选流
	if defaultCodec == "hevc" {
		h264Params := make(map[string]string, len(baseParams))
		for k, v := range baseParams {
			h264Params[k] = v
		}
		h264Params["vframe"] = "h264"
		h264Body, err := l.fetchPlayInfo(h264Params)
		if err == nil && gjson.GetBytes(h264Body, "error").Int() == 0 {
			h264Url := gjson.GetBytes(h264Body, "data.rtmp_url").String()
			h264Live := gjson.GetBytes(h264Body, "data.rtmp_live").String()
			h264RawUrlStr := fmt.Sprintf("%s/%s", h264Url, h264Live)
			if h264Urls, err := utils.GenUrls(h264RawUrlStr); err == nil {
				options = append(options, &douyuOption{
					cdnCode:   defaultCdnCode,
					cdnName:   defaultCdnName,
					rateVal:   0,
					rateName:  "原画",
					codec:     "h264",
					isDefault: true,
					urls:      h264Urls,
				})
			}
		}
	}

	// 获取可选的所有 CDN 和清晰度组合
	for _, c := range cdns {
		cdnCode := c.Get("cdn").String()
		cdnName := c.Get("name").String()
		isH265Supported := c.Get("isH265").Bool()

		for _, r := range rates {
			rateVal := int(r.Get("rate").Int())
			rateName := r.Get("name").String()
			if rateVal == 0 {
				rateName = "原画"
			}

			// 支持的编码格式
			codecs := []string{"h264"}
			if isH265Supported || defaultCodec == "hevc" {
				codecs = append(codecs, "hevc")
			}

			for _, codec := range codecs {
				// 避免重复添加默认流
				isAlreadyAdded := false
				for _, opt := range options {
					if opt.cdnCode == cdnCode && opt.rateVal == rateVal && opt.codec == codec {
						isAlreadyAdded = true
						break
					}
				}
				if isAlreadyAdded {
					continue
				}

				options = append(options, &douyuOption{
					cdnCode:  cdnCode,
					cdnName:  cdnName,
					rateVal:  rateVal,
					rateName: rateName,
					codec:    codec,
				})
			}
		}
	}

	// 读取用户当前的流偏好配置
	var preferredQuality string
	var preferredAttrs map[string]string
	if config := configs.GetCurrentConfig(); config != nil {
		if roomConfig, err := config.GetLiveRoomByUrl(l.GetRawUrl()); err == nil && roomConfig.StreamPreference != nil {
			if roomConfig.StreamPreference.Quality != nil {
				preferredQuality = *roomConfig.StreamPreference.Quality
			}
			if roomConfig.StreamPreference.Attributes != nil {
				preferredAttrs = *roomConfig.StreamPreference.Attributes
			}
		}
	}

	// 如果有流偏好，找出与该偏好最匹配的选项，并在此处单独加载它的真实 URL
	var bestOpt *douyuOption
	bestScore := -1

	if preferredQuality != "" || len(preferredAttrs) > 0 {
		for _, opt := range options {
			score := 0
			if preferredQuality != "" && opt.rateName == preferredQuality {
				score += 100
			}
			for k, v := range preferredAttrs {
				if k == "线路" && opt.cdnName == v {
					score += 1
				} else if k == "画质" && opt.rateName == v {
					score += 1
				} else if k == "编码" && opt.codec == v {
					score += 1
				}
			}
			if score > bestScore {
				bestScore = score
				bestOpt = opt
			}
		}
	}

	// 如果匹配出的最佳选项尚未加载真实 URL，则通过网络请求获取
	if bestOpt != nil && len(bestOpt.urls) == 0 {
		vframe := "h265"
		if bestOpt.codec == "h264" {
			vframe = "h264"
		}
		urls, codec, err := l.fetchStreamForRateAndCDN(baseParams, bestOpt.rateVal, bestOpt.cdnCode, vframe)
		if err == nil && len(urls) > 0 {
			bestOpt.urls = urls
			bestOpt.codec = codec
		} else {
			l.GetLogger().Warnf("无法获取偏好流的真实 URL (quality=%s, cdn=%s, codec=%s): %v", bestOpt.rateName, bestOpt.cdnName, bestOpt.codec, err)
		}
	}

	// 构建最终的 StreamUrlInfo 列表
	infos = make([]*live.StreamUrlInfo, 0, len(options))
	for _, opt := range options {
		format := "flv"

		if len(opt.urls) > 0 {
			// 有真实 URL 的流
			for _, u := range opt.urls {
				if strings.Contains(u.Path, ".m3u8") {
					format = "hls"
				}
				infos = append(infos, &live.StreamUrlInfo{
					Url:                  u,
					Name:                 fmt.Sprintf("%s - %s (%s)", opt.cdnName, opt.rateName, opt.codec),
					Description:          fmt.Sprintf("%s 线路，画质为 %s，视频编码为 %s", opt.cdnName, opt.rateName, opt.codec),
					Quality:              opt.rateName,
					Format:               format,
					Codec:                opt.codec,
					HeadersForDownloader: make(map[string]string),
					AttributesForStreamSelect: map[string]string{
						"线路":          opt.cdnName,
						"画质":          opt.rateName,
						"编码":          opt.codec,
						"format_name": format,
						"协议":          format,
					},
					IsPlaceHolder: false,
				})
			}
		} else {
			// 占位符流 (IsPlaceHolder = true, Url = nil)
			infos = append(infos, &live.StreamUrlInfo{
				Url:                  nil,
				Name:                 fmt.Sprintf("%s - %s (%s) [未加载]", opt.cdnName, opt.rateName, opt.codec),
				Description:          fmt.Sprintf("%s 线路，画质为 %s，视频编码为 %s (选择后将自动加载)", opt.cdnName, opt.rateName, opt.codec),
				Quality:              opt.rateName,
				Format:               format,
				Codec:                opt.codec,
				HeadersForDownloader: make(map[string]string),
				AttributesForStreamSelect: map[string]string{
					"线路":          opt.cdnName,
					"画质":          opt.rateName,
					"编码":          opt.codec,
					"format_name": format,
					"协议":          format,
				},
				IsPlaceHolder: true,
			})
		}
	}

	return infos, nil
}

func (l *Live) GetStreamUrls() (us []*url.URL, err error) {
	infos, err := l.GetStreamInfos()
	if err != nil {
		return nil, err
	}
	us = make([]*url.URL, 0, len(infos))
	for _, info := range infos {
		if info.Url != nil {
			us = append(us, info.Url)
		}
	}
	return us, nil
}

func (l *Live) GetPlatformCNName() string {
	return cnName
}
