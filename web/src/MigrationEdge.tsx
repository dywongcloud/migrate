import {
  BaseEdge,
  EdgeLabelRenderer,
  getBezierPath,
  type EdgeProps,
  type Edge,
} from '@xyflow/react'
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
  const [path, labelX, labelY] = getBezierPath({
    sourceX,
    sourceY,
    sourcePosition,
    targetX,
    targetY,
    targetPosition,
  })

  const migrating = data?.migrating ?? false
  const holding = data?.holding ?? false
  const fadingOut = data?.fadingOut ?? false
  const httpLabel = data?.httpLabel ?? ''
  const httpOk = data?.httpOk ?? true

  const className = migrating
    ? 'migration-edge migration-edge-active'
    : holding
      ? 'migration-edge migration-edge-holding'
      : fadingOut
        ? 'migration-edge migration-edge-fading'
        : 'migration-edge'

  const labelVisible = (migrating || holding || fadingOut) && httpLabel.length > 0
  const labelClass = httpOk
    ? 'migration-edge-label migration-edge-label-ok'
    : 'migration-edge-label migration-edge-label-err'

  return (
    <>
      <BaseEdge id={id} path={path} className={className} />
      {labelVisible ? (
        <EdgeLabelRenderer>
          <div
            className={fadingOut ? `${labelClass} migration-edge-label-fading` : labelClass}
            style={{ transform: `translate(-50%, -50%) translate(${labelX}px, ${labelY}px)` }}
          >
            {`--- ${httpLabel} ---`}
          </div>
        </EdgeLabelRenderer>
      ) : null}
    </>
  )
}
