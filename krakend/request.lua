function prepare_skip_fabs()
  local request = ctx.load()
  local body = request:body()

  -- The POST payload controls KrakenD only; FAB backends continue to receive GET requests.
  request:body("")
  request:headers("X-Internal-Skip-Fabs", nil)

  local skip_fabs = string.match(body, '"skipFabs"%s*:%s*%[(.-)%]')
  if skip_fabs == nil then
    return
  end

  local normalized = string.upper(skip_fabs)
  normalized = string.gsub(normalized, '"', '')
  normalized = string.gsub(normalized, '%s+', '')
  if normalized ~= "" then
    request:headers("X-Internal-Skip-Fabs", normalized)
  end
end
