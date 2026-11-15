export type ApplicationView = "projects" | "runtimes" | "board";

export function applicationViewFromParam(value: string): ApplicationView {
  return value === "runtimes" || value === "board" ? value : "projects";
}

export type ApplicationTab =
  | "instances"
  | "configuration"
  | "orchestration"
  | "activity"
  | "logs";

export function applicationTabFromParam(value: string): ApplicationTab {
  switch (value) {
    case "configuration":
    case "orchestration":
    case "activity":
    case "logs":
      return value;
    default:
      return "instances";
  }
}
