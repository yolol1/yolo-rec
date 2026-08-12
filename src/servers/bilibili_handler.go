package servers

import (
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/hr3lxphr6j/requests"

	applog "github.com/bililive-go/bililive-go/src/log"
)

// getBilibiliQRCode 获取哔哩哔哩登录二维码
func getBilibiliQRCode(writer http.ResponseWriter, r *http.Request) {
	resp, err := requests.Get("https://passport.bilibili.com/x/passport-login/web/qrcode/generate")
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "获取二维码失败: " + err.Error(),
		})
		return
	}
	body, err := resp.Bytes()
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "读取响应体失败: " + err.Error(),
		})
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.Write(body)
}

// pollBilibiliQRCode 轮询哔哩哔哩登录状态
func pollBilibiliQRCode(writer http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	if key == "" {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{ErrNo: http.StatusBadRequest, ErrMsg: "缺少 key 参数"})
		return
	}

	resp, err := requests.Get("https://passport.bilibili.com/x/passport-login/web/qrcode/poll", requests.Query("qrcode_key", key))
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "轮询登录状态失败: " + err.Error(),
		})
		return
	}

	body, err := resp.Bytes()
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "读取响应体失败: " + err.Error(),
		})
		return
	}

	var result struct {
		Code int `json:"code"`
		Data struct {
			Code int    `json:"code"`
			Url  string `json:"url"`
		} `json:"data"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		applog.GetLogger().Error("轮询登录状态解析 JSON 失败: " + err.Error())
	} else if result.Code == 0 && result.Data.Code == 0 && result.Data.Url != "" {
		u, err := url.Parse(result.Data.Url)
		if err != nil {
			applog.GetLogger().Error("解析登录回调 URL 失败: " + err.Error() + ", URL: " + result.Data.Url)
		} else {
			q := u.Query()
			foundExtra := false
			if resp.Response != nil {
				for _, cookie := range resp.Cookies() {
					if cookie.Name == "sid" && q.Get("sid") == "" {
						q.Set("sid", cookie.Value)
						foundExtra = true
					}
				}
			}
			if foundExtra {
				u.RawQuery = q.Encode()
				result.Data.Url = u.String()
				newBody, err := json.Marshal(result)
				if err != nil {
					applog.GetLogger().Error("序列化增强后的登录结果失败: " + err.Error())
				} else {
					body = newBody
				}
			}
		}
	}

	writer.Header().Set("Content-Type", "application/json")
	writer.Write(body)
}

// verifyBilibiliCookie 验证哔哩哔哩 Cookie 有效性
func verifyBilibiliCookie(writer http.ResponseWriter, r *http.Request) {
	var req struct {
		Cookie string `json:"cookie"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJsonWithStatusCode(writer, http.StatusBadRequest, commonResp{ErrNo: http.StatusBadRequest, ErrMsg: "无效的请求体"})
		return
	}

	resp, err := requests.Get("https://api.bilibili.com/x/web-interface/nav", requests.Header("Cookie", req.Cookie))
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "验证 Cookie 失败: " + err.Error(),
		})
		return
	}
	body, err := resp.Bytes()
	if err != nil {
		writeJsonWithStatusCode(writer, http.StatusInternalServerError, commonResp{
			ErrNo:  http.StatusInternalServerError,
			ErrMsg: "读取响应体失败: " + err.Error(),
		})
		return
	}
	writer.Header().Set("Content-Type", "application/json")
	writer.Write(body)
}
