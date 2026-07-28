import { useEffect, useRef } from 'react'
import type { Dispatch, SetStateAction } from 'react'
import type { Node, Edge } from '@xyflow/react'
import type { MicroVMNodeData, MigrationEdgeData } from './types'

export type MigrationEventType =
  | 'migration_start'
  | 'migration_progress'
  | 'migration_complete'
  | 'migration_failed'

export interface MigrationEvent {
  type: MigrationEventType
  migration_id: string
  from?: string
  to?: string
  phase?: string
  blackout_ms?: number
  pass?: boolean
  error?: string
}

declare global {
  interface Window {
    __migrationEventLog?: MigrationEvent[]
  }
}

export interface UseMigrationEventsOptions {
  url?: string
  fadeDurationMs?: number
  holdDurationMs?: number
  hostNodeIds?: Record<string, string>
  edgeId: string
  setNodes: Dispatch<SetStateAction<Node<MicroVMNodeData>[]>>
  setEdges: Dispatch<SetStateAction<Edge<MigrationEdgeData>[]>>
  onOwnerChanged?: (toHostId: string) => void
}

export const DEFAULT_MIGRATION_EVENTS_URL = 'http://localhost:7040/v1/migrations/events'
export const DEFAULT_FADE_DURATION_MS = 600
export const DEFAULT_HOLD_DURATION_MS = 2500

function appendToEventLog(event: MigrationEvent): void {
  if (typeof window === 'undefined') {
    return
  }
  const existing = window.__migrationEventLog ?? []
  window.__migrationEventLog = [...existing, event]
}

export function useMigrationEvents(options: UseMigrationEventsOptions): void {
  const optionsRef = useRef(options)
  optionsRef.current = options
  const url = options.url ?? DEFAULT_MIGRATION_EVENTS_URL

  useEffect(() => {
    const pairByMigrationId = new Map<string, { from: string; to: string }>()
    const fadeTimers = new Map<string, ReturnType<typeof setTimeout>>()

    const resolveNodeId = (hostId: string): string => {
      const mapping = optionsRef.current.hostNodeIds
      return mapping?.[hostId] ?? hostId
    }

    const setNodeHighlight = (hostIds: string[], value: boolean): void => {
      const targetNodeIds = new Set(hostIds.map(resolveNodeId))
      optionsRef.current.setNodes((prevNodes) =>
        prevNodes.map((node) =>
          targetNodeIds.has(node.id)
            ? { ...node, data: { ...node.data, migrationHighlight: value } }
            : node,
        ),
      )
    }

    const setEdgeFlags = (
      flags: Partial<Pick<MigrationEdgeData, 'migrating' | 'holding' | 'fadingOut'>>,
    ): void => {
      const targetEdgeId = optionsRef.current.edgeId
      optionsRef.current.setEdges((prevEdges) =>
        prevEdges.map((edge) => {
          if (edge.id !== targetEdgeId || !edge.data) {
            return edge
          }
          const data = { ...edge.data, ...flags }
          const visible = data.migrating || data.holding || data.fadingOut
          return { ...edge, data, hidden: !visible }
        }),
      )
    }

    const clearFadeTimer = (migrationId: string): void => {
      const pending = fadeTimers.get(migrationId)
      if (pending !== undefined) {
        clearTimeout(pending)
        fadeTimers.delete(migrationId)
      }
    }

    const resolvePair = (event: MigrationEvent): { from: string; to: string } | undefined => {
      const tracked = pairByMigrationId.get(event.migration_id)
      if (tracked) {
        return tracked
      }
      if (event.from && event.to) {
        return { from: event.from, to: event.to }
      }
      return undefined
    }

    const handleEvent = (event: MigrationEvent): void => {
      appendToEventLog(event)

      switch (event.type) {
        case 'migration_start': {
          clearFadeTimer(event.migration_id)
          if (event.from && event.to) {
            pairByMigrationId.set(event.migration_id, { from: event.from, to: event.to })
            setNodeHighlight([event.from, event.to], true)
          }
          setEdgeFlags({ migrating: true, holding: false, fadingOut: false })
          break
        }
        case 'migration_complete': {
          const pair = resolvePair(event)
          pairByMigrationId.delete(event.migration_id)
          setEdgeFlags({ migrating: false, holding: true, fadingOut: false })
          if (pair) {
            setNodeHighlight([pair.from, pair.to], false)
          }
          if (pair) {
            optionsRef.current.onOwnerChanged?.(pair.to)
          }
          const fadeDurationMs = optionsRef.current.fadeDurationMs ?? DEFAULT_FADE_DURATION_MS
          const holdDurationMs = optionsRef.current.holdDurationMs ?? DEFAULT_HOLD_DURATION_MS
          const timer = setTimeout(() => {
            setEdgeFlags({ holding: false, fadingOut: true })
            const fadeTimer = setTimeout(() => {
              setEdgeFlags({ fadingOut: false })
              fadeTimers.delete(event.migration_id)
            }, fadeDurationMs)
            fadeTimers.set(event.migration_id, fadeTimer)
          }, holdDurationMs)
          fadeTimers.set(event.migration_id, timer)
          break
        }
        case 'migration_failed': {
          const pair = resolvePair(event)
          pairByMigrationId.delete(event.migration_id)
          clearFadeTimer(event.migration_id)
          setEdgeFlags({ migrating: false, holding: true, fadingOut: false })
          if (pair) {
            setNodeHighlight([pair.from, pair.to], false)
          }
          const failHold = setTimeout(() => {
            setEdgeFlags({ holding: false, fadingOut: true })
            const failFade = setTimeout(() => {
              setEdgeFlags({ fadingOut: false })
              fadeTimers.delete(event.migration_id)
            }, optionsRef.current.fadeDurationMs ?? DEFAULT_FADE_DURATION_MS)
            fadeTimers.set(event.migration_id, failFade)
          }, optionsRef.current.holdDurationMs ?? DEFAULT_HOLD_DURATION_MS)
          fadeTimers.set(event.migration_id, failHold)
          break
        }
        case 'migration_progress': {
          break
        }
      }
    }

    const source = new EventSource(url)
    source.onmessage = (message: MessageEvent<string>) => {
      let parsed: MigrationEvent
      try {
        parsed = JSON.parse(message.data) as MigrationEvent
      } catch {
        return
      }
      handleEvent(parsed)
    }

    return () => {
      source.close()
      for (const timer of fadeTimers.values()) {
        clearTimeout(timer)
      }
      fadeTimers.clear()
      pairByMigrationId.clear()
    }
  }, [url])
}
