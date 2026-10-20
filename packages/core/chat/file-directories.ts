import type { ChatSessionFilesResponse } from "../types/chat";

export interface ChatFileDirectory {
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
  const directories: ChatFileDirectory[] = [];
  const seen = new Set<string>();
  const append = (directory: ChatFileDirectory) => {
    const identity = `${directory.projectResourceId ? "resource" : "task"}:${directory.runtimeId ?? directory.key}:${directory.path.replace(/[\\/]+$/, "")}`;
    if (seen.has(identity)) return;
    seen.add(identity);
    directories.push(directory);
  };
  const tasks = [...files.tasks, ...(files.project_tasks ?? [])];
  for (const directory of files.directories ?? []) {
    append({
      key: `resource:${directory.project_resource_id}`,
      path: directory.path,
      runtimeId: directory.runtime_id,
      label: directory.label,
      projectResourceId: directory.project_resource_id,
    });
  }
  for (const task of tasks) {
    if (!task.work_dir || (projectId && task.project_id && task.project_id !== projectId)) continue;
    append({ key: `task:${task.id}`, path: task.work_dir, runtimeId: task.runtime_id, taskId: task.id, branchName: task.branch_name });
  }
  return directories;
}
