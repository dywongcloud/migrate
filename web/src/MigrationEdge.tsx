import { BaseEdge, getBezierPath, type EdgeProps, type Edge } from '@xyflow/react'
import type { MigrationEdgeData } from './types'
import './MigrationEdge.css'

export function MigrationEdge({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  data,
}: EdgeProps<Edge<MigrationEdgeData>>) {
  const [path] = getBezierPath({
    sourceX,
    sourceY,
    sourcePosition,
    targetX,
    targetY,
    targetPosition,
  })

  const migrating = data?.migrating ?? false
  const fadingOut = data?.fadingOut ?? false
  const className = migrating
    ? 'migration-edge migration-edge-active'
    : fadingOut
      ? 'migration-edge migration-edge-fading'
      : 'migration-edge'

  return <BaseEdge id={id} path={path} className={className} />
}
