package migrations

import (
	"path/filepath"
	"testing"
)

func TestRenumberedMigrationsKeepPublishedLedgerIdentities(t *testing.T) {
	for _, version := range []string{
		"451_notification_bot", "452_notification_bot_owner_index",
		"453_notification_bot_delivery_unique", "454_notification_bot_delivery_ready",
		"455_issue_goal_mode", "457_pinned_item_runtime_mirror",
		"458_runtime_mirror_event", "459_runtime_mirror_event_index",
		"468_drop_reference_only_column",
	} {
		for _, direction := range []string{"up", "down"} {
			if got := ExtractVersion(filepath.Join("migrations", "900"+version+"."+direction+".sql")); got != version {
				t.Fatalf("ledger identity changed: %s => %s", version, got)
			}
		}
	}
	if got := ExtractVersion("900999_future_change.up.sql"); got != "900999_future_change" {
		t.Fatalf("unexpected alias applied to a new migration: %s", got)
	}
}

func TestMigrationLedgerIdentitiesAreUnique(t *testing.T) {
	seen := make(map[string]string)
	for _, file := range migrationFilesForLint(t, "*.up.sql") {
		version := ExtractVersion(file)
		if previous, exists := seen[version]; exists {
			t.Errorf("%s and %s map to the same ledger identity %s", previous, file, version)
		}
		seen[version] = file
	}
}
