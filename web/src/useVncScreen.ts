import { useEffect, useRef, useState } from 'react'
import RFB from '@novnc/novnc'

export type VncStatus = 'idle' | 'connecting' | 'connected' | 'disconnected'

const RECONNECT_BASE_MS = 1000
const RECONNECT_MAX_MS = 15000

export function vncGatewayUrl(gatewayBase: string, vncNodeId: string): string {
  return `${gatewayBase}/vnc?node=${encodeURIComponent(vncNodeId)}`
}

declare global {
  interface Window {
    __vncStatus?: Record<string, VncStatus>
    __vncClients?: Record<string, RFB>
    __vncTxBytes?: number
    __vncTxHooked?: boolean
  }
}

function hookOutboundByteCounter() {
  if (typeof window === 'undefined' || window.__vncTxHooked) {
    return
  }
  window.__vncTxHooked = true
  window.__vncTxBytes = 0
  const original = WebSocket.prototype.send
  WebSocket.prototype.send = function patched(this: WebSocket, data: Parameters<WebSocket['send']>[0]) {
    const size =
      typeof data === 'string'
        ? data.length
        : data instanceof ArrayBuffer
          ? data.byteLength
          : 'byteLength' in (data as ArrayBufferView)
            ? (data as ArrayBufferView).byteLength
            : 0
    window.__vncTxBytes = (window.__vncTxBytes ?? 0) + size
    return original.call(this, data)
  }
}

hookOutboundByteCounter()

function publishStatus(nodeKey: string, status: VncStatus) {
  if (!window.__vncStatus) window.__vncStatus = {}
  window.__vncStatus[nodeKey] = status
}

export function useVncScreen(
  nodeKey: string,
  vncNodeId: string | undefined,
  gatewayBase: string,
) {
  const containerRef = useRef<HTMLDivElement | null>(null)
  const clientRef = useRef<RFB | null>(null)
  const [status, setStatus] = useState<VncStatus>('idle')

  const focus = () => {
    clientRef.current?.focus()
  }

  useEffect(() => {
    const container = containerRef.current
    if (!container || !vncNodeId) {
      setStatus('idle')
      publishStatus(nodeKey, 'idle')
      return
    }

    let rfb: RFB | null = null
    let retryTimer: number | undefined
    let attempt = 0
    let cancelled = false

    const apply = (next: VncStatus) => {
      if (cancelled) return
      setStatus(next)
      publishStatus(nodeKey, next)
    }

    const scheduleReconnect = () => {
      if (cancelled) return
      const delay = Math.min(RECONNECT_BASE_MS * 2 ** attempt, RECONNECT_MAX_MS)
      attempt += 1
      retryTimer = window.setTimeout(connect, delay)
    }

    const connect = () => {
      if (cancelled) return
      apply('connecting')
      try {
        rfb = new RFB(container, vncGatewayUrl(gatewayBase, vncNodeId))
      } catch {
        apply('disconnected')
        scheduleReconnect()
        return
      }
      rfb.scaleViewport = true
      rfb.resizeSession = false
      rfb.viewOnly = false
      rfb.focusOnClick = true
      clientRef.current = rfb
      if (!window.__vncClients) window.__vncClients = {}
      window.__vncClients[nodeKey] = rfb
      rfb.addEventListener('connect', () => {
        attempt = 0
        apply('connected')
      })
      rfb.addEventListener('disconnect', () => {
        apply('disconnected')
        rfb = null
        scheduleReconnect()
      })
    }

    connect()

    return () => {
      cancelled = true
      if (retryTimer !== undefined) window.clearTimeout(retryTimer)
      if (rfb) rfb.disconnect()
      publishStatus(nodeKey, 'idle')
    }
  }, [nodeKey, vncNodeId, gatewayBase])

  return { containerRef, status, focus }
}
