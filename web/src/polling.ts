type PollEnvironment = {
  visibility: Pick<Document, 'hidden' | 'addEventListener' | 'removeEventListener'>
  schedule: typeof setTimeout
  cancel: typeof clearTimeout
}

// The caller handles read errors; one pending read is allowed to finish when hidden.
export function startVisiblePolling(task: (signal: AbortSignal) => Promise<void>, interval: number, environment: PollEnvironment = { visibility: document, schedule: setTimeout, cancel: clearTimeout }) {
  const { visibility, schedule, cancel } = environment
  const controller = new AbortController()
  let timer: ReturnType<typeof setTimeout> | undefined
  let running = false
  let loaded = false
  const refresh = async () => {
    if (running || visibility.hidden || controller.signal.aborted || (interval === 0 && loaded)) return
    running = true
    loaded = true
    cancel(timer)
    try { await task(controller.signal) }
    finally {
      running = false
      if (interval > 0 && !visibility.hidden && !controller.signal.aborted) timer = schedule(() => { void refresh() }, interval)
    }
  }
  const changed = () => {
    cancel(timer)
    if (!visibility.hidden) void refresh()
  }
  visibility.addEventListener('visibilitychange', changed)
  void refresh()
  return () => {
    controller.abort()
    cancel(timer)
    visibility.removeEventListener('visibilitychange', changed)
  }
}
