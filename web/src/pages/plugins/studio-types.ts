export interface StudioDiagnostic {
  file: string;
  line: number;
  column: number;
  text: string;
  severity: "error" | "warning";
}

export interface StudioTemplate {
  id: string;
  label: string;
  summary: string;
  kind: string;
  capabilities: string[];
  defaultPluginId: string;
  defaultName: string;
  defaultRoute: string;
  defaultPage?: string;
  supportsStremio?: boolean;
  supportsSlot?: boolean;
  files: Record<string, string>;
}

export interface StudioProject {
  id: string;
  template: string;
  name: string;
  pluginId: string;
  slot?: string;
  stremioUrl?: string;
  files: Record<string, string>;
  shadow: boolean;
  settings?: Record<string, unknown>;
  diagnostics: StudioDiagnostic[];
  defaultPage?: string;
  defaultRoute?: string;
  createdAt: string;
  updatedAt: string;
}

export interface StudioProjectSummary {
  id: string;
  template: string;
  name: string;
  pluginId: string;
  slot?: string;
  shadow: boolean;
  errors: number;
  updatedAt: string;
}

export interface StudioCompileResult {
  ok: boolean;
  diagnostics: StudioDiagnostic[];
  jsSize?: number;
  sourcemapSize?: number;
}

export interface StudioCheckItem {
  id: string;
  category: string;
  label: string;
  status: "pass" | "fail" | "warn";
  message?: string;
  fix?: string;
}

export interface StudioCheckReport {
  ok: boolean;
  checks: StudioCheckItem[];
  diagnostics: StudioDiagnostic[];
}

export interface StudioCallRecord {
  id: number;
  plugin: string;
  route: string;
  method: string;
  status: number;
  ms: number;
  error?: string;
  request?: string;
  response?: string;
  created: string;
}

export interface StudioValidationHint {
  callId?: number;
  route: string;
  severity: "warning" | "error" | "info";
  message: string;
  suggestedFix: string;
  field?: string;
}

export interface StudioJournalEntry {
  id: number;
  plugin: string;
  op: string;
  detail: string;
  inverse?: unknown;
  shadow: boolean;
  undone: boolean;
  created: string;
}

export interface StudioRunResponse {
  status: number;
  headers?: Record<string, string>;
  body?: unknown;
  ms: number;
  units: number;
  logs: string[];
  call: StudioCallRecord;
  hints: StudioValidationHint[];
  journal: StudioJournalEntry[];
}

export interface StudioPreviewResponse {
  kind: "view" | "route";
  route: string;
  page?: string;
  title?: string;
  spec?: unknown;
  data?: Record<string, unknown>;
  body?: unknown;
  valid: boolean;
  errors?: string[];
  hints?: StudioValidationHint[];
  ms: number;
}

export interface StudioInstallPreview {
  manifest: {
    id: string;
    version: string;
    name: string;
    description?: string;
    ether?: {
      kind: string;
      summary: string;
      capabilities?: string[];
      permissions?: {
        network?: string[];
        catalog?: string;
        collections?: string;
        folders?: string;
        secrets?: string[];
      };
    };
  };
  permissionLines: string[];
  needsViewers: boolean;
  needsFolders: boolean;
  verified: boolean;
}
