package daemon

import "strings"

func isVscreenToolName(name string) bool {
	index := strings.LastIndex(name, "vscreen_")
	if index < 0 {
		return false
	}
	switch name[index:] {
	case "vscreen_status", "vscreen_acquire", "vscreen_release", "vscreen_list_apps", "vscreen_launch_app", "vscreen_observe", "vscreen_click", "vscreen_drag", "vscreen_scroll", "vscreen_type", "vscreen_key":
		return true
	}
	return false
}
func vscreenTranscriptInput(name string, input map[string]any) map[string]any {
	if isVscreenToolName(name) {
		return map[string]any{"managed_virtual_screen": true}
	}
	return input
}
