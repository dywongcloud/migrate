export interface MigrateButtonProps {
  currentHost: string
  nextHost: string
  inFlight: boolean
  apiBase?: string
  onHttpStatus?: (label: string, ok: boolean) => void
  onServerOwner?: (host: string) => void
}

interface MigrateResponse {
  current_host?: string
  blackout_ms?: number
  guest_mem_mib?: number
}

export const DEFAULT_API_BASE = 'http://localhost:7040'

export function MigrateButton({
  currentHost,
  nextHost,
  inFlight,
  apiBase = DEFAULT_API_BASE,
  onHttpStatus,
  onServerOwner,
}: MigrateButtonProps) {
  async function handleClick() {
    onHttpStatus?.('HTTP ...', true)
    let response: Response
    try {
      response = await fetch(`${apiBase}/v1/migrations`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: '{}',
      })
    } catch {
      onHttpStatus?.('HTTP ERR', false)
      return
    }
    onHttpStatus?.(`HTTP ${response.status}`, response.ok)
    if (!response.ok) {
      return
    }
    let body: MigrateResponse
    try {
      body = (await response.json()) as MigrateResponse
    } catch {
      return
    }
    if (typeof body.current_host === 'string' && body.current_host !== '') {
      onServerOwner?.(body.current_host)
    }
  }

  return (
    <button type="button" onClick={() => void handleClick()} disabled={inFlight}>
      {inFlight ? 'Migrating...' : `Migrate (${currentHost} -> ${nextHost})`}
    </button>
  )
}
