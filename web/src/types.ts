export interface HostNodeData {
  id: string;
  label: string;
  hostAddr: string;
  status: 'running' | 'migrating' | 'idle';
  migrationHighlight: boolean;
  [key: string]: unknown;
}

export interface DesktopNodeData {
  id: string;
  label: string;
  hostId: string;
  vncNodeId: string;
  [key: string]: unknown;
}

export type MicroVMNodeData = HostNodeData;

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
