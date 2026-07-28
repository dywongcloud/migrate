import { useEffect, useState } from 'react'
import {
  ReactFlow,
  Background,
  Controls,
  applyNodeChanges,
  type Node,
  type Edge,
  type NodeTypes,
  type EdgeTypes,
  type NodeChange,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import type { HostNodeData, DesktopNodeData, MigrationEdgeData } from './types'
import { MicroVMNode } from './MicroVMNode'
import { DesktopNode } from './DesktopNode'
import { GroupNode } from './GroupNode'
import { ComponentNode } from './ComponentNode'
import { MigrationEdge } from './MigrationEdge'
import { useMigrationEvents } from './useMigrationEvents'
import { MigrateButton } from './MigrateButton'
import {
  buildNodes,
  buildComponentEdges,
  buildMigrationEdges,
  MIGRATION_EDGE_ID,
  HOST_NODE_IDS,
  GROUP_BY_HOST,
  DESKTOP_BY_HOST,
  VNC_PARAM_BY_DESKTOP,
  type GraphNode,
} from './graph'
import './App.toolbar.css'

const HOSTS_URL = 'http://localhost:7040/v1/hosts'

interface HostRegistryEntry {
  id: string
  node_id: string
}

type HostsStatus = 'loading' | 'ready' | 'unreachable'

declare global {
  interface Window {
    __applyNodeChanges?: (changes: NodeChange<GraphNode>[]) => void
  }
}

const nodeTypes: NodeTypes = {
  microvm: MicroVMNode,
  desktop: DesktopNode,
  vmgroup: GroupNode,
  component: ComponentNode,
}
const edgeTypes: EdgeTypes = { migration: MigrationEdge }

const componentEdges = buildComponentEdges()

function isDesktopNode(node: GraphNode): node is Node<DesktopNodeData> {
  return node.type === 'desktop'
}

function nodesWithUrlOverrides(base: GraphNode[]): { nodes: GraphNode[]; overridden: boolean } {
  const params = new URLSearchParams(window.location.search)
  let overridden = false
  const nodes: GraphNode[] = base.map((node) => {
    if (!isDesktopNode(node)) {
      return node
    }
    const value = params.get(VNC_PARAM_BY_DESKTOP[node.id])
    if (!value) {
      return node
    }
    overridden = true
    return { ...node, data: { ...node.data, vncNodeId: value } }
  })
  return { nodes, overridden }
}

function statusLabel(status: HostsStatus): string {
  if (status === 'loading') {
    return 'loading host registry...'
  }
  if (status === 'unreachable') {
    return 'host registry unreachable, using URL overrides'
  }
  return 'host registry loaded'
}

function App() {
  const initial = nodesWithUrlOverrides(buildNodes())
  const [nodes, setNodes] = useState<GraphNode[]>(initial.nodes)
  const [edges, setEdges] = useState<Edge<MigrationEdgeData>[]>(buildMigrationEdges())
  const [hostsStatus, setHostsStatus] = useState<HostsStatus>(
    initial.overridden ? 'ready' : 'loading',
  )

  const onNodesChange = (changes: NodeChange<GraphNode>[]) => {
    setNodes((prevNodes) => applyNodeChanges(changes, prevNodes))
  }
  window.__applyNodeChanges = onNodesChange

  const setHttpLabel = (label: string, ok: boolean) => {
    setEdges((prevEdges) =>
      prevEdges.map((edge) =>
        edge.id === MIGRATION_EDGE_ID && edge.data
          ? { ...edge, data: { ...edge.data, httpLabel: label, httpOk: ok } }
          : edge,
      ),
    )
  }

  useEffect(() => {
    let cancelled = false
    if (initial.overridden) {
      return
    }

    async function loadHosts() {
      try {
        const response = await fetch(HOSTS_URL)
        if (!response.ok) {
          if (!cancelled) {
            setHostsStatus('unreachable')
          }
          return
        }
        const entries = (await response.json()) as HostRegistryEntry[]
        if (cancelled) {
          return
        }
        setNodes((prevNodes) =>
          prevNodes.map<GraphNode>((node) => {
            if (!isDesktopNode(node)) {
              return node
            }
            const entry = entries.find(
              (candidate) => DESKTOP_BY_HOST[candidate.id] === node.id,
            )
            return entry ? { ...node, data: { ...node.data, vncNodeId: entry.node_id } } : node
          }),
        )
        setHostsStatus('ready')
      } catch {
        if (!cancelled) {
          setHostsStatus('unreachable')
        }
      }
    }

    void loadHosts()

    return () => {
      cancelled = true
    }
  }, [])

  useMigrationEvents({
    setNodes: setNodes as React.Dispatch<React.SetStateAction<Node<HostNodeData>[]>>,
    setEdges,
    edgeId: MIGRATION_EDGE_ID,
    hostNodeIds: HOST_NODE_IDS,
    groupNodeIds: GROUP_BY_HOST,
  })

  return (
    <div style={{ width: '100vw', height: '100vh', display: 'flex', flexDirection: 'column' }}>
      <div className="app-toolbar">
        <MigrateButton hostA="host-a" hostB="host-b" onHttpStatus={setHttpLabel} />
        <span className="app-hosts-status">{statusLabel(hostsStatus)}</span>
      </div>
      <div style={{ flex: 1, minHeight: 0 }}>
        <ReactFlow
          nodes={nodes}
          edges={[...componentEdges, ...edges] as Edge[]}
          onNodesChange={onNodesChange}
          nodeTypes={nodeTypes}
          edgeTypes={edgeTypes}
          nodesDraggable
          minZoom={0.15}
          fitView
        >
          <Background />
          <Controls />
        </ReactFlow>
      </div>
    </div>
  )
}

export default App
