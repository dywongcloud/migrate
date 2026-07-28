import type { Node, Edge } from '@xyflow/react'
import type {
  HostNodeData,
  DesktopNodeData,
  GroupNodeData,
  ComponentNodeData,
  ComponentKind,
  MigrationEdgeData,
} from './types'

export type GraphNode =
  | Node<HostNodeData>
  | Node<DesktopNodeData>
  | Node<GroupNodeData>
  | Node<ComponentNodeData>

export const MIGRATION_EDGE_ID = 'host-a-host-b'

export const HOST_NODE_IDS: Record<string, string> = {
  'host-a': 'host-a',
  'host-b': 'host-b',
}

export const GROUP_BY_HOST: Record<string, string> = {
  'host-a': 'host-a-group',
  'host-b': 'host-b-group',
}

export const DESKTOP_BY_HOST: Record<string, string> = {
  'host-a': 'desktop-a',
  'host-b': 'desktop-b',
}

export const VNC_PARAM_BY_DESKTOP: Record<string, string> = {
  'desktop-a': 'nodeA',
  'desktop-b': 'nodeB',
}

const GAP = 24
const COMPONENT_WIDTH = 258
const COMPONENT_HEIGHT = 44
const HOST_CARD_HEIGHT = 112
const GROUP_HEADER_HEIGHT = 54

const COL_A = GAP
const COL_B = GAP + COMPONENT_WIDTH + GAP
const GROUP_WIDTH = COL_B + COMPONENT_WIDTH + GAP
const HOST_CARD_Y = GROUP_HEADER_HEIGHT + GAP
const COMPONENT_TOP = HOST_CARD_Y + HOST_CARD_HEIGHT + GAP
const COMPONENT_PITCH = COMPONENT_HEIGHT + GAP
const COMPONENT_ROW_COUNT = 4
const GROUP_HEIGHT =
  COMPONENT_TOP + COMPONENT_PITCH * (COMPONENT_ROW_COUNT - 1) + COMPONENT_HEIGHT + GAP
const GROUP_PITCH = GROUP_WIDTH + GAP
const DESKTOP_Y = GROUP_HEIGHT + GAP

interface HostSpec {
  host: string
  label: string
  guestIp: string
  guestMac: string
  tap: string
}

const HOSTS: HostSpec[] = [
  { host: 'host-a', label: 'Host A', guestIp: '172.20.0.3', guestMac: '06:00:AC:14:00:03', tap: 'tap-lm-a' },
  { host: 'host-b', label: 'Host B', guestIp: '172.20.0.4', guestMac: '06:00:AC:14:00:04', tap: 'tap-lm-b' },
]

function componentsFor(spec: HostSpec): Array<{ kind: ComponentKind; label: string; detail: string }> {
  return [
    { kind: 'vmm', label: 'Firecracker VMM', detail: 'firecracker-aarch64' },
    { kind: 'kernel', label: 'Guest kernel', detail: 'vmlinux-aarch64' },
    { kind: 'rootfs', label: 'Desktop rootfs', detail: 'rootfs-desktop.ext4 ext4' },
    { kind: 'netrootfs', label: 'Net rootfs', detail: 'rootfs.ext4 busybox + beacon' },
    { kind: 'init', label: 'Init', detail: '/sbin/init systemd (busybox /init on net rootfs)' },
    { kind: 'net', label: 'Network', detail: `${spec.guestIp} ${spec.guestMac} on ${spec.tap}` },
    { kind: 'vnc', label: 'VNC server', detail: 'Xtigervnc :1 -> 5901 + XFCE' },
  ]
}

export function buildNodes(): GraphNode[] {
  const nodes: GraphNode[] = []

  HOSTS.forEach((spec, index) => {
    const groupId = GROUP_BY_HOST[spec.host]
    const desktopId = DESKTOP_BY_HOST[spec.host]

    nodes.push({
      id: groupId,
      type: 'vmgroup',
      position: { x: index * GROUP_PITCH, y: 0 },
      style: { width: GROUP_WIDTH, height: GROUP_HEIGHT },
      data: {
        id: groupId,
        label: `${spec.label} microVM`,
        detail: `firecracker + guest ${spec.guestIp}`,
        migrationHighlight: false,
      },
    })

    nodes.push({
      id: spec.host,
      type: 'microvm',
      parentId: groupId,
      extent: 'parent',
      position: { x: COL_A, y: HOST_CARD_Y },
      data: {
        id: spec.host,
        label: spec.label,
        hostAddr: spec.guestIp,
        status: 'running',
        migrationHighlight: false,
      },
    })

    componentsFor(spec).forEach((component, i) => {
      nodes.push({
        id: `${spec.host}-${component.kind}`,
        type: 'component',
        parentId: groupId,
        extent: 'parent',
        position: {
          x: i % 2 === 0 ? COL_A : COL_B,
          y: COMPONENT_TOP + COMPONENT_PITCH * Math.floor(i / 2),
        },
        data: {
          id: `${spec.host}-${component.kind}`,
          kind: component.kind,
          label: component.label,
          detail: component.detail,
        },
      })
    })

    nodes.push({
      id: desktopId,
      type: 'desktop',
      dragHandle: '.desktop-drag-handle',
      position: { x: index * GROUP_PITCH, y: DESKTOP_Y },
      data: {
        id: desktopId,
        label: `XFCE desktop (${spec.label})`,
        hostId: spec.host,
        vncNodeId: '',
      },
    })
  })

  return nodes
}

export function buildComponentEdges(): Edge[] {
  const edges: Edge[] = []
  HOSTS.forEach((spec) => {
    edges.push({
      id: `${spec.host}-to-desktop`,
      source: spec.host,
      sourceHandle: 'desktop',
      target: DESKTOP_BY_HOST[spec.host],
      label: 'VNC over iroh',
      labelStyle: { fill: '#7d8b9b', fontSize: 10 },
      labelBgStyle: { fill: '#0d1117' },
      style: { stroke: '#3f4c5a' },
    })
  })
  return edges
}

export function buildMigrationEdges(): Edge<MigrationEdgeData>[] {
  return [
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
}
