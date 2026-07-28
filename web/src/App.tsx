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
import { MigrateButton, DEFAULT_API_BASE } from './MigrateButton'
import './App.toolbar.css'

const HOSTS_URL = `${DEFAULT_API_BASE}/v1/hosts`
const GUEST_URL = `${DEFAULT_API_BASE}/v1/migrations/guest`
const CURRENT_HOST_URL = `${DEFAULT_API_BASE}/v1/migrations/current-host`
const MIGRATION_EDGE_ID = 'host-a-host-b'
const HOST_A = 'host-a'
const HOST_B = 'host-b'
const HOST_NODE_IDS: Record<string, string> = {
  [HOST_A]: HOST_A,
  [HOST_B]: HOST_B,
}
const DESKTOP_BY_HOST: Record<string, string> = {
  [HOST_A]: 'desktop-a',
  [HOST_B]: 'desktop-b',
}
const HOST_LABELS: Record<string, string> = {
  [HOST_A]: 'host A',
  [HOST_B]: 'host B',
}

interface HostRegistryEntry {
  id: string
  node_id: string
}

interface GuestResponse {
  host?: string
}

declare global {
  interface Window {
    __applyNodeChanges?: (changes: NodeChange<GraphNode>[]) => void
    __desktopOwner?: { host: string; desktop: string; vncNodeId: string; source: string }
  }
}

const nodeTypes: NodeTypes = { microvm: MicroVMNode, desktop: DesktopNode }
const edgeTypes: EdgeTypes = { migration: MigrationEdge }

type GraphNode = Node<HostNodeData> | Node<DesktopNodeData>

const initialNodes: GraphNode[] = [
  {
    id: HOST_A,
    type: 'microvm',
    position: { x: 40, y: 0 },
    data: {
      id: HOST_A,
      label: 'Host A',
      hostAddr: '',
      status: 'running',
      migrationHighlight: false,
    },
  },
  {
    id: HOST_B,
    type: 'microvm',
    position: { x: 800, y: 0 },
    data: {
      id: HOST_B,
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
      label: '',
      hostId: HOST_A,
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
      label: '',
      hostId: HOST_B,
      vncNodeId: '',
    },
  },
]

const initialEdges: Edge<MigrationEdgeData>[] = [
  {
    id: MIGRATION_EDGE_ID,
    type: 'migration',
    source: HOST_A,
    target: HOST_B,
    hidden: true,
    data: {
      id: MIGRATION_EDGE_ID,
      source: HOST_A,
      target: HOST_B,
      migrating: false,
      holding: false,
      fadingOut: false,
      httpLabel: '',
      httpOk: true,
    },
  },
]

function isDesktopNode(node: GraphNode): node is Node<DesktopNodeData> {
  return node.type === 'desktop'
}

function isKnownHost(value: unknown): value is string {
  return value === HOST_A || value === HOST_B
}

function otherHost(host: string): string {
  return host === HOST_A ? HOST_B : HOST_A
}

function urlVncNodeId(): string {
  const params = new URLSearchParams(window.location.search)
  return params.get('vnc') ?? params.get('nodeA') ?? ''
}

function urlOwnerHost(): string {
  const params = new URLSearchParams(window.location.search)
  const owner = params.get('owner')
  return isKnownHost(owner) ? owner : ''
}

function desktopLabel(nodeHostId: string, ownerHost: string): string {
  if (nodeHostId === ownerHost) {
    return `XFCE desktop live on ${HOST_LABELS[nodeHostId]} -- VNC over iroh`
  }
  return `no guest on ${HOST_LABELS[nodeHostId]}`
}

function desktopEdgesFor(ownerHost: string): Edge[] {
  const desktopId = DESKTOP_BY_HOST[ownerHost]
  if (!desktopId) {
    return []
  }
  return [
    {
      id: `${ownerHost}-${desktopId}`,
      source: ownerHost,
      sourceHandle: 'desktop',
      target: desktopId,
      label: 'VNC 5901',
      style: { stroke: '#3f4c5a' },
    },
  ]
}

function App() {
  const paramVncNodeId = urlVncNodeId()
  const paramOwnerHost = urlOwnerHost()
  const [nodes, setNodes] = useState<GraphNode[]>(initialNodes)
  const [edges, setEdges] = useState<Edge<MigrationEdgeData>[]>(initialEdges)
  const [ownerHost, setOwnerHost] = useState<string>(paramOwnerHost || HOST_A)
  const [ownerSource, setOwnerSource] = useState<string>(
    paramOwnerHost ? 'owner url param' : 'default, server not read yet',
  )
  const [registry, setRegistry] = useState<HostRegistryEntry[]>([])
  const [inFlight, setInFlight] = useState<boolean>(false)

  const registryNodeId = registry.find((entry) => entry.id === ownerHost)?.node_id ?? ''
  const guestVncNodeId = paramVncNodeId || registryNodeId
  const ownerDesktopId = DESKTOP_BY_HOST[ownerHost] ?? ''

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

  const adoptOwner = (host: string, source: string) => {
    if (!isKnownHost(host)) {
      return
    }
    setOwnerHost(host)
    setOwnerSource(source)
  }

  useEffect(() => {
    let cancelled = false

    async function readOwner(url: string, source: string): Promise<boolean> {
      try {
        const response = await fetch(url)
        if (!response.ok) {
          return false
        }
        const body = (await response.json()) as GuestResponse
        if (!isKnownHost(body.host)) {
          return false
        }
        if (!cancelled) {
          adoptOwner(body.host, source)
        }
        return true
      } catch {
        return false
      }
    }

    async function seedOwner() {
      if (await readOwner(GUEST_URL, 'GET /v1/migrations/guest')) {
        return
      }
      if (await readOwner(CURRENT_HOST_URL, 'GET /v1/migrations/current-host')) {
        return
      }
      if (!cancelled) {
        setOwnerSource(
          paramOwnerHost
            ? 'owner url param, server reports no persistent guest'
            : 'default host-a, server reports no persistent guest',
        )
      }
    }

    async function loadRegistry() {
      try {
        const response = await fetch(HOSTS_URL)
        if (!response.ok) {
          return
        }
        const entries = (await response.json()) as HostRegistryEntry[]
        if (!cancelled && Array.isArray(entries)) {
          setRegistry(entries)
        }
      } catch {
        return
      }
    }

    void seedOwner()
    void loadRegistry()

    return () => {
      cancelled = true
    }
  }, [paramOwnerHost])

  useMigrationEvents({
    setNodes: setNodes as React.Dispatch<React.SetStateAction<Node<HostNodeData>[]>>,
    setEdges,
    edgeId: MIGRATION_EDGE_ID,
    hostNodeIds: HOST_NODE_IDS,
    onOwnerChanged: (toHostId) => adoptOwner(toHostId, 'migration_complete destination'),
    onInFlightChanged: setInFlight,
  })

  const renderedNodes: GraphNode[] = nodes.map((node) => {
    if (!isDesktopNode(node)) {
      return node
    }
    const owns = node.id === ownerDesktopId
    const vncNodeId = owns ? guestVncNodeId : ''
    const label = desktopLabel(node.data.hostId, ownerHost)
    if (node.data.vncNodeId === vncNodeId && node.data.label === label) {
      return node
    }
    return { ...node, data: { ...node.data, vncNodeId, label } }
  })

  window.__desktopOwner = {
    host: ownerHost,
    desktop: ownerDesktopId,
    vncNodeId: guestVncNodeId,
    source: ownerSource,
  }

  return (
    <div style={{ width: '100vw', height: '100vh', display: 'flex', flexDirection: 'column' }}>
      <div className="app-toolbar">
        <MigrateButton
          currentHost={ownerHost}
          nextHost={otherHost(ownerHost)}
          inFlight={inFlight}
          onHttpStatus={setHttpLabel}
          onServerOwner={(host) => adoptOwner(host, 'POST /v1/migrations current_host')}
        />
        <span className="app-hosts-status" data-owner-host={ownerHost} data-owner-source={ownerSource}>
          {`guest on ${ownerHost} (${ownerSource})`}
        </span>
      </div>
      <div style={{ flex: 1, minHeight: 0 }}>
        <ReactFlow
          nodes={renderedNodes}
          edges={[...desktopEdgesFor(ownerHost), ...edges] as Edge[]}
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
