import { Handle, Position, type NodeProps, type Node } from '@xyflow/react'
import type { GroupNodeData } from './types'
import { CpuIcon, BoltIcon } from './icons'
import './turbo.css'
import './GroupNode.css'

export function GroupNode({ data }: NodeProps<Node<GroupNodeData>>) {
  const wrapperClass = data.migrationHighlight
    ? 'turbo-wrapper turbo-wrapper-migrating group-node'
    : 'turbo-wrapper group-node'

  return (
    <div className={wrapperClass}>
      <Handle type="target" position={Position.Left} />
      <div className="turbo-inner group-inner">
        <div className="turbo-head group-head">
          <span className="turbo-icon">
            <CpuIcon />
          </span>
          <span className="turbo-heading">
            <span className="turbo-title">{data.label}</span>
            <span className="turbo-subtitle">{data.detail}</span>
          </span>
          <span className={`turbo-badge turbo-badge-${data.status}`}>{data.status}</span>
        </div>
        <div className="group-stats">
          <span className="group-stat">
            <BoltIcon />
            KVM
          </span>
          <span className="group-stat">1024 MiB</span>
          <span className="group-stat">2 vCPU</span>
          <span className="group-stat">firecracker</span>
        </div>
      </div>
      <Handle type="source" position={Position.Right} />
      <Handle type="source" position={Position.Bottom} id="desktop" />
    </div>
  )
}
