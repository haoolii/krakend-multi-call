# KrakenD：平行呼叫 Fab API 並用 Lua 移除欄位

`GET /api/allfabs` 會透過一個 KrakenD endpoint 平行呼叫 registry 中的 Fab API。目前 registry 包含 `FAB_A` 到 `FAB_J` 與 `FAB_WRONG` 共 11 個 API。每個 API 回傳 JSON array，KrakenD 在每個 backend response 上執行 Lua，刪除每筆資料的 `page` 欄位，最後將結果依 Fab 名稱聚合。

## 流程

```text
Client
  -> KrakenD GET /api/allfabs
      -> FAB_A API  -- Lua 刪除 page --+
      -> FAB_B API  -- Lua 刪除 page --+
      -> ...                              +-> 合併為一個 JSON response
      -> FAB_J API  -- Lua 刪除 page --+
```

11 個 backend 是 KrakenD 同一 endpoint 的 backend array，KrakenD 會平行執行，不會依 JSON 順序串行呼叫。

## OpenTelemetry OTLP/HTTP

KrakenD 2.6.8 支援 OTLP/HTTP。此 POC 的設定使用 `use_http: true`，將 trace 送到 Docker network 內的 `otel-collector:4318`；KrakenD 不需要放 TLS certificate：

```json
"otlp": [
  {
    "name": "local_collector",
    "host": "otel-collector",
    "port": 4318,
    "use_http": true,
    "disable_metrics": true
  }
]
```

`use_http` 選擇的是 OTLP/HTTP transport，不是強制 TLS。此 POC 使用 Docker service name 與 port 4318，所以是明文 HTTP。`observability/otel-collector.yml` 的 HTTP receiver 接收後，再將 trace 以 gRPC 送至 Tempo：

```text
KrakenD -- OTLP/HTTP :4318 --> OTel Collector -- OTLP/gRPC --> Tempo
```

明文 HTTP 只應用於受信任的內網、同 Pod 或同 Docker network。若中央 telemetry 平台要求 HTTPS/mTLS，應讓 OTel Collector 管理 CA、client certificate 與 HTTPS，KrakenD 只送 HTTP 到本機或內網 Collector。

驗證流程：

```powershell
docker compose up -d --no-deps --force-recreate krakend
curl.exe -sS -o NUL -H "X-Request-Id: otlp-http-check" http://localhost:8080/api/allfabs
docker compose logs --since 2m krakend otel-collector
```

等待數秒後，在 Grafana Explore 選擇 Tempo，查詢 service name `krakend-permission-fanout`。查到新 trace 才是端到端成功。`disable_metrics: true` 只關閉 OTLP metrics push；Prometheus 仍從 KrakenD `/metrics` pull metrics。

## Endpoint 設定

位置：`krakend/krakend.tmpl`

```json
{
  "endpoint": "/api/allfabs",
  "method": "GET",
  "output_encoding": "json",
  "backend": [
    {
      "group": "FAB_A",
      "host": ["http://mock-api:8080"],
      "url_pattern": "/fabs/FAB_A/settings",
      "encoding": "json",
      "is_collection": true,
      "extra_config": {
        "modifier/lua-backend": {
          "sources": ["/etc/krakend/response.lua"],
          "post": "remove_page_field(response.load(), 'FAB_A')"
        }
      }
    }
  ]
}
```

實際 template 以 `range .fabs.fabs` 產生各 backend。registry 的 `endpoint` 為 `http://mock-api:8080`，每個 Fab ID 會自動組成 `/fabs/{FAB_ID}/settings`，例如 FAB_A 的完整 URL 是 `http://mock-api:8080/fabs/FAB_A/settings`。

| 設定 | 意義 |
| --- | --- |
| `group: "FAB_A"` | 聚合後結果的 key，因此外層會有 `FAB_A` |
| `host` + `url_pattern` | 組成 Fab API URL |
| `encoding: "json"` | 將 JSON response 解析給 Lua 和 KrakenD 聚合器 |
| `is_collection: true` | 告知 KrakenD Fab response 的 JSON root 是 array |
| `modifier/lua-backend.post` | 每個 Fab HTTP response 回來後執行 Lua |

## Fab API 原始 Response

每隻 Fab API 回傳 JSON array，例如 FAB_A：

```json
[
  {
    "id": 1,
    "name": "example-1",
    "fab": "A",
    "page": []
  },
  {
    "id": 2,
    "name": "example-2",
    "fab": "A",
    "page": []
  }
]
```

JSON root 是 array 時，KrakenD 以 internal `collection` 欄位表示它。

## Lua 轉換

位置：`krakend/response.lua`

```lua
function remove_page_field(resp, fab)
  local data = resp:data()
  local collection = data:get(fab):get("collection")

  for i = 0, collection:len() - 1 do
    collection:get(i):del("page")
  end
end
```

步驟：

1. `response.load()` 取得目前 backend response。
2. `resp:data()` 取得 KrakenD 已解析的 JSON data。
3. `data:get(fab):get("collection")` 先取得 backend 的 group，再取得原始 JSON array。
4. 逐筆讀取 collection item。
5. `del("page")` 刪除 `page` 欄位。
6. Lua 不必重新建立 response；修改後的 data 會交給 KrakenD 聚合。

## 前端收到的 Response

```json
{
  "callFabs": ["FAB_A", "FAB_B", "FAB_C", "FAB_D", "FAB_E", "FAB_F", "FAB_G", "FAB_H", "FAB_I", "FAB_J", "FAB_WRONG"],
  "FAB_A": {
    "collection": [
      {
        "id": 1,
        "name": "example-1",
        "fab": "A"
      },
      {
        "id": 2,
        "name": "example-2",
        "fab": "A"
      }
    ]
  },
  "FAB_B": {
    "collection": [
      {
        "id": 1,
        "name": "example-1",
        "fab": "B"
      },
      {
        "id": 2,
        "name": "example-2",
        "fab": "B"
      }
    ]
  }
}
```

`page` 不會出現在最終 response。

## callFabs 與結果判斷

Response 最上層的 `callFabs` 由 KrakenD proxy static data 直接設定為 Flexible Configuration 展開的 Fab ID array，不解析成功或失敗 response，也不需要額外 metadata backend API。它列出此 endpoint 設定要呼叫的完整 Fab 清單，即使某個 Fab transport timeout。

前端對每個 `callFabs` 成員 `FAB_X` 判斷：

| Response key | 結果 |
| --- | --- |
| `FAB_X` | 成功 |
| `error_FAB_X` | Fab 回傳 HTTP error |
| `FAB_X`、`error_FAB_X` 都不存在，且 `X-Krakend-Completed: false` | transport timeout、decode error 或連線失敗 |

注意：真正 transport timeout 時 KrakenD CE 不執行 endpoint Lua post，但 proxy static data 仍會注入 `callFabs`。前端應依 `X-Krakend-Completed: false` 判斷 aggregation 不完整。

## FAB_WRONG 格式錯誤情境

`FAB_WRONG` 是測試用 backend。它的 HTTP status 是 200，root 仍是 JSON array，因此 KrakenD 仍會建立 `FAB_WRONG.collection`；但 item 故意不符合正常 contract：

```json
[
  {
    "id": "not-an-integer",
    "name": ["not-a-string"],
    "fab": "WRONG",
    "unexpected": true,
    "wrongPayload": { "nested": "unrecognized field" }
  }
]
```

正常 Fab item 有 integer `id`、string `name` 與 `page`。Lua 只會在 `page` 存在時刪除，所以 FAB_WRONG 沒有 `page` 不會出錯，但型別錯誤與額外欄位會原樣出現在前端 response。這可用於測試前端 contract validation。

## Fab Error Response

Fab API 回傳 HTTP error 時，KrakenD 會繼續回傳其他成功 Fab 的資料，並以對應 Fab key 回傳 aggregation error detail：

```json
{
  "error_FAB_B": {
    "http_status_code": 500,
    "http_body": "{\"code\":\"FAB_UNAVAILABLE\",\"message\":\"service for FAB_B is unavailable\"}"
  }
}
```

`error_FAB_B` 的命名來自 Fab backend group `FAB_B`。例如 FAB_A 失敗時，key 是 `error_FAB_A`。HTTP error 由 KrakenD `return_error_details` 處理。真正 transport timeout 在 backend Lua post 前終止 pipeline，無法用 Lua modifier 建立 `error_FAB_X`；請使用 `X-Krakend-Completed: false` 判斷 aggregation 不完整。

## 執行與驗證

```powershell
docker compose up -d --build --force-recreate
curl.exe http://localhost:8080/api/allfabs
```

驗證平行呼叫：先把三個 Fab 設成 1 秒延遲。

```powershell
curl.exe -X POST http://localhost:8081/admin/reset
curl.exe -X PUT "http://localhost:8081/admin/fabs/FAB_A?mode=success&delay_ms=1000"
curl.exe -X PUT "http://localhost:8081/admin/fabs/FAB_C?mode=success&delay_ms=1000"
curl.exe -X PUT "http://localhost:8081/admin/fabs/FAB_J?mode=success&delay_ms=1000"
curl.exe -o NUL -w "total=%{time_total}s`n" http://localhost:8080/api/allfabs
```

總時間約一秒代表平行呼叫；如果串行則約三秒。查看 Mock call counter：

```powershell
curl.exe http://localhost:8081/admin/calls
```

## 大型 Payload 與 Hang 壓測

Mock Fab 可以刻意產生大型、但最終會被 Lua 刪除的 `page`。每個正常 Fab 有兩筆主資料，以下設定會讓每筆主資料具有 3 筆 page item、每筆 page item 約 1 MiB：

```powershell
curl.exe -X PUT "http://localhost:8081/admin/fabs/all?mode=success&page_items=3&payload_kb=1024"
```

每個正常 Fab response 有兩筆主資料，每筆的 `page` 約 3 MiB，因此單一 Fab upstream response 約 6 MiB；十個正常 Fab 合計約 60 MiB 原始 JSON。KrakenD 必須先接收與解析這些資料，Lua 才能移除 `page`，所以適合觀察 CPU、heap 與 resident memory。

`hang` 不回傳 HTTP response，直到 KrakenD cancel backend connection：

```powershell
curl.exe -X PUT "http://localhost:8081/admin/fabs/all?mode=hang"
```

目前 KrakenD `response_header_timeout` 為 3 秒，因此單一 hang request 約 3 秒後取消。若 backend 延遲 20 秒但願意尊重 context cancellation，Mock active request 也會在約 3 秒後歸零：

```powershell
curl.exe -X POST "http://localhost:8081/admin/reset"
curl.exe -X PUT "http://localhost:8081/admin/fabs/FAB_B?mode=timeout&delay_ms=20000"
curl.exe -D - -o NUL -w "total=%{time_total}s`n" http://localhost:8080/api/allfabs
curl.exe http://localhost:8081/admin/state
```

`ignore_cancel` 用於模擬不理會 client disconnect 的 backend；Gateway 仍約 3 秒回應，但 `activeByFab.FAB_B` 會持續到 delay 結束。`slow_body` 先送 response headers、再延遲 body，因此 3 秒 header timeout 不適用，最終由 10 秒 endpoint timeout budget 中止。

```powershell
curl.exe -X PUT "http://localhost:8081/admin/fabs/FAB_B?mode=ignore_cancel&delay_ms=20000"
curl.exe -X PUT "http://localhost:8081/admin/fabs/FAB_B?mode=slow_body&delay_ms=20000&page_items=3&payload_kb=1024"
```

PowerShell 7 壓測：

```powershell
1..20 | ForEach-Object -Parallel {
  curl.exe -sS -o NUL http://localhost:8080/api/allfabs
} -ThrottleLimit 20
```

Git Bash 或 Linux/macOS Bash 可直接執行：

```bash
bash scripts/load-test.sh large 10
bash scripts/load-test.sh hang 30 10
bash scripts/load-test.sh large-timeout 10 1
```

第二個參數是持續秒數，第三個參數是 RPS，預設為 10。`large-timeout` 讓全部 Fab 回傳大 payload，再讓 FAB_B 延遲 20 秒；KrakenD 應在 response-header timeout 3 秒後取消 FAB_B。每個 request 輸出 `total` 實際耗時。`hang` 與 `large-timeout` 會在第一秒後顯示 Mock active connection state，最後顯示 peak 與 canceled counters。

20 個 Gateway requests 且全部 11 個 Fab hang 時，Mock peak active connections 理論最大接近 220。用以下 API 或 Grafana 觀察：

```powershell
curl.exe http://localhost:8081/admin/state
curl.exe http://localhost:8081/metrics
```
