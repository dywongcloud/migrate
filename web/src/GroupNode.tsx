import { Handle, Position, type NodeProps, type Node } from '@xyflow/react'
import type { GroupNodeData } from './types'
import { CpuIcon } from './icons'
import './turbo.css'
import './GroupNode.css'

export function GroupNode({ data }: NodeProps<Node<GroupNodeData>>) {
  const className = data.migrationHighlight
    ? 'group-node group-node-migrating'
    : 'group-node'

  return (
    <div className={className}>
      <Handle type="target" position={Position.Left} />
      <div className="group-node-header">
        <span className="group-node-icon">
          <CpuIcon />
        </span>
        <span className="group-node-heading">
          <span className="group-node-title">{data.label}</span>
          <span className="group-node-detail">{data.detail}</span>
        </span>
      </div>
      <Handle type="source" position={Position.Right} />
    </div>
  )
}
