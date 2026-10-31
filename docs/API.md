# Bililive-go API

## `GET /api/info` Get app info
- Request:
    ```text
    method: GET
    path: http://127.0.0.1:8080/api/info
    ```
- Response:
    ```json
    {
      "app_name": "BiliLive-go",
      "app_version": "0.5.0-rc.3-3-g31ceeda",
      "build_time": "2020-05-05_01:07:16",
      "git_hash": "31ceeda8f508ba5546cfdefef5f3945828a87651",
      "pid": 33295,
      "platform": "darwin/amd64",
      "go_version": "go1.14.2"
    }
    ```
        
## `GET /api/lives` Get all live info 
- Request:  
    ```text
    method: GET
    path: http://127.0.0.1:8080/api/lives
    ```
- Response:   
    ```json
    [
      {
        "id": "212d9c98c7b376b730d4336bb49f6d3f",
        "live_url": "https://live.bilibili.com/14917277",
        "platform_cn_name": "哔哩哔哩",
        "host_name": "湊-阿库娅Official",
        "room_name": "【B站限定】棉花糖＆唱歌！！！！",
        "status": false,
        "listening": true,
        "recording": false
      },
      {
        "id": "63dc965c77d3d81058c92c3e38822256",
        "live_url": "https://live.bilibili.com/11588230",
        "platform_cn_name": "哔哩哔哩",
        "host_name": "白上吹雪Official",
        "room_name": "古老niconico老人会with☆乐园",
        "status": false,
        "listening": true,
        "recording": false
      },
      {
        "id": "dfb964a56725bbad165cb9ea1ef8ac5b",
        "live_url": "https://live.bilibili.com/1030",
        "platform_cn_name": "哔哩哔哩",
        "host_name": "怕上火暴王老菊",
        "room_name": "直播做饭",
        "status": false,
        "listening": true,
        "recording": false
      }
    ]
    ```
        
## `GET /api/lives/{id}` Get live info by id
- Request:  
    ```text
    method: GET
    path: http://127.0.0.1:8080/api/lives/212d9c98c7b376b730d4336bb49f6d3f
    ```
- Response:
    ```json
    {
      "id": "212d9c98c7b376b730d4336bb49f6d3f",
      "live_url": "https://live.bilibili.com/14917277",
      "platform_cn_name": "哔哩哔哩",
      "host_name": "湊-阿库娅Official",
      "room_name": "【B站限定】棉花糖＆唱歌！！！！",
      "status": false,
      "listening": true,
      "recording": false
    }
    ```
        
## `POST /api/lives` Add live
- Request:  
    ```text
    method: POST
    path: http://127.0.0.1:8080/api/lives
    body: 
        [
            {
                "url": "https://live.bilibili.com/14917277",
                "listen": true
            }
        ]
    ```
- Response:
    ```json
    [
        {
            "id": "212d9c98c7b376b730d4336bb49f6d3f",
            "live_url": "https://live.bilibili.com/14917277",
            "platform_cn_name": "哔哩哔哩",
            "host_name": "湊-阿库娅Official",
            "room_name": "【B站限定】棉花糖＆唱歌！！！！",
            "status": false,
            "listening": true,
            "recording": false
        }
    ]
    ```        
        
## `DELETE /api/lives/{id}` Delete live by id
- Request:  
    ```text
    method: DELETE
    path: http://127.0.0.1:8080/api/lives/212d9c98c7b376b730d4336bb49f6d3f
    ```
- Response:
    ```json
    {
        "err_no": 0,
        "err_msg": "",
        "data": "OK"
    }
    ```

## `GET /api/lives/{id}/start` Start listen live by id
- Request:  
    ```text
    method: GET
    path: http://127.0.0.1:8080/api/lives/212d9c98c7b376b730d4336bb49f6d3f/start
    ```
- Response:
    ```json
    {
        "id": "212d9c98c7b376b730d4336bb49f6d3f",
        "live_url": "https://live.bilibili.com/14917277",
        "platform_cn_name": "哔哩哔哩",
        "host_name": "湊-阿库娅Official",
        "room_name": "【B站限定】棉花糖＆唱歌！！！！",
        "status": false,
        "listening": true,
        "recording": false
    }
    ```
        
## `GET /api/lives/{id}/stop` Stop listen and record live by id
- Request:  
    ```text
    method: GET
    path: http://127.0.0.1:8080/api/lives/212d9c98c7b376b730d4336bb49f6d3f/stop
    ```
- Response:
    ```json
    {
        "id": "212d9c98c7b376b730d4336bb49f6d3f",
        "live_url": "https://live.bilibili.com/14917277",
        "platform_cn_name": "哔哩哔哩",
        "host_name": "湊-阿库娅Official",
        "room_name": "【B站限定】棉花糖＆唱歌！！！！",
        "status": false,
        "listening": false,
        "recording": false
    }
    ```

## `POST /api/lives/{id}/playback` 创建播放器直播地址

该接口按直播间当前流偏好解析直播流，返回稳定的本地播放地址。播放地址只实时转发 FLV 或 HLS 数据，不会创建录制任务，也不会写入视频文件。

- 请求：
    ```text
    method: POST
    path: http://127.0.0.1:8080/api/lives/212d9c98c7b376b730d4336bb49f6d3f/playback
    ```
- 响应：
    ```json
    {
      "url": "/api/playback/6Bz5b9Y5xQ0fVg7oPqRwSg/stream.flv",
      "format": "flv",
      "quality": "原画",
      "stream_name": "原画 - flv",
      "proxy": true
    }
    ```

返回的 `url` 是相对于服务根路径的地址。浏览器中会使用当前 WebUI 的来源地址生成完整播放地址，可直接复制到 PotPlayer 等支持网络串流的播放器。播放会话默认持续 24 小时，期间每次访问都会续期；服务重启后需重新创建地址。
        
## `GET /api/config` Get config info
- Request:  
    ```text
    method: GET
    path: http://127.0.0.1:8080/api/config
    ```
- Response:
    ```json
    {
      "RPC": {
        "Enable": true,
        "Bind": "127.0.0.1:8080"
      },
      "Debug": false,
      "Interval": 15,
      "OutPutPath": "/tmp",
      "Feature": {
        "UseNativeFlvParser": false
      },
      "LiveRooms": null
    }
    ```
        
## `PUT /api/config` Save lives info to config file
- Request:  
    ```text
    method: PUT
    path: http://127.0.0.1:8080/api/config
    ```
- Response:
    ```json
    {
        "err_no": 0,
        "err_msg": "",
        "data": "OK"
    }
    ```

## `GET /api/raw-config` Get raw config file
- Request:
    ```text
    method: GET
    path: http://127.0.0.1:8080/api/raw-config
    ```
- Response:
    ```json
    {
        "config": "rpc:\n  enable: true\n  bind: 0.0.0.0:8080\ndebug: false\ninterval: 15\nout_put_path: ./\nfeature:\n  use_native_flv_parser: false\nlive_rooms:\n- url: https://www.huya.com/991111\n  is_listening: false\nout_put_tmpl: \"\"\nvideo_split_strategies:\n  on_room_name_changed: false\n  max_duration: 0s\ncookies:\n  live.douyin.com: name1=qwer;name2=asdf;aaaa\non_record_finished:\n  convert_to_mp4: true\n  delete_flv_after_convert: false\ntimeout_in_us: 50000000\n"
    }
    ```

## `PUT /api/raw-config` Save the whole config file
- Request:
    ```text
    method: PUT
    path: http://127.0.0.1:8080/api/raw-config
    body:
        {
            "config": "rpc:\n  enable: true\n  bind: 0.0.0.0:8080\ndebug: false\ninterval: 15\nout_put_path: ./\nfeature:\n  use_native_flv_parser: false\nlive_rooms:\n- url: https://www.huya.com/991111\n  is_listening: false\nout_put_tmpl: \"\"\nvideo_split_strategies:\n  on_room_name_changed: false\n  max_duration: 0s\ncookies:\n  live.douyin.com: name1=qwer;name2=asdf;aaaa\non_record_finished:\n  convert_to_mp4: true\n  delete_flv_after_convert: false\ntimeout_in_us: 50000000\n"
        }
    ```
- Response:
    ```json
    {
        "err_no": 0,
        "err_msg": "",
        "data": "OK"
    }
    ```
