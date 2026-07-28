export interface GroupNodeData {
  id: string;
  label: string;
  detail: string;
  status: 'running' | 'migrating' | 'idle';
  migrationHighlight: boolean;
  [key: string]: unknown;
}

export type ComponentKind = 'kernel' | 'rootfs' | 'netrootfs' | 'init' | 'net' | 'vnc' | 'vmm';

export interface ComponentNodeData {
  id: string;
  kind: ComponentKind;
  label: string;
  detail: string;
  [key: string]: unknown;
}

export interface DesktopNodeData {
  id: string;
  label: string;
  hostId: string;
  vncNodeId: string;
  [key: string]: unknown;
}

export interface MigrationEdgeData {
  id: string;
  source: string;
  target: string;
  migrating: boolean;
  holding: boolean;
  fadingOut: boolean;
  httpLabel: string;
  httpOk: boolean;
  [key: string]: unknown;
}
