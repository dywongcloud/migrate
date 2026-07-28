import { Handle, Position, type NodeProps, type Node } from '@xyflow/react'
import type { DesktopNodeData } from './types'
import { useVncScreen } from './useVncScreen'
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
    <div className="desktop-node">
      <Handle type="target" position={Position.Left} />
      <div className="desktop-node-header">
        <span className="desktop-node-label">{data.label}</span>
        <span className={`desktop-node-status desktop-node-status-${status}`}>
          {status}
        </span>
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
      <div className="desktop-node-hint">click to type; keyboard and mouse go to the guest</div>
    </div>
  )
}
