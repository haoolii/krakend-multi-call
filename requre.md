我現在需要一個 POC
主要針對 karkenD 可以透過一發 Request 後面打出 10知 API 拿回 10個廠區的 settings

格式大概是
GET /api/allfabs-settings
{
    fabsError: { //欄位名稱幫我想
        "FAB_A": {...} //自由發揮
    },
    xxxx: { //欄位名稱幫我想
        "FAB_B": {
            .... // 回應內容
        },
        ....
    },
    status: "partial_success",
    summary // 或其他名稱 {
        success: 9,
        failed: 1,
        total: 10
    }
}


這要跑在docker
然後我還要有 grafana 

krakenD 可以透過 otel & Prometheus 送到 (?) 我也不知道送到哪 
反正我需要可以用 Grafana 撈取這些 metrics
我要這樣的POC 你看看有甚麼問題