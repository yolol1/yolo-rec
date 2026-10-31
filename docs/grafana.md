# Prometheus 与 Grafana 配置

本项目不包含 Docker 运行支持。请在本机或独立服务器安装 Prometheus 和 Grafana。

Prometheus 的 `prometheus.yml` 需要配置以下抓取任务：

``` yml
global:
  scrape_interval: 15s
scrape_configs:
  - job_name: "bililive"
    metrics_path: "/api/metrics"
    scheme: http
    static_configs:
      - targets: ["127.0.0.1:8080"] # 自行修改 IP 和端口
```

在 Grafana 中导入 [dashboard.json](/contrib/grafana/dashboard.json) 即可创建监控面板。
