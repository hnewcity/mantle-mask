// A completion-based poller: no overlapping background requests, no work in
// hidden tabs, and cancellation on teardown. Explicit refresh replaces stale
// work so a refresh after an edit cannot reuse a pre-edit response.
export function startPolling(load: (signal: AbortSignal) => Promise<void>, interval = 15000) {
  let timer: ReturnType<typeof setTimeout> | undefined
  let active: AbortController | undefined
  let stopped = false
  function clearTimer() { clearTimeout(timer); timer = undefined }
  function background() { void refresh().catch(() => { /* Loaders report non-abort failures. */ }) }
  async function refresh() {
    if (stopped) return
    clearTimer()
    active?.abort()
    const controller = new AbortController()
    active = controller
    try { await load(controller.signal) }
    finally {
      // A failed Promise.all can leave sibling fetches running. Always close
      // this batch before forgetting its controller or scheduling another one.
      controller.abort()
      if (active === controller) {
        active = undefined
        if (!stopped && !document.hidden) timer = setTimeout(background, interval)
      }
    }
  }
  function visibilityChanged() {
    clearTimer()
    if (document.hidden) { active?.abort(); active = undefined }
    else background()
  }
  document.addEventListener("visibilitychange", visibilityChanged)
  if (!document.hidden) background()
  return {
    refresh,
    stop() {
      stopped = true
      clearTimer()
      active?.abort()
      active = undefined
      document.removeEventListener("visibilitychange", visibilityChanged)
    },
  }
}
