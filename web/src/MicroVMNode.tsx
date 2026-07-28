import { Handle, Position, type NodeProps, type Node } from '@xyflow/react'
import type { HostNodeData } from './types'
import './MicroVMNode.css'

export function MicroVMNode({ data }: NodeProps<Node<HostNodeData>>) {
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
      <div className="microvm-node-meta">
        <span className="microvm-node-kind">Firecracker microVM</span>
        {data.hostAddr ? <span className="microvm-node-addr">{data.hostAddr}</span> : null}
      </div>
      <Handle type="source" position={Position.Right} />
      <Handle type="source" position={Position.Bottom} id="desktop" />
    </div>
  )
}
