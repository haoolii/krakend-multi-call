function format_all_fabs_response(resp)
  local data = resp:data()
  local expected = data:get("_expected")
  data:del("_expected")
  local keys = data:keys()
  local settings = luaTable.new()
  local errors = luaTable.new()
  local completed = {}
  local success_count = 0
  local failed_count = 0

  for i = 0, keys:len() - 1 do
    local key = keys:get(i)
    local value = data:get(key)

    if string.sub(key, 1, 6) == "error_" then
      local fab = string.sub(key, 7)
      errors:set(fab, value)
      completed[fab] = true
      failed_count = failed_count + 1
    else
      settings:set(key, value)
      completed[key] = true
      success_count = success_count + 1
    end

    data:del(key)
  end

  if expected ~= nil then
    local expected_keys = expected:keys()
    for i = 0, expected_keys:len() - 1 do
      local fab = expected_keys:get(i)
      if not completed[fab] then
        local timeout_error = luaTable.new()
        timeout_error:set("http_status_code", 504)
        timeout_error:set("code", "FAB_SETTINGS_TIMEOUT")
        timeout_error:set("message", "backend did not respond within 30 seconds")
        errors:set(fab, timeout_error)
        failed_count = failed_count + 1
      end
    end
  end

  local status = "success"
  if success_count == 0 then
    status = "failed"
  elseif failed_count > 0 then
    status = "partial_success"
  end

  local summary = luaTable.new()
  summary:set("success", success_count)
  summary:set("failed", failed_count)
  summary:set("total", success_count + failed_count)

  data:set("settings", settings)
  data:set("errors", errors)
  data:set("status", status)
  data:set("summary", summary)
  resp:isComplete(failed_count == 0)
end
