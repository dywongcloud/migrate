import { Handle, Position, type NodeProps, type Node } from '@xyflow/react'
import type { MicroVMNodeData } from './types'
import { useVncScreen } from './useVncScreen'
import './MicroVMNode.css'

const VNC_GATEWAY_BASE =
  import.meta.env.VITE_VNC_GATEWAY ?? 'ws://127.0.0.1:8088'

export function MicroVMNode({ id, data }: NodeProps<Node<MicroVMNodeData>>) {
  const { containerRef, status: vncStatus } = useVncScreen(
    id,
    data.vncNodeId,
    VNC_GATEWAY_BASE,
  )

  const className = data.migrationHighlight
    ? 'microvm-node microvm-node-highlight'
    : 'microvm-node'

  return (
    <div className={className}>
      <Handle type="target" position={Position.Left} />
      <div className="microvm-node-header">
        <span className="microvm-node-label">{data.label}</span>
        <span className={`microvm-node-status microvm-node-status-${data.status}`}>
          {data.status}
        </span>
      </div>
      <div
        className="microvm-node-screen"
        data-vnc-node-id={data.vncNodeId}
        data-vnc-status={vncStatus}
        ref={containerRef}
      >
        {vncStatus === 'connected' ? null : (
          <span className="microvm-node-screen-placeholder">
            {data.vncNodeId ? vncStatus : 'no vnc node id'}
          </span>
        )}
      </div>
      <Handle type="source" position={Position.Right} />
    </div>
  )
}
