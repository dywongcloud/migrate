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
import { MigrationEdge } from './MigrationEdge'
import { useMigrationEvents } from './useMigrationEvents'
import { MigrateButton } from './MigrateButton'
import './App.toolbar.css'

const HOSTS_URL = 'http://localhost:7040/v1/hosts'
const MIGRATION_EDGE_ID = 'host-a-host-b'
const HOST_NODE_IDS: Record<string, string> = {
  'host-a': 'host-a',
  'host-b': 'host-b',
}
const DESKTOP_BY_HOST: Record<string, string> = {
  'host-a': 'desktop-a',
  'host-b': 'desktop-b',
}
const VNC_PARAM_BY_DESKTOP: Record<string, string> = {
  'desktop-a': 'nodeA',
  'desktop-b': 'nodeB',
}

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

const nodeTypes: NodeTypes = { microvm: MicroVMNode, desktop: DesktopNode }
const edgeTypes: EdgeTypes = { migration: MigrationEdge }

type GraphNode = Node<HostNodeData> | Node<DesktopNodeData>

const initialNodes: GraphNode[] = [
  {
    id: 'host-a',
    type: 'microvm',
    position: { x: 40, y: 0 },
    data: {
      id: 'host-a',
      label: 'Host A',
      hostAddr: '',
      status: 'running',
      migrationHighlight: false,
    },
  },
  {
    id: 'host-b',
    type: 'microvm',
    position: { x: 800, y: 0 },
    data: {
      id: 'host-b',
      label: 'Host B',
      hostAddr: '',
      status: 'running',
      migrationHighlight: false,
    },
  },
  {
    id: 'desktop-a',
    type: 'desktop',
    position: { x: 0, y: 200 },
    dragHandle: '.desktop-drag-handle',
    data: {
      id: 'desktop-a',
      label: 'XFCE desktop (host A) -- VNC over iroh',
      hostId: 'host-a',
      vncNodeId: '',
    },
  },
  {
    id: 'desktop-b',
    type: 'desktop',
    position: { x: 700, y: 200 },
    dragHandle: '.desktop-drag-handle',
    data: {
      id: 'desktop-b',
      label: 'XFCE desktop (host B) -- VNC over iroh',
      hostId: 'host-b',
      vncNodeId: '',
    },
  },
]

const initialEdges: Edge<MigrationEdgeData>[] = [
  {
    id: MIGRATION_EDGE_ID,
    type: 'migration',
    source: 'host-a',
    target: 'host-b',
    hidden: true,
    data: {
      id: MIGRATION_EDGE_ID,
      source: 'host-a',
      target: 'host-b',
      migrating: false,
      holding: false,
      fadingOut: false,
      httpLabel: '',
      httpOk: true,
    },
  },
]

const desktopEdges: Edge[] = [
  {
    id: 'host-a-desktop-a',
    source: 'host-a',
    sourceHandle: 'desktop',
    target: 'desktop-a',
    label: 'VNC 5901',
    style: { stroke: '#3f4c5a' },
  },
  {
    id: 'host-b-desktop-b',
    source: 'host-b',
    sourceHandle: 'desktop',
    target: 'desktop-b',
    label: 'VNC 5901',
    style: { stroke: '#3f4c5a' },
  },
]

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
    const paramName = VNC_PARAM_BY_DESKTOP[node.id]
    if (!paramName) {
      return node
    }
    const value = params.get(paramName)
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
  const initial = nodesWithUrlOverrides(initialNodes)
  const [nodes, setNodes] = useState<GraphNode[]>(initial.nodes)
  const [edges, setEdges] = useState<Edge<MigrationEdgeData>[]>(initialEdges)
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
          edges={[...desktopEdges, ...edges] as Edge[]}
          onNodesChange={onNodesChange}
          nodeTypes={nodeTypes}
          edgeTypes={edgeTypes}
          nodesDraggable
          minZoom={0.2}
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
