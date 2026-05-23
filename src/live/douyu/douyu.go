package douyu

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/hr3lxphr6j/requests"

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

var (
	cryptoJS        []byte
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

func (l *Live) loadCryptoJS() {
	var (
		resp *requests.Response
		body []byte
		err  error
	)
	cdnUrls := [...]string{"https://cdnjs.cloudflare.com/ajax/libs/crypto-js/3.1.9-1/crypto-js.min.js",
		"https://cdn.jsdelivr.net/npm/crypto-js@3.1.9-1/crypto-js.min.js",
		"https://cdn.staticfile.org/crypto-js/3.1.9-1/crypto-js.min.js",
		"https://cdn.bootcdn.net/ajax/libs/crypto-js/3.1.9-1/crypto-js.min.js"}

	for _, url := range cdnUrls {
		resp, err = l.RequestSession.Get(url)
		if err != nil || resp.StatusCode != http.StatusOK {
			continue
		}
		body, err = resp.Bytes()
		if err != nil {
			continue
		}
		cryptoJS = body
		return
	}
	panic(fmt.Errorf("failed to load CryptoJS, please check network"))
}

func (l *Live) getEngineWithCryptoJS() (*otto.Otto, error) {
	if cryptoJS == nil {
		l.loadCryptoJS()
	}
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
	fmt.Printf("DEBUG: Douyu PlayInfo Response: %s\n", string(body))
	if errorInt := gjson.GetBytes(body, "error").Int(); errorInt != 0 {
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

	infos = make([]*live.StreamUrlInfo, 0)

	addUrlInfos := func(urls []*url.URL, cdnName, rateName, codec string) {
		for _, u := range urls {
			format := "flv"
			if strings.Contains(u.Path, ".m3u8") {
				format = "hls"
			}
			infos = append(infos, &live.StreamUrlInfo{
				Url:                  u,
				Name:                 fmt.Sprintf("%s - %s (%s)", cdnName, rateName, codec),
				Description:          fmt.Sprintf("%s 线路，画质为 %s，视频编码为 %s", cdnName, rateName, codec),
				Quality:              rateName,
				Format:               format,
				Codec:                codec,
				HeadersForDownloader: make(map[string]string),
				AttributesForStreamSelect: map[string]string{
					"线路":          cdnName,
					"画质":          rateName,
					"编码":          codec,
					"format_name": format,
					"协议":          format,
				},
			})
		}
	}

	defaultCdnName := "主线路"
	for _, c := range cdns {
		if c.Get("cdn").String() == defaultCdnCode {
			defaultCdnName = c.Get("name").String()
			break
		}
	}

	defaultCodec := getCodecFromPlayInfo(body)
	addUrlInfos(defaultUrls, defaultCdnName, "原画", defaultCodec)

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
				addUrlInfos(h264Urls, defaultCdnName, "原画", "h264")
			}
		}
	}

	// 遍历其他 CDN 线路（请求 vframe = "h265"）
	for _, c := range cdns {
		cdnCode := c.Get("cdn").String()
		cdnName := c.Get("name").String()
		if cdnCode == "" || cdnCode == defaultCdnCode {
			continue
		}
		urls, codec, err := l.fetchStreamForRateAndCDN(baseParams, 0, cdnCode, "h265")
		if err != nil {
			continue
		}
		addUrlInfos(urls, cdnName, "原画", codec)
	}

	// 遍历其他清晰度（请求 vframe = "h265"）
	for _, r := range rates {
		rateVal := int(r.Get("rate").Int())
		rateName := r.Get("name").String()
		if rateVal == 0 {
			continue
		}
		urls, codec, err := l.fetchStreamForRateAndCDN(baseParams, rateVal, "", "h265")
		if err != nil {
			continue
		}
		addUrlInfos(urls, defaultCdnName, rateName, codec)
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
