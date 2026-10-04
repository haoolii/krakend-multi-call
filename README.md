# KrakenD Parallel Fab Lua Transform POC

此 POC 使用單一 KrakenD instance 平行呼叫 `krakend/config/settings/fabs.json` 中列出的 Fab API。目前清單為 FAB_A 到 FAB_J 與 FAB_WRONG，共 11 個。每個 Fab 回傳 JSON array，KrakenD backend Lua 會刪除每筆資料的 `page` 欄位，再依 Fab 名稱聚合 response。

Fab registry 只設定共同 endpoint 與 Fab ID 清單；template 自動產生每個 backend 的 `http://mock-api:8080/fabs/{FAB_ID}/settings` 呼叫。

完整中文說明請見 [`KRAKEND_LUA_FLOW_ZH.md`](KRAKEND_LUA_FLOW_ZH.md)。

## Start

```powershell
docker compose up -d --build --remove-orphans
curl.exe http://localhost:8080/api/allfabs
```

服務網址：

- KrakenD API: http://localhost:8080/api/allfabs
- Mock Admin API: http://localhost:8081
- Prometheus: http://localhost:9090
- Grafana: http://localhost:3000
- Tempo: http://localhost:3200

## OTLP/HTTP

KrakenD 2.6.8 支援 OTLP/HTTP。此 POC 的 KrakenD 對同一 Docker network 內的 OTel Collector 使用明文 HTTP，因此 KrakenD 不需要掛載 TLS certificate：

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

`use_http: true` 指的是使用 OTLP/HTTP transport；是否使用 TLS 取決於 exporter endpoint。此 POC 的 bare Docker service name `otel-collector` 搭配 port `4318` 是明文 HTTP。Collector 在 `observability/otel-collector.yml` 開啟對應 receiver：

```yaml
receivers:
  otlp:
    protocols:
      http:
        endpoint: 0.0.0.0:4318
```

Trace flow：

```text
KrakenD -- OTLP/HTTP :4318 --> OTel Collector -- OTLP/gRPC --> Tempo
```

這只適用於受信任的內網、同 Pod 或同 Docker network。若公司中央 telemetry endpoint 強制 HTTPS 或 mTLS，請保留 KrakenD 到本機/內網 Collector 的 HTTP，改由 Collector 負責 HTTPS、CA 與 client certificate；不要為了移除 KrakenD certificate 而將中央 endpoint 改成未加密 HTTP。

### Verify OTLP Traces

1. 確認 `krakend/krakend.tmpl` 使用 `use_http: true`、port `4318`，且 Collector HTTP receiver 已啟用。
2. 重新建立 KrakenD 以載入設定：

```powershell
docker compose up -d --no-deps --force-recreate krakend
```

3. 產生帶有可追蹤 request ID 的流量：

```powershell
curl.exe -sS -o NUL -H "X-Request-Id: otlp-http-check" http://localhost:8080/api/allfabs
```

4. 等待數秒讓 exporter 與 Collector batch flush，確認兩個服務沒有 export error：

```powershell
docker compose logs --since 2m krakend otel-collector
```

常見失敗字串包括 `connection refused`、`404`、`x509`、`tls` 與 `failed to upload traces`。

5. 開啟 Grafana `http://localhost:3000`，在 **Explore** 選擇 **Tempo**，查詢 service name `krakend-permission-fanout`，時間範圍設為最近 15 分鐘。查到剛產生的 trace 才代表 KrakenD -> Collector -> Tempo 的端到端流程成功。

此設定的 `disable_metrics: true` 代表 OTLP exporter 只送 traces；KrakenD metrics 仍由 Prometheus 從 `http://localhost:9091/metrics` pull，這是預期行為。

## Response

```json
{
  "callFabs": ["FAB_A", "FAB_B", "FAB_C", "FAB_D", "FAB_E", "FAB_F", "FAB_G", "FAB_H", "FAB_I", "FAB_J", "FAB_WRONG"],
  "FAB_A": {
    "collection": [
      { "id": 1, "name": "example-1", "fab": "A" },
      { "id": 2, "name": "example-2", "fab": "A" }
    ]
  }
}
```

`callFabs` 由 KrakenD proxy static data 直接設定為 `krakend/config/settings/fabs.json` 展開後的 Fab ID array，不解析成功或失敗 response，也不需要額外 metadata backend API。它永遠列出 KrakenD 此 endpoint 設定要呼叫的完整 Fab 清單，即使某個 Fab transport timeout：

- `FAB_X` 存在：成功。
- `error_FAB_X` 存在：Fab 回傳 HTTP error。
- `FAB_X` 與 `error_FAB_X` 都不存在，且 `X-Krakend-Completed: false`：transport timeout、decode error 或連線失敗。

真正 transport timeout 時，KrakenD CE 不會執行 endpoint Lua post，但 `callFabs` 改由 proxy static data 注入；請使用 `X-Krakend-Completed: false` 判斷 aggregation 不完整。

`FAB_WRONG` 是刻意加入的 item contract 錯誤 backend：root 仍回傳 JSON array，所以最終仍有 `FAB_WRONG.collection`；但 item 不含 `page`，`id` 是 string、`name` 是 array，還有未預期欄位。Lua 只會在 `page` 存在時刪除，因此 FAB_WRONG 的錯誤欄位會原樣傳給前端。

Fab 回傳 HTTP error 時，保留成功 Fab，並以 `error_FAB_X` object 回傳該 backend error：

```json
{
  "FAB_A": { "collection": [] },
  "error_FAB_B": {
    "http_status_code": 500,
    "http_body": "{\"code\":\"FAB_UNAVAILABLE\"}"
  }
}
```

`error_FAB_X` 的 HTTP error detail 由 KrakenD `backend/http.return_error_details` 原生產生。真正 transport timeout 在 Lua backend post 前終止 pipeline，因此不會有 `error_FAB_X` entry；請使用 `X-Krakend-Completed: false` 判斷 aggregation 不完整。

Mock Fab behavior can be changed for demos:

```powershell
curl.exe -X PUT "http://localhost:8081/admin/fabs/FAB_A?mode=success&delay_ms=1000"
curl.exe -X PUT "http://localhost:8081/admin/fabs/FAB_B?mode=error"
curl.exe -X POST http://localhost:8081/admin/reset
```

## Large Payload and Hang Load Test

每個正常 Fab response 有兩筆主資料。Mock reset 後，每筆主資料的 `page` 有 3 筆、每筆約 1 MiB 的 payload，合計約 3 MiB；因此單一 Fab upstream response 約 6 MiB，十個正常 Fab 合計約 60 MiB 原始 JSON。KrakenD 接收及解析後，backend Lua 才會刪除 `page`，所以 `/api/allfabs` 最終 response 不含這些 payload。

設定全部 Fab 使用大型 payload：

```powershell
curl.exe -X PUT "http://localhost:8081/admin/fabs/all?mode=success&page_items=3&payload_kb=1024"
```

設定單一 Fab：

```powershell
curl.exe -X PUT "http://localhost:8081/admin/fabs/FAB_A?mode=success&page_items=3&payload_kb=1024"
```

讓一個或全部 backend 維持連線、不回傳 response：

```powershell
curl.exe -X PUT "http://localhost:8081/admin/fabs/FAB_A?mode=hang"
curl.exe -X PUT "http://localhost:8081/admin/fabs/all?mode=hang"
```

KrakenD 的 backend response-header timeout 為 3 秒，所以 `hang` request 約 3 秒後會被 KrakenD cancel，不會永久堆積。持續併發 request 才能觀察連線堆積。

測試 20 秒 backend 與 KrakenD timeout 的關係：

```powershell
# KrakenD 約 3 秒取消，Mock FAB_B 收到 cancel 後立即退出。
curl.exe -X POST "http://localhost:8081/admin/reset"
curl.exe -X PUT "http://localhost:8081/admin/fabs/FAB_B?mode=timeout&delay_ms=20000"
curl.exe -D - -o NUL -w "total=%{time_total}s`n" "http://localhost:8080/api/allfabs"

# Mock FAB_B 刻意忽略 cancel；Gateway 約 3 秒回應，但 FAB_B active 維持約 20 秒。
curl.exe -X POST "http://localhost:8081/admin/reset"
curl.exe -X PUT "http://localhost:8081/admin/fabs/FAB_B?mode=ignore_cancel&delay_ms=20000"
curl.exe -D - -o NUL -w "total=%{time_total}s`n" "http://localhost:8080/api/allfabs"

# Mock 先送 headers 再延遲 body；3 秒 header timeout 不適用，會消耗剩餘的 10 秒 endpoint timeout budget。
curl.exe -X POST "http://localhost:8081/admin/reset"
curl.exe -X PUT "http://localhost:8081/admin/fabs/FAB_B?mode=slow_body&delay_ms=20000&page_items=3&payload_kb=1024"
curl.exe -D - -o NUL -w "total=%{time_total}s`n" "http://localhost:8080/api/allfabs"
```

檢查個別 Fab backend 是否仍在執行：

```powershell
curl.exe http://localhost:8081/admin/state
```

查看 `activeByFab.FAB_B`。`timeout` 時 gateway 回應後應為 `0`；`ignore_cancel` 時會維持 `1` 到第 20 秒。Grafana 的 `Mock Active Requests By Fab`、`KrakenD Heap Detail` 與 `KrakenD Garbage Collection Rate` 可用於觀察同一輪測試。

PowerShell 7 可用以下指令建立 20 個平行 Gateway requests：

```powershell
1..20 | ForEach-Object -Parallel {
  curl.exe -sS -o NUL http://localhost:8080/api/allfabs
} -ThrottleLimit 20
```

Git Bash 或 Linux/macOS Bash 可直接使用壓測腳本：

```bash
bash scripts/load-test.sh large 10
bash scripts/load-test.sh hang 30
bash scripts/load-test.sh hang 30 2
bash scripts/load-test.sh large-timeout 10 1
```

第二個參數是持續秒數，第三個參數是 RPS，預設為 10。例如 `hang 30 2` 會在 30 秒內送出 60 個 Gateway requests。每個 request 會輸出 `total` 實際耗時。`large-timeout` 讓 FAB_B 延遲 20 秒，KrakenD 應在約 3 秒取消它，同時其他 Fab 回傳大型 payload。腳本結束或按 Ctrl+C 時會自動重置 Mock，避免測試狀態保留。

KrakenD container 預設限制為 1 CPU，超出處理能力時會增加 latency 而不搶佔其他容器的 CPU。需要調整時，在啟動前設定 `KRAKEND_CPUS`，例如 PowerShell：

```powershell
$env:KRAKEND_CPUS = "2.0"
docker compose up -d krakend
```

觀察：

- Grafana: http://localhost:3000，選擇 `KrakenD Parallel Fab Load`
- 即時 container 資源: `docker stats kareknd-mo-krakend-1`
- Mock active/peak/canceled 狀態: `curl.exe http://localhost:8081/admin/state`
- Mock Prometheus metrics: `curl.exe http://localhost:8081/metrics`

完成後恢復小型成功 response：

```powershell
curl.exe -X POST http://localhost:8081/admin/reset
```
