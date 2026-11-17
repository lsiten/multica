import type { ChatSessionFilesResponse } from "../types/chat";

export interface ChatFileDirectory {
  readonly scope: "project" | "current_chat" | "project_task";
  readonly source?: "project" | "current_chat" | "project_task";
  readonly readOnly?: boolean;
  readonly key: string;
  readonly path: string;
  readonly runtimeId?: string;
  readonly taskId?: string;
  readonly projectResourceId?: string;
  readonly label?: string;
  readonly branchName?: string;
}

/** Keep every selectable root; reading one directory must not hide its peers. */
export function chatFileDirectories(files: ChatSessionFilesResponse | undefined, projectId?: string): ChatFileDirectory[] {
  if (!files) return [];
  // A multi-project chat must stay empty until the user chooses a project.
  // The server keeps `tasks` for older clients and chat-level projections, so
  // filtering only the project task list is not enough to prevent an implicit
  // worktree read here.
  if (files.selection_required && !projectId) return [];
  const directories: ChatFileDirectory[] = [];
  const seen = new Set<string>();
  const append = (directory: ChatFileDirectory) => {
    const identity = `${directory.projectResourceId ? "resource" : "task"}:${directory.runtimeId ?? directory.key}:${directory.path.replace(/[\\/]+$/, "")}`;
    if (seen.has(identity)) return;
    seen.add(identity);
    directories.push(directory);
  };
  const currentTaskIds = new Set(files.tasks.map((task) => task.id));
  const tasks = [...files.tasks, ...(files.project_tasks ?? [])];
  for (const directory of files.directories ?? []) {
    append({
      scope: "project",
      source: "project",
      readOnly: true,
      key: `resource:${directory.project_resource_id}`,
      path: directory.path,
      runtimeId: directory.runtime_id,
      label: directory.label,
      projectResourceId: directory.project_resource_id,
    });
  }
  for (const task of tasks) {
    if (!task.work_dir || (projectId && task.project_id && task.project_id !== projectId)) continue;
    const scope = currentTaskIds.has(task.id) ? "current_chat" : "project_task";
    append({ scope, source: scope, readOnly: false, key: `task:${task.id}`, path: task.work_dir, runtimeId: task.runtime_id, taskId: task.id, branchName: task.branch_name });
  }
  return directories;
}
