function remove_page_field(resp, fab)
  local data = resp:data()
  local grouped = data:get(fab)
  if grouped == nil then
    print(string.format("event=fab.response.error fab=%s", fab))
    return
  end
  local collection = grouped:get("collection")
  if collection == nil then
    print(string.format("event=fab.response.unexpected_shape fab=%s", fab))
    return
  end

  for i = 0, collection:len() - 1 do
    local item = collection:get(i)
    if item:get("page") ~= nil then
      item:del("page")
    end
  end

  print(string.format("event=fab.response.transformed fab=%s items=%d", fab, collection:len()))
end
