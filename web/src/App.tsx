import { useEffect, useState } from 'react'
import {
  ReactFlow,
  Background,
  Controls,
  type Node,
  type Edge,
  type NodeTypes,
  type EdgeTypes,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import type { MicroVMNodeData, MigrationEdgeData } from './types'
import { MicroVMNode } from './MicroVMNode'
import { MigrationEdge } from './MigrationEdge'
import { useMigrationEvents } from './useMigrationEvents'
import { MigrateButton } from './MigrateButton'
import './App.toolbar.css'

const HOSTS_URL = 'http://localhost:7040/v1/hosts'
const MIGRATION_EDGE_ID = 'host-a-desktop-host-b-desktop'
const HOST_NODE_IDS: Record<string, string> = {
  'host-a': 'host-a-desktop',
  'host-b': 'host-b-desktop',
}

interface HostRegistryEntry {
  id: string
  node_id: string
}

type HostsStatus = 'loading' | 'ready' | 'unreachable'

const nodeTypes: NodeTypes = { microvm: MicroVMNode }
const edgeTypes: EdgeTypes = { migration: MigrationEdge }

const initialNodes: Node<MicroVMNodeData>[] = [
  {
    id: 'host-a-desktop',
    type: 'microvm',
    position: { x: 0, y: 0 },
    data: {
      id: 'host-a-desktop',
      label: 'Host A Desktop',
      hostAddr: '',
      vncNodeId: '',
      status: 'idle',
      migrationHighlight: false,
    },
  },
  {
    id: 'host-b-desktop',
    type: 'microvm',
    position: { x: 400, y: 0 },
    data: {
      id: 'host-b-desktop',
      label: 'Host B Desktop',
      hostAddr: '',
      vncNodeId: '',
      status: 'idle',
      migrationHighlight: false,
    },
  },
]

const initialEdges: Edge<MigrationEdgeData>[] = [
  {
    id: MIGRATION_EDGE_ID,
    type: 'migration',
    source: 'host-a-desktop',
    target: 'host-b-desktop',
    data: {
      id: MIGRATION_EDGE_ID,
      source: 'host-a-desktop',
      target: 'host-b-desktop',
      migrating: false,
      fadingOut: false,
    },
  },
]

function statusLabel(status: HostsStatus): string {
  if (status === 'loading') {
    return 'loading host registry...'
  }
  if (status === 'unreachable') {
    return 'host registry unreachable, using defaults'
  }
  return 'host registry loaded'
}

const NODE_ID_PARAMS: Record<string, string> = {
  'host-a-desktop': 'nodeA',
  'host-b-desktop': 'nodeB',
}

const HOST_IDS = ['host-a', 'host-b']

function readMigratingDesktop(): { vncNodeId: string; owner: string } | null {
  const params = new URLSearchParams(window.location.search)
  const vncNodeId = params.get('vnc')
  if (!vncNodeId) {
    return null
  }
  const requested = params.get('owner')
  const owner = requested && HOST_IDS.includes(requested) ? requested : HOST_IDS[0]
  return { vncNodeId, owner }
}

function assignDesktopToOwner(
  base: Node<MicroVMNodeData>[],
  vncNodeId: string,
  ownerHost: string,
): Node<MicroVMNodeData>[] {
  const ownerNodeId = HOST_NODE_IDS[ownerHost]
  return base.map((node) => ({
    ...node,
    data: {
      ...node.data,
      vncNodeId: node.id === ownerNodeId ? vncNodeId : '',
      status: node.id === ownerNodeId ? 'running' : 'idle',
    },
  }))
}

function nodesWithUrlOverrides(
  base: Node<MicroVMNodeData>[],
): { nodes: Node<MicroVMNodeData>[]; overridden: boolean } {
  const params = new URLSearchParams(window.location.search)
  let overridden = false
  const nodes = base.map((node) => {
    const value = params.get(NODE_ID_PARAMS[node.id])
    if (!value) {
      return node
    }
    overridden = true
    return { ...node, data: { ...node.data, vncNodeId: value } }
  })
  return { nodes, overridden }
}

function App() {
  const migrating = readMigratingDesktop()
  const initial = migrating
    ? {
        nodes: assignDesktopToOwner(initialNodes, migrating.vncNodeId, migrating.owner),
        overridden: true,
      }
    : nodesWithUrlOverrides(initialNodes)
  const [nodes, setNodes] = useState<Node<MicroVMNodeData>[]>(initial.nodes)
  const [edges, setEdges] = useState<Edge<MigrationEdgeData>[]>(initialEdges)
  const [hostsStatus, setHostsStatus] = useState<HostsStatus>(
    initial.overridden ? 'ready' : 'loading',
  )

  const moveDesktopTo = (toHostId: string) => {
    if (!migrating) {
      return
    }
    setNodes((prevNodes) => assignDesktopToOwner(prevNodes, migrating.vncNodeId, toHostId))
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
          prevNodes.map((node) => {
            const entry = entries.find((candidate) => HOST_NODE_IDS[candidate.id] === node.id)
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
    setNodes,
    setEdges,
    edgeId: MIGRATION_EDGE_ID,
    hostNodeIds: HOST_NODE_IDS,
    onOwnerChanged: moveDesktopTo,
  })

  return (
    <div style={{ width: '100vw', height: '100vh', display: 'flex', flexDirection: 'column' }}>
      <div className="app-toolbar">
        <MigrateButton hostA="host-a" hostB="host-b" />
        <span className="app-hosts-status">{statusLabel(hostsStatus)}</span>
      </div>
      <div style={{ flex: 1, minHeight: 0 }}>
        <ReactFlow nodes={nodes} edges={edges} nodeTypes={nodeTypes} edgeTypes={edgeTypes} fitView>
          <Background />
          <Controls />
        </ReactFlow>
      </div>
    </div>
  )
}

export default App
