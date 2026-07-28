import { type NodeProps, type Node } from '@xyflow/react'
import type { ComponentNodeData, ComponentKind } from './types'
import './ComponentNode.css'

const GLYPH: Record<ComponentKind, string> = {
  kernel: 'K',
  rootfs: 'R',
  netrootfs: 'N',
  init: 'I',
  net: 'E',
  vnc: 'V',
  vmm: 'F',
}

export function ComponentNode({ data }: NodeProps<Node<ComponentNodeData>>) {
  return (
    <div className={`component-node component-node-${data.kind}`}>
      <span className="component-glyph">{GLYPH[data.kind]}</span>
      <span className="component-text">
        <span className="component-label">{data.label}</span>
        <span className="component-detail">{data.detail}</span>
      </span>
    </div>
  )
}
