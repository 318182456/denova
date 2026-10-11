/** Public browser protocol. No host components or management endpoints. */
export interface Connection { baseUrl: string; token: string; consumerId?: string }
export interface Context {
  source: { package: { kind: 'plugin' | 'game'; id: string }; releaseId: string };
  scope: { kind: string; projectId?: string; sessionId?: string; instanceId?: string; storyId?: string; branchId?: string };
  locale: 'zh-CN' | 'en-US'; theme: 'light' | 'dark'; visible?: boolean;
  environment: 'installed' | 'preview'; settings: Record<string, unknown>; setup?: Record<string, unknown>;
}
export interface CapabilityState { implemented: boolean; granted: boolean; applicable: boolean; configured: boolean }
export interface Capabilities {
  apiMajor: number; hostVersion: string; permissions: string[];
  capabilities: Record<string, CapabilityState>; models: Record<string, boolean>;
  limits: Record<string, number>; schemaDialect: string;
}
export interface Client {
  context: Context;
  setContext(value: unknown): void;
  /** Display-only metadata. Suggestions fill the composer; actions emit
   * denova:page-action with detail.id inside this frame. Limits: 4 actions,
   * 4 suggestions, labels/title <= 200 chars, prompts <= 2000 chars, 8 KiB total. */
  setPresentation(value: { title?: string; contextLabel?: string; referenceCount?: number; suggestions?: { label: string; prompt: string }[]; actions?: { id: string; label: string }[] }): void;
  setState(state: { busy?: boolean; dirty?: boolean }): void;
  exit(): void;
  openInstance(instanceId: string): void;
  request(path: string, options: RequestInit & { responseType: 'stream' }): Promise<Response>;
  request<T = unknown>(path: string, options?: RequestInit): Promise<T>;
}
export function connect(options?: { prepareExit?: () => boolean | void | Promise<boolean | void> }): Promise<Client>;
