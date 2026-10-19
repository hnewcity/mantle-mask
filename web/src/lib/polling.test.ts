import { afterEach, beforeEach, describe, expect, it, vi } from "vitest"
import { startPolling } from "./polling"

class Visibility extends EventTarget {
  hidden = false
  setHidden(hidden: boolean) { this.hidden = hidden; this.dispatchEvent(new Event("visibilitychange")) }
}

describe("completion-based polling", () => {
  let visibility: Visibility
  beforeEach(() => { vi.useFakeTimers(); visibility = new Visibility(); vi.stubGlobal("document", visibility) })
  afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals() })
  it("waits for a slow request before scheduling the next", async () => {
    let finish!: () => void
    const load = vi.fn(() => new Promise<void>(resolve => { finish = resolve }))
    const poller = startPolling(load, 100)
    await vi.advanceTimersByTimeAsync(1000)
    expect(load).toHaveBeenCalledTimes(1)
    finish()
    await vi.advanceTimersByTimeAsync(99)
    expect(load).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(load).toHaveBeenCalledTimes(2)
    poller.stop()
    finish()
  })
  it("cancels hidden/unmounted requests and resumes once visible", async () => {
    const signals: AbortSignal[] = []
    const load = vi.fn(async (signal: AbortSignal) => { signals.push(signal) })
    const poller = startPolling(load, 100)
    visibility.setHidden(true)
    expect(signals[0].aborted).toBe(true)
    await vi.advanceTimersByTimeAsync(1000)
    expect(load).toHaveBeenCalledTimes(1)
    visibility.setHidden(false)
    expect(load).toHaveBeenCalledTimes(2)
    poller.stop()
    expect(signals[1].aborted).toBe(true)
    visibility.setHidden(true); visibility.setHidden(false)
    await vi.advanceTimersByTimeAsync(1000)
    expect(load).toHaveBeenCalledTimes(2)
  })
  it("replaces stale work on manual refresh without duplicate timers", async () => {
    const signals: AbortSignal[] = [], finish: (() => void)[] = []
    const load = vi.fn((signal: AbortSignal) => { signals.push(signal); return new Promise<void>(resolve => finish.push(resolve)) })
    const poller = startPolling(load, 100)
    const refresh = poller.refresh()
    expect(signals[0].aborted).toBe(true)
    finish[0]()
    await vi.advanceTimersByTimeAsync(1000)
    expect(load).toHaveBeenCalledTimes(2)
    finish[1](); await refresh
    await vi.advanceTimersByTimeAsync(100)
    expect(load).toHaveBeenCalledTimes(3)
    poller.stop(); finish[2]()
  })
  it("does not start while hidden and continues after a rejected load", async () => {
    visibility.hidden = true
    const load = vi.fn(async () => { throw new Error("offline") })
    const poller = startPolling(load, 100)
    await vi.advanceTimersByTimeAsync(1000)
    expect(load).not.toHaveBeenCalled()
    visibility.setHidden(false)
    await vi.advanceTimersByTimeAsync(100)
    expect(load).toHaveBeenCalledTimes(2)
    poller.stop()
  })
  it("aborts unfinished siblings when a batch rejects early", async () => {
    let pending: AbortSignal | undefined
    const load = vi.fn(async (signal: AbortSignal) => {
      pending = signal
      await Promise.all([
        Promise.reject(new Error("one endpoint failed")),
        new Promise<void>(resolve => signal.addEventListener("abort", () => resolve(), { once: true })),
      ])
    })
    const poller = startPolling(load, 100)
    await vi.advanceTimersByTimeAsync(0)
    expect(pending?.aborted).toBe(true)
    await vi.advanceTimersByTimeAsync(100)
    expect(load).toHaveBeenCalledTimes(2)
    expect(pending?.aborted).toBe(true)
    poller.stop()
  })
})
