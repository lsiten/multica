import type { LocalVoiceAPI } from "../shared/local-voice";
import type { VscreenDesktopRequest, VscreenDesktopResult } from "../shared/vscreen-desktop";
import type { RuntimeMirrorWindowRequest } from "../shared/runtime-mirror-window";
import type { RuntimeMirrorPreloadAPI } from "./runtime-mirror";
import { ElectronAPI } from "@electron-toolkit/preload";
import type { RuntimeConfig, RuntimeConfigResult } from "../shared/runtime-config";
import type { NavigationGesture } from "../shared/navigation-gestures";
import type { ContextMenuCommand, ContextMenuRequest } from "../shared/context-menu";
import type { RendererRouteContextInput } from "../shared/renderer-route-context";
import type { FreezeBreadcrumb } from "../shared/freeze-breadcrumb";
import type {
  DesktopWindowContext,
  IssueWindowRequest,
} from "../shared/issue-window";
import type {
  ManualUpdateCheckResult,
  UpdaterPreferences,
} from "../shared/updater-types";
import type {
  DaemonStatus,
  DaemonPrefs,
  LocalRuntimeProbe,
  ManagedWorktree,
  ManagedWorktreeCleanupResult,
  WorktreeCacheResult,
  WorktreeCacheSelection,
  DaemonMcpReadiness,
  DaemonWorkerProcess,
  DaemonProcessOverviewResult,
} from "../shared/daemon-types";
import type { TabSelectionShortcutKey } from "../shared/main-renderer-messages";
import type {
  ThemeFolderFile,
  ThemeFolderSnapshot,
  ThemeFolderWriteResult,
} from "../shared/theme-folder";
import type {
  WorkspacePathOptions,
  WorkspacePathRequest,
} from "../shared/workspace-window";

interface DesktopAPI {
  getSavedRuntimeConfig: () => RuntimeConfigResult;
  saveRuntimeConfig: (config: RuntimeConfig) => Promise<RuntimeConfig>;
  pickAppIcon: () => Promise<string | null>;
  restartApp: () => Promise<void>;
  vscreenDesktop: (request: VscreenDesktopRequest) => Promise<VscreenDesktopResult>;
  openRuntimeMirror: (request: RuntimeMirrorWindowRequest) => Promise<boolean>;
  /** App version + normalized OS, captured synchronously at preload time. */
  appInfo: {
    version: string;
    os: "macos" | "windows" | "linux" | "unknown";
  };
  /** OS-preferred locale (BCP 47) injected by main via additionalArguments. */
  systemLocale: string;
  /** Subscribe to OS language changes detected after boot. Returns an unsubscribe function. */
  onSystemLocaleChanged: (callback: (locale: string) => void) => () => void;
  /** Validated runtime endpoint config, or a blocking config error. */
  runtimeConfig: RuntimeConfigResult;
  /** Main tabbed window or a dedicated issue-only window. */
  windowContext: DesktopWindowContext;
  /** Read any freeze/crash breadcrumb from a previous session, so the renderer
   *  can flush it to telemetry on boot. Null when nothing's pending. Reading
   *  does not consume it — acknowledge with `ackFreeze`. */
  getLastFreeze: () => FreezeBreadcrumb | null;
  /** Retire the breadcrumb with this exact timestamp once its event has been
   *  handed to analytics. Unacknowledged breadcrumbs are retried next boot. */
  ackFreeze: (ts: number) => void;
  /** Report the resolved account identity so stale issue windows can close. */
  reportAuthSession: (userId: string | null) => void;
  /** Listen for auth token delivered via deep link. Returns an unsubscribe function. */
  onAuthToken: (callback: (token: string) => void) => () => void;
  /** Listen for invitation IDs delivered via deep link. Returns an unsubscribe function. */
  onInviteOpen: (callback: (invitationId: string) => void) => () => void;
  /** Listen for issue links from the web once authentication is ready. */
  onIssueOpen: (callback: (url: string) => void) => () => void;
  /** Open a URL in the default browser. */
  openExternal: (url: string) => Promise<void>;
  /** Open a local file with the operating system's registered application. */
  openPath: (filePath: string) => Promise<{ ok: boolean; reason?: string; error?: string }>;
  /** Download a file by URL through Electron's native download system.
   *  Shows a native save dialog. On non-desktop platforms this is undefined. */
  downloadURL: (url: string) => Promise<void>;
  /** Hide macOS traffic lights for full-screen modals; restore when false. */
  setImmersiveMode: (immersive: boolean) => Promise<void>;
  /** Show a native OS notification for an inbox item or chat reply. */
  showNotification: (payload: {
    slug: string;
    itemId: string;
    issueKey?: string;
    chatSessionId?: string;
    title: string;
    body: string;
  }) => void;
  getWindowForeground: () => Promise<boolean>;
  onWindowForegroundChanged: (callback: (foreground: boolean) => void) => () => void;
  /** Update the OS dock / taskbar unread badge. Pass 0 to clear. */
  setUnreadBadge: (count: number) => void;
  /** Listen for inbox/chat navigation from notification clicks. Returns an unsubscribe function. */
  onInboxOpen: (
    callback: (payload: {
      slug: string;
      itemId: string;
      issueKey?: string;
      chatSessionId?: string;
    }) => void,
  ) => () => void;
  /** Listen for native macOS back/forward swipe gestures. Returns an unsubscribe function. */
  onNavigationGesture: (callback: (gesture: NavigationGesture) => void) => () => void;
  /** Listen for right-clicks the page left unhandled, to draw their menu. */
  onContextMenu: (callback: (request: ContextMenuRequest) => void) => () => void;
  /** Run the item picked in that menu. */
  runContextMenuCommand: (command: ContextMenuCommand) => void;
  /** Report the renderer's memory-router path for recovery diagnostics. */
  setRendererRouteContext: (context: RendererRouteContextInput) => void;
  /** Open the OS folder picker and return the chosen absolute path.
   *  Used by the Project settings "Add local directory" flow. */
  pickDirectory: (
    defaultPath?: string,
  ) => Promise<{
    ok: boolean;
    path?: string;
    basename?: string;
    reason?: "cancelled" | "no_window" | "error";
    error?: string;
  }>;
  /** Validate that a path is an existing readable+writable directory.
   *  Mirrors the daemon's runtime check so the user sees errors before submit. */
  validateLocalDirectory: (
    path: string,
  ) => Promise<{
    ok: boolean;
    reason?:
      | "not_absolute"
      | "not_found"
      | "not_a_directory"
      | "not_readable"
      | "not_writable"
      | "error";
    error?: string;
    /** Whether the path sits inside a git working tree. Only set when ok=true.
     *  Drives the worktree execution-mode option in the resource UI. */
    is_git_repo?: boolean;
  }>;
  /** Read every theme in the themes folder in user data, and start watching it. */
  readThemeFolder: () => Promise<ThemeFolderSnapshot>;
  /** Called once a change in the themes folder settles. Returns an unsubscribe function. */
  onThemeFolderChange: (callback: () => void) => () => void;
  /** Open the themes folder, or one theme's folder in it, in the file manager. */
  openThemeFolder: (name?: string) => Promise<{ ok: boolean; error?: string }>;
  /** Create a theme folder from files; never replaces an existing one. */
  writeThemeFolder: (name: string, files: ThemeFolderFile[]) => Promise<ThemeFolderWriteResult>;
  /** Listen for Cmd/Ctrl+W tab-close requests from the main process.
   *  Returns an unsubscribe function. */
  onCloseActiveTab: (callback: () => void) => () => void;
  /** Listen for a safe active-tab refresh request. */
  onReloadActiveTab: (callback: () => void) => () => void;
  /** Listen for Cmd/Ctrl+, requests to open Settings, delivered to the main
   *  window whichever window had focus. Returns an unsubscribe function. */
  onOpenSettings: (callback: () => void) => () => void;
  /** Listen for Cmd/Ctrl+1..9 tab-selection requests, delivered to the main
   *  window whichever window had focus. Returns an unsubscribe function. */
  onSelectTabShortcut: (
    callback: (key: TabSelectionShortcutKey) => void,
  ) => () => void;
  /** Ask the main process to close the window. */
  closeWindow: () => void;
  /** Open an issue-detail tab in a dedicated native window. */
  openIssueWindow: (
    request: IssueWindowRequest,
  ) => Promise<{ ok: true } | { ok: false; reason: "invalid_request" }>;
  /** Tell main which workspace this main window shows (null while none). */
  reportWindowWorkspace: (workspaceSlug: string | null) => void;
  /** Workspaces shown by the other main windows (synchronous). */
  getOtherWindowWorkspaces: () => string[];
  /** Hand a workspace (and optionally a path inside it) to the other main
   *  window that shows it. Returns false when no other window does. */
  focusWorkspaceWindow: (
    workspaceSlug: string,
    path?: string,
    options?: WorkspacePathOptions,
  ) => boolean;
  /** Open a workspace in its own main window, or focus the one showing it. */
  openWorkspaceWindow: (workspaceSlug: string) => Promise<{ ok: boolean }>;
  /** Listen for workspace paths routed here from another window.
   *  Returns an unsubscribe function. */
  onOpenWorkspacePath: (callback: (request: WorkspacePathRequest) => void) => () => void;
  /** Listen for Cmd/Ctrl+Shift+N new-window requests. Returns an
   *  unsubscribe function. */
  onNewWindowShortcut: (callback: () => void) => () => void;
  /** Whether View → Show Dev Toolbar is checked; false unless VITE_DEV_TOOLBAR is set. */
  getDevToolbarVisible: () => boolean;
  /** Listen for View → Show Dev Toolbar being toggled. Returns an unsubscribe function. */
  onDevToolbarVisibleChange: (callback: (visible: boolean) => void) => () => void;
}

type DaemonReauthResult =
  | { ok: true }
  | { ok: false; reason: "session_invalid" }
  | { ok: false; reason: "transient"; message: string };

type FocusEnvironmentResult =
  | { ok: true }
  | { ok: false; reason: "gone" | "unsupported" | "unavailable" | "failed"; error?: string };

interface DaemonAPI {
  listLocalApplications: () => Promise<unknown>;
  localApplicationSupported:(request:unknown)=>Promise<boolean>;
  localApplicationOperation: (request:unknown)=>Promise<unknown|null>;
  getBuiltinMcpServices: () => Promise<unknown>;
  getJevModels: () => Promise<unknown>;
  registerJevModel: (modelId: string, revision: string) => Promise<unknown>;
  installJevModel: (modelId: string, revision?: string) => Promise<unknown>;
  cancelJevModelInstall: (modelId: string, revision?: string) => Promise<unknown>;
  start: () => Promise<{ success: boolean; error?: string }>;
  stop: () => Promise<{ success: boolean; error?: string }>;
  restart: () => Promise<{ success: boolean; error?: string }>;
  getStatus: () => Promise<DaemonStatus>;
  getMcpReadiness: () => Promise<DaemonMcpReadiness[]>;
  probeRuntimes: () => Promise<LocalRuntimeProbe>;
  getHostName: () => Promise<string>;
  /** List daemon-owned task environments, including active-state protection. */
  listWorktrees: () => Promise<ManagedWorktree[]>;
  readLocalReview: (request: unknown) => Promise<unknown>;
  applyFileEdit: (request: unknown) => Promise<unknown>;
  readLocalReviewBranches: (request: unknown, readID?: string) => Promise<unknown>;
  readLocalReviewPage: (request: unknown, readID?: string) => Promise<unknown>;
  cancelLocalReviewRead: (readID: string) => void;
  reviewInventory: () => Promise<unknown>;
  /** Remove selected inactive daemon-owned task environments. */
  cleanupWorktrees: (paths: string[], discardChanges?: boolean) => Promise<ManagedWorktreeCleanupResult>;
  previewWorktreeCaches: (workspaceId?: string) => Promise<WorktreeCacheResult[]>;
  cleanWorktreeCaches: (selections: WorktreeCacheSelection[], workspaceId?: string) => Promise<WorktreeCacheResult[]>;
  listEnvironmentArchives: (workspaceId?: string) => Promise<import("@multica/core/types/worktree-archives").WorktreeArchiveSummary[]>;
  environmentArchiveOperation: (request: import("@multica/core/types/worktree-archives").WorktreeArchiveRequest) => Promise<import("@multica/core/types/worktree-archives").WorktreeArchiveResult[]>;
  /** Brings forward a desktop app environment the local daemon detected. */
  focusEnvironment: (key: string) => Promise<FocusEnvironmentResult>;
  onStatusChange: (callback: (status: DaemonStatus) => void) => () => void;
  setTargetApiUrl: (url: string) => Promise<void>;
  syncToken: (token: string, userId: string) => Promise<void>;
  clearToken: () => Promise<void>;
  reauthenticate: (
    token: string,
    userId: string,
  ) => Promise<DaemonReauthResult>;
  isCliInstalled: () => Promise<boolean>;
  getPrefs: () => Promise<DaemonPrefs>;
  setPrefs: (prefs: Partial<DaemonPrefs>) => Promise<DaemonPrefs>;
  autoStart: () => Promise<void>;
  retryInstall: () => Promise<void>;
  startLogStream: () => void;
  stopLogStream: () => void;
  onLogLine: (callback: (line: string) => void) => () => void;
  openLogFile: () => Promise<{ success: boolean; error?: string }>;
  /** List live per-execution task workers on the daemon. Local only. */
  getWorkerProcesses: () => Promise<DaemonWorkerProcess[]>;
  /** Unified overview of every child process the daemon supervises, with best-effort resource usage. Local only. */
  getProcessOverview: () => Promise<DaemonProcessOverviewResult>;
  /** Stop one live worker process by its execution id. Local only. */
  stopWorkerProcess: (execId: string) => Promise<unknown>;
}

interface UpdaterAPI {
  onUpdateAvailable: (callback: (info: { version: string; releaseNotes?: string }) => void) => () => void;
  onDownloadProgress: (callback: (progress: { percent: number }) => void) => () => void;
  onUpdateDownloaded: (
    callback: (info: { version: string; releaseNotes?: string }) => void,
  ) => () => void;
  downloadUpdate: () => Promise<void>;
  installUpdate: () => Promise<void>;
  getPreferences: () => Promise<UpdaterPreferences>;
  setAutomaticUpdates: (enabled: boolean) => Promise<UpdaterPreferences>;
  checkForUpdates: () => Promise<ManualUpdateCheckResult>;
}

declare global {
  interface Window {
    localVoiceAPI?: LocalVoiceAPI;
    runtimeMirrorAPI?: RuntimeMirrorPreloadAPI;
    electron: ElectronAPI;
    desktopAPI: DesktopAPI;
    daemonAPI: DaemonAPI;
    updater: UpdaterAPI;
  }
}

export {};
