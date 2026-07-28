import { Handle, Position, type NodeProps, type Node } from '@xyflow/react'
import type { HostNodeData } from './types'
import { CpuIcon, BoltIcon } from './icons'
import './turbo.css'
import './MicroVMNode.css'

export function MicroVMNode({ data }: NodeProps<Node<HostNodeData>>) {
  const migrating = data.migrationHighlight
  const wrapperClass = migrating
    ? 'turbo-wrapper turbo-wrapper-migrating microvm-node'
    : 'turbo-wrapper microvm-node'

  return (
    <div className={wrapperClass}>
      <Handle type="target" position={Position.Left} />
      <div className="turbo-inner microvm-inner">
        <div className="turbo-head">
          <span className="turbo-icon">
            <CpuIcon />
          </span>
          <span className="turbo-heading">
            <span className="turbo-title">{data.label}</span>
            <span className="turbo-subtitle">
              {data.hostAddr ? data.hostAddr : 'firecracker microVM'}
            </span>
          </span>
          <span className={`turbo-badge turbo-badge-${data.status}`}>{data.status}</span>
        </div>
        <div className="microvm-stats">
          <span className="microvm-stat">
            <BoltIcon />
            KVM
          </span>
          <span className="microvm-stat">1024 MiB</span>
          <span className="microvm-stat">2 vCPU</span>
        </div>
      </div>
      <Handle type="source" position={Position.Right} />
      <Handle type="source" position={Position.Bottom} id="desktop" />
    </div>
  )
}
