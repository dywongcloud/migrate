import { Handle, Position, type NodeProps, type Node } from '@xyflow/react'
import type { DesktopNodeData } from './types'
import { useVncScreen } from './useVncScreen'
import { MonitorIcon } from './icons'
import './turbo.css'
import './DesktopNode.css'

const VNC_GATEWAY_BASE =
  import.meta.env.VITE_VNC_GATEWAY ?? 'ws://127.0.0.1:8088'

export function DesktopNode({ id, data }: NodeProps<Node<DesktopNodeData>>) {
  const { containerRef, status, focus } = useVncScreen(
    id,
    data.vncNodeId,
    VNC_GATEWAY_BASE,
  )

  return (
    <div className="turbo-wrapper desktop-node">
      <Handle type="target" position={Position.Top} />
      <div className="turbo-inner desktop-inner">
        <div className="turbo-head desktop-drag-handle">
          <span className="turbo-icon desktop-icon">
            <MonitorIcon />
          </span>
          <span className="turbo-heading">
            <span className="turbo-title">{data.label}</span>
            <span className="turbo-subtitle">
              {data.vncNodeId ? `${data.vncNodeId.slice(0, 12)}...` : 'no vnc endpoint'}
            </span>
          </span>
          <span className={`turbo-badge turbo-badge-${status}`}>{status}</span>
        </div>
        <div
          className="desktop-node-screen nodrag nopan nowheel"
          data-vnc-node-id={data.vncNodeId}
          data-vnc-status={status}
          ref={containerRef}
          onMouseDown={focus}
          onClick={focus}
        >
          {status === 'connected' ? null : (
            <span className="desktop-node-placeholder">
              {data.vncNodeId ? status : 'no vnc node id'}
            </span>
          )}
        </div>
      </div>
    </div>
  )
}
