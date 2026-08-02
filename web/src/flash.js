import { onUnmounted, reactive } from 'vue'

/**
 * Inline action feedback (next to buttons), not fixed corner toast.
 * ok() auto-clears; err() stays until next message or clear().
 *
 * Template: v-if="flash.msg" :class="flash.kind"  (reactive, no .value)
 */
export function useFlash(okMs = 4500) {
  const flash = reactive({
    msg: '',
    kind: 'ok', // 'ok' | 'err'
  })
  let timer = null

  function clearTimer() {
    if (timer) {
      clearTimeout(timer)
      timer = null
    }
  }

  function clear() {
    clearTimer()
    flash.msg = ''
  }

  function show(text, k = 'ok') {
    const t = String(text || '').trim()
    if (!t) {
      clear()
      return
    }
    clearTimer()
    flash.msg = t
    flash.kind = k === 'err' ? 'err' : 'ok'
    if (flash.kind === 'ok' && okMs > 0) {
      timer = setTimeout(() => {
        flash.msg = ''
        timer = null
      }, okMs)
    }
  }

  function ok(text) {
    show(text, 'ok')
  }

  function err(text) {
    show(text, 'err')
  }

  onUnmounted(clear)

  return Object.assign(flash, { show, ok, err, clear })
}
