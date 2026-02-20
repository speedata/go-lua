-- No-op tracegc for go-lua (no __gc metamethod support)
local M = {}
function M.start() end
function M.stop() end
return M
