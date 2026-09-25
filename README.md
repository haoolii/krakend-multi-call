# KrakenD All Fabs Settings POC

這個 POC 提供單一 API，透過 KrakenD 平行取得 10 個廠區的 settings，並支援 partial response、Prometheus metrics、OpenTelemetry traces 與 Grafana dashboard。

## 架構

```text
Client
  -> Public KrakenD :8080
      -> Aggregator KrakenD
          -> 10 Fab APIs in parallel

Public/Aggregator KrakenD
  -> Prometheus :9090 -> Grafana :3000
  -> OTel Collector -> Tempo :3200 -> Grafana :3000
```

使用兩層 KrakenD 是為了同時滿足：

- 對外整組 request timeout 為 60 秒。
- 每個廠區 API 等待 response header 最多 30 秒。
- 某個廠區 timeout 時，仍可執行 Lua 並維持固定 response schema。
- 內層 KrakenD 仍負責真正的 10 路平行 fan-out。

內層 timeout 設為 59 秒，預留 1 秒讓外層在 60 秒 deadline 前完成 Lua response formatting。

## 元件

| 元件 | 用途 | 對外網址 |
| --- | --- | --- |
| Public KrakenD | 對外 API、Lua response formatting | http://localhost:8080 |
| Aggregator KrakenD | 平行呼叫 10 個廠區 API | 僅 Docker network |
| Mock API | 模擬廠區 API 與故障情境 | http://localhost:8081 |
| Prometheus | 收集兩個 KrakenD instance 的 metrics | http://localhost:9090 |
| OTel Collector | 接收 KrakenD OTLP traces | http://localhost:4318 |
| Tempo | 儲存與查詢 traces | http://localhost:3200 |
| Grafana | Metrics dashboard 與 trace 查詢 | http://localhost:3000 |

Grafana 已啟用 anonymous admin，POC 不需要登入。

## 快速啟動

需求：Docker Desktop 與 Docker Compose。

```powershell
docker compose up -d --build
docker compose ps
```

呼叫聚合 API：

```powershell
curl.exe http://localhost:8080/api/allfabs-settings
```

停止服務：

```powershell
docker compose down
```

若要一併刪除 Prometheus、Tempo、Grafana 資料：

```powershell
docker compose down -v
```

## API Response

```json
{
  "settings": {
    "FAB_A": {
      "fab": "FAB_A",
      "timezone": "Asia/Taipei",
      "features": {
        "autoDispatch": true,
        "maintenance": false
      }
    }
  },
  "errors": {
    "FAB_J": {
      "http_status_code": 504,
      "code": "FAB_SETTINGS_TIMEOUT",
      "message": "backend did not respond within 30 seconds"
    }
  },
  "status": "partial_success",
  "summary": {
    "success": 9,
    "failed": 1,
    "total": 10
  }
}
```

`status` 有三種值：

- `success`：10 個廠區全部成功。
- `partial_success`：至少一個成功、至少一個失敗。
- `failed`：10 個廠區全部失敗。

KrakenD aggregation 使用 graceful degradation，因此 partial 或全部 backend 失敗時，HTTP response 仍為 `200`。呼叫端應以 body 的 `status` 與 `summary` 判斷結果；`X-Krakend-Completed` header 也會反映是否完整成功。

## 廠區設定

設定檔位於 `krakend/config/settings/fabs.json`：

```json
{
  "request_timeout": "59s",
  "backend_timeout": "30s",
  "items": [
    {
      "id": "FAB_A",
      "host": "http://mock-api:8080",
      "path": "/fabs/FAB_A/settings"
    }
  ]
}
```

- `request_timeout`：內層 aggregation timeout，應小於對外 60 秒。
- `backend_timeout`：每個廠區等待 response header 的上限。
- `id`：response 中的廠區 key，必須唯一。
- `host`：廠區 API base URL。
- `path`：settings API path。

修改後先驗證設定：

```powershell
docker compose run --rm --no-deps krakend-aggregator check -t -d -c /etc/krakend/krakend.tmpl
```

重新載入廠區設定：

```powershell
docker compose restart krakend-aggregator
```

`fabs.json` 不應存放 API key 或 token。正式環境應改用環境變數、Docker secrets 或外部 secret manager。

## Mock 故障測試

Mock 支援 `success`、`error`、`timeout` 三種模式。

讓 `FAB_J` 回傳 HTTP 500：

```powershell
curl.exe -X PUT "http://localhost:8081/admin/fabs/FAB_J?mode=error"
```

讓 `FAB_J` 超過 30 秒沒有 response header：

```powershell
curl.exe -X PUT "http://localhost:8081/admin/fabs/FAB_J?mode=timeout"
```

恢復單一廠區：

```powershell
curl.exe -X PUT "http://localhost:8081/admin/fabs/FAB_J?mode=success"
```

重置全部廠區：

```powershell
curl.exe -X POST http://localhost:8081/admin/reset
```

查看目前 mock 狀態：

```powershell
curl.exe http://localhost:8081/admin/state
```

## Smoke Test

測試會依序驗證全部成功、單一 HTTP 500、30 秒 timeout、全部失敗，最後自動重置 mock：

```powershell
powershell.exe -NoProfile -ExecutionPolicy Bypass -File .\scripts\smoke-test.ps1
```

timeout case 會真的等待約 30 秒，因此完整測試需要約 30 至 40 秒。

## Observability

### Metrics

KrakenD 使用 OpenTelemetry Prometheus exporter 暴露 metrics，由 Prometheus pull：

```text
KrakenD /metrics -> Prometheus -> Grafana
```

- Prometheus targets：http://localhost:9090/targets
- Grafana dashboard：http://localhost:3000/d/krakend-all-fabs

Dashboard 已預先建立，包含 service up、request rate、p95 latency 與 memory。

### Traces

Trace pipeline：

```text
KrakenD -> OTLP/HTTP -> OTel Collector -> Tempo -> Grafana
```

在 Grafana 的 `Explore` 選擇 `Tempo`，以 service name 查詢：

- `krakend-all-fabs`
- `krakend-fab-aggregator`

Trace 使用 batch export，API 呼叫後可能需要等待約 15 至 30 秒才會出現在 Tempo。

## 專案結構

```text
docker-compose.yml
krakend/
  public.json                 # Public KrakenD 與 60 秒整體 timeout
  krakend.tmpl                # Aggregator flexible configuration
  response.lua               # 固定 response schema 與 summary
  config/settings/fabs.json  # 廠區 URL 與 timeout 設定
mock-api/
  Dockerfile
  main.go
observability/
  prometheus.yml
  otel-collector.yml
  tempo.yml
  grafana/
scripts/
  smoke-test.ps1
```

## 常用診斷

```powershell
docker compose ps
docker compose logs -f krakend krakend-aggregator
docker compose logs -f otel-collector tempo
docker compose logs -f prometheus grafana
```

KrakenD 的 `response_header_timeout` 只限制等待 response header。若下游先送出 header、之後長時間卡在 response body，最終會由 59/60 秒 aggregation timeout 中止；若正式需求是整個 backend request 包含 body 都必須嚴格限制為 30 秒，需要 HTTP client plugin 或獨立 timeout proxy。
