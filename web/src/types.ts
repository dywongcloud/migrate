export interface MicroVMNodeData {
  id: string;
  label: string;
  hostAddr: string;
  vncNodeId: string;
  status: 'running' | 'migrating' | 'idle';
  migrationHighlight: boolean;
  [key: string]: unknown;
}

export interface MigrationEdgeData {
  id: string;
  source: string;
  target: string;
  migrating: boolean;
  fadingOut: boolean;
  [key: string]: unknown;
}
