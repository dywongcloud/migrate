import { useEffect, useRef, useState } from 'react'
import type { MigrationEvent } from './useMigrationEvents'

export interface MigrateButtonProps {
  hostA: string
  hostB: string
  apiBase?: string
}

interface CurrentHostResponse {
  host?: string
}

const DEFAULT_API_BASE = 'http://localhost:7040'

function otherHost(host: string, hostA: string, hostB: string): string {
  return host === hostA ? hostB : hostA
}

export function MigrateButton({ hostA, hostB, apiBase = DEFAULT_API_BASE }: MigrateButtonProps) {
  const [currentHost, setCurrentHost] = useState<string>(hostA)
  const [inFlightCount, setInFlightCount] = useState<number>(0)
  const [isSubmitting, setIsSubmitting] = useState<boolean>(false)
  const pairByMigrationId = useRef(new Map<string, { from: string; to: string }>())

  useEffect(() => {
    let cancelled = false

    async function seedCurrentHost() {
      try {
        const response = await fetch(`${apiBase}/v1/migrations/current-host`)
        if (!response.ok) {
          return
        }
        const body = (await response.json()) as CurrentHostResponse
        if (!cancelled && (body.host === hostA || body.host === hostB)) {
          setCurrentHost(body.host)
        }
      } catch {
        return
      }
    }

    void seedCurrentHost()

    return () => {
      cancelled = true
    }
  }, [apiBase, hostA, hostB])

  useEffect(() => {
    const source = new EventSource(`${apiBase}/v1/migrations/events`)

    source.onmessage = (message: MessageEvent<string>) => {
      let event: MigrationEvent
      try {
        event = JSON.parse(message.data) as MigrationEvent
      } catch {
        return
      }

      if (event.type === 'migration_start') {
        if (event.from && event.to) {
          const touchesThisPair =
            (event.from === hostA || event.from === hostB) &&
            (event.to === hostA || event.to === hostB)
          if (touchesThisPair) {
            pairByMigrationId.current.set(event.migration_id, { from: event.from, to: event.to })
            setInFlightCount((count) => count + 1)
          }
        }
        return
      }

      if (event.type === 'migration_complete') {
        const pair = pairByMigrationId.current.get(event.migration_id)
        if (pair) {
          pairByMigrationId.current.delete(event.migration_id)
          setInFlightCount((count) => Math.max(0, count - 1))
          setCurrentHost(pair.to)
        }
        return
      }

      if (event.type === 'migration_failed') {
        const pair = pairByMigrationId.current.get(event.migration_id)
        if (pair) {
          pairByMigrationId.current.delete(event.migration_id)
          setInFlightCount((count) => Math.max(0, count - 1))
        }
        return
      }
    }

    return () => {
      source.close()
    }
  }, [apiBase, hostA, hostB])

  const inFlight = inFlightCount > 0
  const disabled = inFlight || isSubmitting

  async function handleClick() {
    const from = currentHost
    const to = otherHost(currentHost, hostA, hostB)

    setIsSubmitting(true)
    try {
      await fetch(`${apiBase}/v1/migrations`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ from, to }),
      })
    } catch {
      return
    } finally {
      setIsSubmitting(false)
    }
  }

  return (
    <button type="button" onClick={() => void handleClick()} disabled={disabled}>
      {inFlight ? 'Migrating...' : `Migrate (${currentHost} -> ${otherHost(currentHost, hostA, hostB)})`}
    </button>
  )
}
